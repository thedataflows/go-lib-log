package log

import "fmt"

// LogFormat defines the available log output formats.
type LogFormat int

const (
	// LOG_FORMAT_CONSOLE represents a human-readable console log format.
	LOG_FORMAT_CONSOLE LogFormat = iota
	// LOG_FORMAT_JSON represents a machine-readable JSON log format.
	LOG_FORMAT_JSON
	_logFormatCount // sentinel for count
)

// AllLogFormats is a list of all supported LogFormat values.
var AllLogFormats = [_logFormatCount]LogFormat{
	LOG_FORMAT_CONSOLE,
	LOG_FORMAT_JSON,
}

// String implements fmt.Stringer interface
func (f LogFormat) String() string {
	switch f {
	case LOG_FORMAT_CONSOLE:
		return "console"
	case LOG_FORMAT_JSON:
		return "json"
	default:
		return "unknown"
	}
}

// ParseLogFormat converts a string representation of a log format into a LogFormat type.
// It returns LOG_FORMAT_CONSOLE if the string is empty or "console",
// LOG_FORMAT_JSON if the string is "json".
// It returns an error for any other invalid input.
func ParseLogFormat(s string) (LogFormat, error) {
	switch s {
	case "", "console": // empty defaults to console
		return LOG_FORMAT_CONSOLE, nil
	case "json":
		return LOG_FORMAT_JSON, nil
	default:
		return LOG_FORMAT_CONSOLE, fmt.Errorf("invalid log format '%s'. Supported formats: %v", s, AllLogFormatsStrings())
	}
}

// AllLogFormatsStrings returns a slice of strings representing all supported log formats.
func AllLogFormatsStrings() []string {
	formats := make([]string, len(AllLogFormats))
	for i, format := range AllLogFormats {
		formats[i] = format.String()
	}
	return formats
}

// IsValidLogFormat checks if a given string is a valid log format.
func IsValidLogFormat(s string) bool {
	for _, format := range AllLogFormats {
		if format.String() == s {
			return true
		}
	}
	return false
}

// SetLoggerLogFormat sets the log format for the global Logger.
// It parses the provided logFormat string and updates the Logger's
// underlying writer to either a console writer or a JSON writer.
// Returns an error if the logFormat string is invalid.
func SetLoggerLogFormat(logFormat string) error {
	format, err := ParseLogFormat(logFormat)
	if err != nil {
		return err
	}

	// Close the current logger's writer to clean up resources
	Logger.Close()

	// Create a new logger with the desired format and replace the global Logger's components
	var newLogger *CustomLogger
	switch format {
	case LOG_FORMAT_JSON:
		newLogger = NewLogger().AsJSON().Build()
	default:
		newLogger = NewLogger().Build()
	}

	// Replace the Logger's components with the new logger's components
	Logger.Logger = newLogger.Logger
	Logger.writer = newLogger.writer
	Logger.bufferSize = newLogger.bufferSize

	return nil
}
