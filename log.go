package log

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"
)

const (
	// ENV_LOG_LEVEL is the environment variable for the log level.
	ENV_LOG_LEVEL = "LOG_LEVEL"
	// ENV_LOG_FORMAT is the environment variable for the log format.
	ENV_LOG_FORMAT = "LOG_FORMAT"
	// ENV_LOG_BUFFER_SIZE is the environment variable for the log buffer size.
	ENV_LOG_BUFFER_SIZE = "LOG_BUFFER_SIZE"
	// ENV_LOG_RATE_LIMIT is the environment variable for the log rate limit.
	ENV_LOG_RATE_LIMIT = "LOG_RATE_LIMIT" // messages per second
	// ENV_LOG_RATE_BURST is the environment variable for the log rate burst size.
	ENV_LOG_RATE_BURST = "LOG_RATE_BURST" // burst size
	// ENV_LOG_DROP_REPORT_INTERVAL is the environment variable for the log drop report interval.
	ENV_LOG_DROP_REPORT_INTERVAL = "LOG_DROP_REPORT_INTERVAL" // seconds between drop reports
	// DEFAULT_BUFFER_SIZE is the default buffer size for the logger.
	DEFAULT_BUFFER_SIZE = 100000 // High throughput: 100K buffer
	// DEFAULT_RATE_LIMIT is the default rate limit for the logger in messages per second.
	DEFAULT_RATE_LIMIT = 50000 // High throughput: 50K msgs/sec
	// DEFAULT_RATE_BURST is the default rate burst for the logger.
	DEFAULT_RATE_BURST = 10000 // High throughput: 10K burst
	// DEFAULT_DROP_REPORT_INTERVAL is the default interval in seconds for reporting dropped messages.
	DEFAULT_DROP_REPORT_INTERVAL = 10 // Report drops every 10 seconds
	// KEY_PKG is the key used for the package name in log fields.
	KEY_PKG = "pkg"
)

var (
	// Logger is the global instance of the CustomLogger.
	Logger = NewLogger()
	// ParseLevel parses a string into a zerolog.Level. It's a convenience wrapper around zerolog.ParseLevel.
	ParseLevel = zerolog.ParseLevel

	// DebugLevel defines the debug log level.
	DebugLevel = zerolog.DebugLevel
	// InfoLevel defines the info log level.
	InfoLevel = zerolog.InfoLevel
	// WarnLevel defines the warn log level.
	WarnLevel = zerolog.WarnLevel
	// ErrorLevel defines the error log level.
	ErrorLevel = zerolog.ErrorLevel
	// FatalLevel defines the fatal log level.
	FatalLevel = zerolog.FatalLevel
	// PanicLevel defines the panic log level.
	PanicLevel = zerolog.PanicLevel
	// NoLevel defines an absent log level.
	NoLevel = zerolog.NoLevel
	// Disabled disables the logger.
	Disabled = zerolog.Disabled
	// TraceLevel defines the trace log level.
	TraceLevel = zerolog.TraceLevel
)

// CustomLogger wraps zerolog.Logger to provide additional functionalities like
// rate limiting, buffering, and custom formatting.
type CustomLogger struct {
	zerolog.Logger
	writer     *BufferedRateLimitedWriter
	bufferSize int
	once       sync.Once
}

// BufferedRateLimitedWriter wraps an io.Writer with rate limiting and buffering.
// It ensures that logs are written at a controlled pace and buffers messages
// to handle bursts, dropping messages if the buffer is full and reporting drops.
type BufferedRateLimitedWriter struct {
	target  io.Writer
	limiter *rate.Limiter
	buffer  chan []byte
	wg      sync.WaitGroup
	// mu      sync.Mutex // Mutex removed
	once sync.Once

	closed      atomic.Bool   // Changed from bool with mutex
	closeSignal chan struct{} // Added for shutdown signaling

	// Drop tracking and reporting
	droppedCount       atomic.Uint64
	dropReportTicker   *time.Ticker
	dropReportInterval time.Duration
	reportWg           sync.WaitGroup

	// Pre-computed drop report parts for performance
	dropReportPrefix   []byte
	dropReportSuffix   []byte
	intervalSecondsStr string
}

