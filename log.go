package log

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
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
	// ENV_LOG_GROUP_WINDOW is the environment variable for the log event grouping window.
	ENV_LOG_GROUP_WINDOW = "LOG_GROUP_WINDOW" // seconds for event grouping window
	// DEFAULT_BUFFER_SIZE is the default buffer size for the logger.
	DEFAULT_BUFFER_SIZE = 100000 // High throughput: 100K buffer
	// DEFAULT_RATE_LIMIT is the default rate limit for the logger in messages per second.
	DEFAULT_RATE_LIMIT = 50000 // High throughput: 50K msgs/sec
	// DEFAULT_RATE_BURST is the default rate burst for the logger.
	DEFAULT_RATE_BURST = 10000 // High throughput: 10K burst
	// DEFAULT_DROP_REPORT_INTERVAL is the default interval in seconds for reporting dropped messages.
	DEFAULT_DROP_REPORT_INTERVAL = 10 // Report drops every 10 seconds
	// DEFAULT_GROUP_WINDOW is the default window in seconds for grouping similar events.
	DEFAULT_GROUP_WINDOW = 1 // Group events over 1 second window
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

// eventKey represents a unique identifier for grouping similar log events.
// It uses the message content hash and level to identify similar events.
type eventKey struct {
	hash  uint64
	level string
}

// eventWindow tracks events within a time window for grouping.
type eventWindow struct {
	firstSeen time.Time
	count     atomic.Uint64
	message   []byte // Store the first occurrence message
}

// eventGrouper manages event grouping within time windows.
type eventGrouper struct {
	events      sync.Map // Use sync.Map for better concurrent performance
	windowDur   time.Duration
	cleanupChan chan struct{}
	wg          sync.WaitGroup
	writer      io.Writer // Reference to output writer for emitting grouped messages
}

// newEventGrouper creates a new event grouper with the specified window duration.
func newEventGrouper(windowDur time.Duration, writer io.Writer) *eventGrouper {
	eg := &eventGrouper{
		windowDur:   windowDur,
		cleanupChan: make(chan struct{}),
		writer:      writer,
	}

	// Start cleanup goroutine if grouping is enabled
	if windowDur > 0 {
		eg.wg.Add(1)
		go eg.cleanupExpired()
	}

	return eg
}

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
// It also supports event grouping to reduce duplicate log noise.
type BufferedRateLimitedWriter struct {
	target    io.Writer
	targetMux sync.Mutex // Protects concurrent writes to target
	limiter   *rate.Limiter
	buffer    chan []byte
	wg        sync.WaitGroup
	once      sync.Once

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

	// Event grouping
	grouper *eventGrouper
}

// NewBufferedRateLimitedWriter creates a new BufferedRateLimitedWriter.
// It takes a target io.Writer, buffer size, rate limit (messages per second),
// rate burst, and group window duration as parameters.
// The drop report interval can be configured via the ENV_LOG_DROP_REPORT_INTERVAL
// environment variable, and group window via ENV_LOG_GROUP_WINDOW.
func NewBufferedRateLimitedWriter(target io.Writer, bufferSize int, rateLimit, rateBurst int) *BufferedRateLimitedWriter {
	return NewBufferedRateLimitedWriterWithGrouping(target, bufferSize, rateLimit, rateBurst, 0)
}

