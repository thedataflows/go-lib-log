package log

import (
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// LoggerBuilder provides a fluent interface for configuring and building CustomLogger instances.
type LoggerBuilder struct {
	output            io.Writer
	bufferSize        int
	rateLimit         int
	rateBurst         int
	groupWindow       time.Duration
	bufferingDisabled bool
	groupingDisabled  bool
	logLevel          zerolog.Level
	forceJSON         bool
}

// NewLogger creates a new LoggerBuilder with default configuration.
// This is the only constructor function - all other configurations are done through the builder pattern.
func NewLogger() *LoggerBuilder {
	return &LoggerBuilder{
		bufferSize:        -1, // Use default from environment
		rateLimit:         -1, // Use default from environment
		rateBurst:         -1, // Use default from environment
		groupWindow:       0,  // Use default from environment
		bufferingDisabled: false,
		groupingDisabled:  false,
		logLevel:          NoLevel, // Use default from environment
		forceJSON:         false,
	}
}

// WithOutput sets the output writer for the logger.
func (lb *LoggerBuilder) WithOutput(output io.Writer) *LoggerBuilder {
	lb.output = output
	return lb
}

// WithBufferSize sets the buffer size for the logger.
func (lb *LoggerBuilder) WithBufferSize(size int) *LoggerBuilder {
	lb.bufferSize = size
	return lb
}

// WithRateLimit sets the rate limit (messages per second) for the logger.
func (lb *LoggerBuilder) WithRateLimit(limit int) *LoggerBuilder {
	lb.rateLimit = limit
	return lb
}

// WithRateBurst sets the rate burst size for the logger.
func (lb *LoggerBuilder) WithRateBurst(burst int) *LoggerBuilder {
	lb.rateBurst = burst
	return lb
}

// WithGroupWindow sets the event grouping window duration.
// Use 0 for default from environment, negative values to disable grouping.
func (lb *LoggerBuilder) WithGroupWindow(window time.Duration) *LoggerBuilder {
	lb.groupWindow = window
	return lb
}

// WithoutBuffering disables buffering for the logger.
func (lb *LoggerBuilder) WithoutBuffering() *LoggerBuilder {
	lb.bufferingDisabled = true
	return lb
}

// WithoutGrouping disables event grouping for the logger.
func (lb *LoggerBuilder) WithoutGrouping() *LoggerBuilder {
	lb.groupingDisabled = true
	return lb
}

// WithLogLevel sets the log level.
func (lb *LoggerBuilder) WithLogLevel(level zerolog.Level) *LoggerBuilder {
	lb.logLevel = level
	return lb
}

// WithLogLevelString sets the log level from a string.
func (lb *LoggerBuilder) WithLogLevelString(level string) *LoggerBuilder {
	if parsedLevel, err := ParseLevel(level); err == nil {
		lb.logLevel = parsedLevel
	}
	return lb
}

// AsJSON forces JSON output format regardless of environment settings.
func (lb *LoggerBuilder) AsJSON() *LoggerBuilder {
	lb.forceJSON = true
	return lb
}

// Build creates and returns the configured CustomLogger instance.
func (lb *LoggerBuilder) Build() *CustomLogger {
	// Determine output writer
	output := lb.output
	if output == nil {
		if lb.forceJSON {
			output = os.Stderr
		} else {
			output = getFormatBasedOutput()
		}
	}

	// Get configuration values, using environment defaults if not set
	bufferSize, rateLimit, rateBurst := lb.getBufferConfig()
	logLevel := lb.getLogLevel()
	groupWindow := lb.getGroupWindow()

	// Create the buffered rate limited writer
	bufferedWriter := newBufferedRateLimitedWriter(
		output, bufferSize, rateLimit, rateBurst, groupWindow, lb.getBufferingDisabled())

	// Create the zerolog logger
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

// getBufferingDisabled checks if buffering is explicitly disabled or set via environment variable.
func (lb *LoggerBuilder) getBufferingDisabled() bool {
	// If buffering is explicitly disabled, return true
	if lb.bufferingDisabled {
		return true
	}

	// Otherwise, check environment variable
	v := strings.ToLower(os.Getenv(ENV_LOG_DISABLE_BUFFERING))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}

// getBufferConfig returns buffer configuration, using builder values or environment defaults.
func (lb *LoggerBuilder) getBufferConfig() (bufferSize, rateLimit, rateBurst int) {
	if lb.bufferSize > 0 {
		bufferSize = lb.bufferSize
	} else {
		bufferSize = getEnvInt(ENV_LOG_BUFFER_SIZE, DEFAULT_BUFFER_SIZE)
	}

	if lb.rateLimit > 0 {
		rateLimit = lb.rateLimit
	} else {
		rateLimit = getEnvInt(ENV_LOG_RATE_LIMIT, DEFAULT_RATE_LIMIT)
	}

	if lb.rateBurst > 0 {
		rateBurst = lb.rateBurst
	} else {
		rateBurst = getEnvInt(ENV_LOG_RATE_BURST, DEFAULT_RATE_BURST)
	}

	return bufferSize, rateLimit, rateBurst
}

// getLogLevel returns the log level, using builder value or environment default.
func (lb *LoggerBuilder) getLogLevel() zerolog.Level {
	if lb.logLevel != NoLevel {
		return lb.logLevel
	}
	return getLogLevel()
}

// getGroupWindow returns the group window duration, using builder value or environment default.
func (lb *LoggerBuilder) getGroupWindow() time.Duration {
	if lb.groupingDisabled {
		return -1 // Explicitly disabled
	}

	if lb.groupWindow != 0 {
		return lb.groupWindow
	}

	// Use environment default
	groupWindowSec := getEnvInt(ENV_LOG_GROUP_WINDOW, DEFAULT_GROUP_WINDOW)
	return time.Duration(groupWindowSec) * time.Second
}

// getEnvInt parses an integer from environment variable with fallback to default.
func getEnvInt(envVar string, defaultValue int) int {
	if str := os.Getenv(envVar); str != "" {
		if val, err := strconv.Atoi(str); err == nil && val > 0 {
			return val
		}
	}
	return defaultValue
}