// NewBufferedRateLimitedWriter creates a new BufferedRateLimitedWriter.
// It takes a target io.Writer, buffer size, rate limit (messages per second),
// and rate burst as parameters.
// The drop report interval can be configured via the ENV_LOG_DROP_REPORT_INTERVAL
// environment variable.
func NewBufferedRateLimitedWriter(target io.Writer, bufferSize int, rateLimit, rateBurst int) *BufferedRateLimitedWriter {
	// Get drop report interval from environment
	dropReportIntervalSec := DEFAULT_DROP_REPORT_INTERVAL
	if intervalStr := os.Getenv(ENV_LOG_DROP_REPORT_INTERVAL); intervalStr != "" {
		if parsed, err := strconv.Atoi(intervalStr); err == nil && parsed > 0 {
			dropReportIntervalSec = parsed
		}
	}

	w := &BufferedRateLimitedWriter{
		target:             target,
		limiter:            rate.NewLimiter(rate.Limit(rateLimit), rateBurst),
		buffer:             make(chan []byte, bufferSize),
		closeSignal:        make(chan struct{}), // Initialize closeSignal
		dropReportInterval: time.Duration(dropReportIntervalSec) * time.Second,
	}

	// Pre-compute static parts of drop report message for performance
	w.intervalSecondsStr = strconv.FormatFloat(w.dropReportInterval.Seconds(), 'f', 0, 64)
	w.dropReportPrefix = []byte(`{"time":"`)
	w.dropReportSuffix = []byte(`","level":"warn","message":"Log messages dropped due to backpressure","dropped_count":`)

	return w
}

func (w *BufferedRateLimitedWriter) startProcessor() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for data := range w.buffer {
			_ = w.limiter.Wait(context.Background())
			_, _ = w.target.Write(data)
		}
	}()
}

func (w *BufferedRateLimitedWriter) startDropReporter() {
	w.reportWg.Add(1) // Increment WaitGroup counter before starting goroutine
	go func() {
		defer w.reportWg.Done()

		// Only start ticker if interval is positive
		if w.dropReportInterval > 0 {
			w.dropReportTicker = time.NewTicker(w.dropReportInterval)
			defer w.dropReportTicker.Stop()
		} else {
			// If no ticker, block on closeSignal directly to keep goroutine alive
			// until close, otherwise it exits immediately if drop reporting is disabled.
			<-w.closeSignal
			return
		}

		for {
			select {
			case <-w.dropReportTicker.C:
				// Atomically read and reset the dropped count
				dropped := w.getAndResetDroppedCount()
				if dropped > 0 {
					// Create drop report message
					timestamp := time.Now().Format(time.RFC3339)
					droppedStr := strconv.FormatUint(dropped, 10)

					// Simplified and corrected capacity calculation for valid JSON construction
					capacity := len(w.dropReportPrefix) + len(timestamp) + len(w.dropReportSuffix) +
						len(droppedStr) + len(",\"interval_seconds\":\"") + len(w.intervalSecondsStr) +
						len("\"}") + len("\n") // Closing quote for interval_seconds value, closing brace, and newline

					buf := make([]byte, 0, capacity)
					buf = append(buf, w.dropReportPrefix...)
					buf = append(buf, timestamp...)
					buf = append(buf, w.dropReportSuffix...)
					buf = append(buf, droppedStr...)
					buf = append(buf, []byte(",\"interval_seconds\":\"")...)
					buf = append(buf, w.intervalSecondsStr...)
					buf = append(buf, []byte("\"}")...) // Closing quote for interval_seconds value and closing brace
					buf = append(buf, []byte("\n")...)

					// Write directly to target to avoid infinite recursion
					_, _ = w.target.Write(buf)
				}
			case <-w.closeSignal:
				return
			}
		}
	}()
}

func (w *BufferedRateLimitedWriter) getAndResetDroppedCount() uint64 {
	return w.droppedCount.Swap(0)
}

// Write writes the provided byte slice to the buffer.
// It implements io.Writer. If the buffer is full, messages are dropped,
// and the drop count is incremented.
// It starts the processor and drop reporter on the first write.
func (w *BufferedRateLimitedWriter) Write(p []byte) (int, error) {
	if w.closed.Load() { // Use atomic Load
		return 0, io.ErrClosedPipe
	}

	w.once.Do(func() {
		w.startProcessor()
		w.startDropReporter()
	})

	// Make a copy of the byte slice since zerolog may reuse it
	// This is the minimal copying we need to do for buffering
	dataCopy := make([]byte, len(p))
	copy(dataCopy, p)

	// Non-blocking send with backpressure (drop if buffer full)
	select {
	case w.buffer <- dataCopy:
		return len(p), nil
	default:
		// Buffer full - implement backpressure by dropping
		// Increment drop counter atomically
		w.droppedCount.Add(1) // Changed from mutex-guarded increment
		return len(p), nil    // Return success to not break the logger
	}
}

// Close closes the BufferedRateLimitedWriter, ensuring all buffered messages are processed
// and the drop reporter is stopped.
func (w *BufferedRateLimitedWriter) Close() error {
	if !w.closed.CompareAndSwap(false, true) { // Use atomic CAS
		return nil // Already closed or closing
	}

	close(w.closeSignal) // Signal drop reporter to stop

	// Close the buffer and wait for processor to finish
	close(w.buffer)
	w.wg.Wait()

	// Wait for drop reporter to finish
	w.reportWg.Wait()
	return nil
}