// NewBufferedRateLimitedWriterWithGrouping creates a new BufferedRateLimitedWriter with event grouping.
// If groupWindow is positive, that duration is used for grouping.
// If groupWindow is 0, the default grouping window is used (from environment or DEFAULT_GROUP_WINDOW).
// If groupWindow is negative, grouping is disabled.
func NewBufferedRateLimitedWriterWithGrouping(target io.Writer, bufferSize int, rateLimit, rateBurst int, groupWindow time.Duration) *BufferedRateLimitedWriter {
	// Get drop report interval from environment
	dropReportIntervalSec := DEFAULT_DROP_REPORT_INTERVAL
	if intervalStr := os.Getenv(ENV_LOG_DROP_REPORT_INTERVAL); intervalStr != "" {
		if parsed, err := strconv.Atoi(intervalStr); err == nil && parsed > 0 {
			dropReportIntervalSec = parsed
		}
	}

	// Get group window from environment if not explicitly provided (groupWindow == 0)
	if groupWindow == 0 {
		groupWindowSec := DEFAULT_GROUP_WINDOW
		if windowStr := os.Getenv(ENV_LOG_GROUP_WINDOW); windowStr != "" {
			if parsed, err := strconv.Atoi(windowStr); err == nil && parsed >= 0 {
				groupWindowSec = parsed
			}
		}
		groupWindow = time.Duration(groupWindowSec) * time.Second
	}
	// If groupWindow is negative, it means grouping is explicitly disabled
	if groupWindow < 0 {
		groupWindow = 0
	}

	w := &BufferedRateLimitedWriter{
		target:             target,
		limiter:            rate.NewLimiter(rate.Limit(rateLimit), rateBurst),
		buffer:             make(chan []byte, bufferSize),
		closeSignal:        make(chan struct{}), // Initialize closeSignal
		dropReportInterval: time.Duration(dropReportIntervalSec) * time.Second,
		grouper:            newEventGrouper(groupWindow, target),
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

			// Protect concurrent writes to target with mutex
			w.targetMux.Lock()
			_, _ = w.target.Write(data)
			w.targetMux.Unlock()
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
					w.targetMux.Lock()
					_, _ = w.target.Write(buf)
					w.targetMux.Unlock()
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

	// Check if this event should be grouped
	messageToWrite, wasGrouped := w.grouper.shouldGroup(p)
	if wasGrouped {
		// Event was grouped (suppressed), but we still report success
		return len(p), nil
	}

	if messageToWrite == nil {
		// Shouldn't happen, but guard against it
		return len(p), nil
	}

	// Make a copy of the byte slice since zerolog may reuse it
	// This is the minimal copying we need to do for buffering
	dataCopy := make([]byte, len(messageToWrite))
	copy(dataCopy, messageToWrite)

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

	// Close the event grouper and emit any pending grouped messages
	pendingMessages := w.grouper.close()
	for _, msg := range pendingMessages {
		w.targetMux.Lock()
		_, _ = w.target.Write(msg)
		w.targetMux.Unlock()
	}

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
// buffer size, rate limit, rate burst, and event grouping window.
func newCustomLogger(output io.Writer) *CustomLogger {
	logLevel := getLogLevel()
	bufferSize, rateLimit, rateBurst := getBufferConfig()

	// Get group window configuration (event grouping is enabled by default)
	groupWindowSec := DEFAULT_GROUP_WINDOW
	if windowStr := os.Getenv(ENV_LOG_GROUP_WINDOW); windowStr != "" {
		if parsed, err := strconv.Atoi(windowStr); err == nil && parsed >= 0 {
			groupWindowSec = parsed
		}
	}
	groupWindow := time.Duration(groupWindowSec) * time.Second

	// Create the buffered rate limited writer with event grouping enabled by default
	bufferedWriter := NewBufferedRateLimitedWriterWithGrouping(output, bufferSize, rateLimit, rateBurst, groupWindow)

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

// NewLogger creates a new CustomLogger instance with event grouping enabled by default.
// The logger's output format (console or JSON) is determined by the
// ENV_LOG_FORMAT environment variable.
// Event grouping can be configured via ENV_LOG_GROUP_WINDOW (default: 1 second).
// Use NewLoggerWithoutGrouping() to disable event grouping.
// It uses a BufferedRateLimitedWriter for output.
func NewLogger() *CustomLogger {
	return newCustomLogger(getFormatBasedOutput())
}

// NewJsonLogger creates a new CustomLogger instance that always outputs in JSON format to os.Stderr.
// It uses a BufferedRateLimitedWriter for output.
func NewJsonLogger() *CustomLogger {
	return newCustomLogger(os.Stderr)
}

// NewLoggerWithGrouping creates a new CustomLogger instance with event grouping enabled.
// The groupWindow parameter specifies the time window for grouping similar events.
// If groupWindow is 0, grouping is disabled.
func NewLoggerWithGrouping(groupWindow time.Duration) *CustomLogger {
	output := getFormatBasedOutput()
	logLevel := getLogLevel()
	bufferSize, rateLimit, rateBurst := getBufferConfig()

	// Create the buffered rate limited writer with grouping
	bufferedWriter := NewBufferedRateLimitedWriterWithGrouping(output, bufferSize, rateLimit, rateBurst, groupWindow)

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

// NewLoggerWithoutGrouping creates a new CustomLogger instance with event grouping explicitly disabled.
// This is useful when you want to disable the default event grouping behavior.
func NewLoggerWithoutGrouping() *CustomLogger {
	output := getFormatBasedOutput()
	logLevel := getLogLevel()
	bufferSize, rateLimit, rateBurst := getBufferConfig()

	// Create the buffered rate limited writer without grouping (negative value disables grouping)
	bufferedWriter := NewBufferedRateLimitedWriterWithGrouping(output, bufferSize, rateLimit, rateBurst, -1)

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

// Hash computes the hash of the given byte slice using the FNV-1a algorithm.
// It returns the hash value as an uint64.
func Hash(data []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}

// GroupEvents groups the given events by their hash value computed from the
// event data. It returns a map where the key is the hash value and the value
// is a slice of events (byte slices) that correspond to the same hash.
func GroupEvents(events [][]byte) map[uint64][][]byte {
	grouped := make(map[uint64][][]byte)
	for _, event := range events {
		hash := Hash(event)
		grouped[hash] = append(grouped[hash], event)
	}
	return grouped
}

// EventHash is a helper function that computes the hash of an event and returns
// it as a string. This can be used for logging or debugging purposes to identify
// events.
func EventHash(event []byte) string {
	return fmt.Sprintf("%x", Hash(event))
}

// hashMessage creates a hash of the log message content for grouping.
func (eg *eventGrouper) hashMessage(data []byte) uint64 {
	// Extract the message part from JSON log for hashing
	// We'll hash the entire message for simplicity and performance
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}

// extractLevel extracts the log level from the JSON log message.
func (eg *eventGrouper) extractLevel(data []byte) string {
	// Simple extraction of level field from JSON
	// This is a fast, zero-allocation approach
	start := bytes.Index(data, []byte(`"level":"`))
	if start == -1 {
		return "unknown"
	}
	start += 9 // len(`"level":"`)
	end := bytes.Index(data[start:], []byte(`"`))
	if end == -1 {
		return "unknown"
	}
	return string(data[start : start+end])
}

// shouldGroup determines if an event should be grouped and returns the modified message.
// Returns the message to write and whether it was grouped.
func (eg *eventGrouper) shouldGroup(data []byte) ([]byte, bool) {
	if eg.windowDur <= 0 {
		return data, false
	}

	hash := eg.hashMessage(data)
	level := eg.extractLevel(data)
	key := eventKey{hash: hash, level: level}
	now := time.Now()

	// Try to load existing window
	if value, exists := eg.events.Load(key); exists {
		window := value.(*eventWindow)

		// Check if the event is within the time window
		if now.Sub(window.firstSeen) <= eg.windowDur {
			// Within window, increment count atomically and suppress this message
			window.count.Add(1)
			return nil, true
		}

		// Window expired, try to replace with new window
		newWindow := &eventWindow{
			firstSeen: now,
			message:   append([]byte(nil), data...), // Copy the new message
		}
		newWindow.count.Store(1)

		// Create grouped message from expired window
		groupedMsg := eg.createGroupedMessage(window, now)

		// Try to replace the old window with the new one
		// If another goroutine replaced it, that's fine - we still emit the grouped message
		eg.events.Store(key, newWindow)

		return groupedMsg, false
	}

	// First occurrence of this event - store it
	newWindow := &eventWindow{
		firstSeen: now,
		message:   append([]byte(nil), data...), // Copy the message
	}
	newWindow.count.Store(1)

	// Try to store the new window
	if _, loaded := eg.events.LoadOrStore(key, newWindow); !loaded {
		// Successfully stored new window, return original message
		return data, false
	}

	// Another goroutine stored a window for this key, retry
	return eg.shouldGroup(data)
}

// createGroupedMessage creates a grouped message showing the count.
func (eg *eventGrouper) createGroupedMessage(window *eventWindow, now time.Time) []byte {
	count := window.count.Load()
	if count <= 1 {
		return window.message
	}

	// Parse the original message to add count information
	// We'll modify the JSON to include a count field
	msg := make([]byte, 0, len(window.message)+100) // Pre-allocate with extra space

	// Find the closing brace and insert count before it
	lastBrace := bytes.LastIndex(window.message, []byte("}"))
	if lastBrace == -1 {
		// Fallback: just append the original message
		return window.message
	}

	// Copy everything up to the last brace
	msg = append(msg, window.message[:lastBrace]...)

	// Add count and grouped indicator
	countStr := strconv.FormatUint(count, 10)
	windowDurStr := eg.windowDur.String()

	msg = append(msg, []byte(`,"group_count":`)...)
	msg = append(msg, countStr...)
	msg = append(msg, []byte(`,"group_window":"`)...)
	msg = append(msg, windowDurStr...)
	msg = append(msg, []byte(`","group_first":"`)...)
	msg = append(msg, window.firstSeen.Format(time.RFC3339)...)
	msg = append(msg, []byte(`"}`)...)
	msg = append(msg, '\n')

	return msg
}

// cleanupExpired removes expired event windows.
func (eg *eventGrouper) cleanupExpired() {
	defer eg.wg.Done()

	ticker := time.NewTicker(eg.windowDur / 2) // Cleanup twice per window
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			eg.performCleanup()
		case <-eg.cleanupChan:
			// Final cleanup before shutdown
			eg.performCleanup()
			return
		}
	}
}

// performCleanup removes expired event windows and emits final grouped messages.
func (eg *eventGrouper) performCleanup() {
	now := time.Now()
	var expiredMessages [][]byte

	// Use Range to iterate over sync.Map
	eg.events.Range(func(key, value interface{}) bool {
		window := value.(*eventWindow)
		if now.Sub(window.firstSeen) > eg.windowDur {
			// Create grouped message for expired window if it has multiple events
			if window.count.Load() > 1 {
				groupedMsg := eg.createGroupedMessage(window, now)
				expiredMessages = append(expiredMessages, groupedMsg)
			}
			// Delete expired window
			eg.events.Delete(key)
		}
		return true // Continue iteration
	})

	// Emit expired grouped messages through the writer's target
	// We need access to the writer to emit these messages
	if len(expiredMessages) > 0 && eg.writer != nil {
		for _, msg := range expiredMessages {
			_, _ = eg.writer.Write(msg)
		}
	}
}

// close shuts down the event grouper and returns any pending grouped messages.
func (eg *eventGrouper) close() [][]byte {
	var pendingMessages [][]byte

	if eg.windowDur > 0 {
		// Collect all pending grouped messages before shutdown
		now := time.Now()
		eg.events.Range(func(key, value interface{}) bool {
			window := value.(*eventWindow)
			if window.count.Load() > 1 {
				groupedMsg := eg.createGroupedMessage(window, now)
				pendingMessages = append(pendingMessages, groupedMsg)
			}
			return true
		})

		close(eg.cleanupChan)
		eg.wg.Wait()
	}

	return pendingMessages
}