// Close closes the CustomLogger and its underlying BufferedRateLimitedWriter.
func (l *CustomLogger) Close() {
	if l.writer != nil {
		_ = l.writer.Close()
	}
}

// SetLogger replaces the underlying zerolog.Logger instance in the CustomLogger.
func (l *CustomLogger) SetLogger(logger zerolog.Logger) {
	l.Logger = logger
}

// getLogLevel parses and validates the log level from environment
func getLogLevel() zerolog.Level {
	logLevel, err := ParseLevel(os.Getenv(ENV_LOG_LEVEL))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error parsing log level: %v. Using default info level.\\n", err)
		logLevel = InfoLevel
	}
	if logLevel == NoLevel {
		logLevel = InfoLevel
	}
	return logLevel
}

// getBufferConfig parses buffer and rate limiting configuration from environment variables:
// ENV_LOG_BUFFER_SIZE, ENV_LOG_RATE_LIMIT, and ENV_LOG_RATE_BURST.
// It returns the buffer size, rate limit, and rate burst, using default values if
// environment variables are not set or invalid.
func getBufferConfig() (bufferSize, rateLimit, rateBurst int) {
	bufferSizeStr := os.Getenv(ENV_LOG_BUFFER_SIZE)
	bufferSize, err := strconv.Atoi(bufferSizeStr)
	if err != nil || bufferSize <= 0 {
		bufferSize = DEFAULT_BUFFER_SIZE
	}

	rateLimitStr := os.Getenv(ENV_LOG_RATE_LIMIT)
	rateLimit, err = strconv.Atoi(rateLimitStr)
	if err != nil || rateLimit <= 0 {
		rateLimit = DEFAULT_RATE_LIMIT
	}

	rateBurstStr := os.Getenv(ENV_LOG_RATE_BURST)
	rateBurst, err = strconv.Atoi(rateBurstStr)
	if err != nil || rateBurst <= 0 {
		rateBurst = DEFAULT_RATE_BURST
	}

	return bufferSize, rateLimit, rateBurst
}

// getFormatBasedOutput determines output writer based on log format
func getFormatBasedOutput() io.Writer {
	logFormat, _ := ParseLogFormat(os.Getenv(ENV_LOG_FORMAT))

	var output io.Writer = os.Stderr
	if logFormat == LOG_FORMAT_CONSOLE {
		output = zerolog.ConsoleWriter{
			Out:        PreferredWriter(),
			TimeFormat: time.RFC3339,
		}
	}
	return output
}

// newCustomLogger creates a new CustomLogger with the specified output writer.
// It configures the logger based on environment variables for log level,
// buffer size, rate limit, and rate burst.
func newCustomLogger(output io.Writer) *CustomLogger {
	logLevel := getLogLevel()
	bufferSize, rateLimit, rateBurst := getBufferConfig()

	// Create the buffered rate limited writer
	bufferedWriter := NewBufferedRateLimitedWriter(output, bufferSize, rateLimit, rateBurst)

	zl := zerolog.New(bufferedWriter).
		With().
		Timestamp().
		Logger().Level(logLevel)

	return &CustomLogger{
		Logger:     zl,
		writer:     bufferedWriter,
		bufferSize: bufferSize,
	}
}

// NewLogger creates a new CustomLogger instance.
// The logger's output format (console or JSON) is determined by the
// ENV_LOG_FORMAT environment variable.
// It uses a BufferedRateLimitedWriter for output.
func NewLogger() *CustomLogger {
	return newCustomLogger(getFormatBasedOutput())
}

// NewJsonLogger creates a new CustomLogger instance that always outputs in JSON format to os.Stderr.
// It uses a BufferedRateLimitedWriter for output.
func NewJsonLogger() *CustomLogger {
	return newCustomLogger(os.Stderr)
}

// PreferredWriter returns an io.Writer that writes to both a new bytes.Buffer and os.Stderr.
// This is typically used for console logging to capture output for testing or other purposes
// while still printing to the standard error stream.
func PreferredWriter() io.Writer {
	return io.MultiWriter(
		new(bytes.Buffer),
		os.Stderr,
	)
}

// SetLoggerLogLevel sets the global log level for the default Logger.
// If the provided level string is empty, it defaults to InfoLevel.
// It returns an error if the level string is invalid.
func SetLoggerLogLevel(level string) error {
	if len(level) == 0 {
		level = InfoLevel.String()
	}
	parsedLevel, err := ParseLevel(level)
	if err != nil {
		return err
	}
	// Update the underlying zerolog.Logger instance
	Logger.Logger = Logger.Logger.Level(parsedLevel)
	return nil
}

// Close closes the global Logger instance, ensuring cleanup of its resources.
// This should be called when the application is shutting down to flush any buffered logs.
func Close() {
	Logger.Close()
}
