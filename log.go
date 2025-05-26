package log

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

type LogFormat int

const (
	ENV_LOG_LEVEL  = "LOG_LEVEL"
	ENV_LOG_FORMAT = "LOG_FORMAT"
	KEY_PKG        = "pkg"

	LOG_FORMAT_CONSOLE LogFormat = iota
	LOG_FORMAT_JSON
)

var (
	Logger     = NewLogger()
	ParseLevel = zerolog.ParseLevel

	DebugLevel = zerolog.DebugLevel
	InfoLevel  = zerolog.InfoLevel
	WarnLevel  = zerolog.WarnLevel
	ErrorLevel = zerolog.ErrorLevel
	FatalLevel = zerolog.FatalLevel
	PanicLevel = zerolog.PanicLevel
	NoLevel    = zerolog.NoLevel
	Disabled   = zerolog.Disabled
	TraceLevel = zerolog.TraceLevel

	logFormatToString = map[LogFormat]string{
		LOG_FORMAT_CONSOLE: "console",
		LOG_FORMAT_JSON:    "json",
	}

	stringToLogFormat = map[string]LogFormat{
		"console": LOG_FORMAT_CONSOLE,
		"json":    LOG_FORMAT_JSON,
	}
)

type CustomLogger struct {
	zerolog.Logger
}

func (l *CustomLogger) Tracef(format string, args ...any) {
	l.Trace().Msgf(format, args...)
}
func (l *CustomLogger) Debugf(format string, args ...any) {
	l.Debug().Msgf(format, args...)
}

func (l *CustomLogger) Errorf(err error, format string, args ...any) {
	l.Error().Err(err).Msgf(format, args...)
}

func (l *CustomLogger) Fatalf(err error, format string, args ...any) {
	l.Fatal().Err(err).Msgf(format, args...)
}

func (l *CustomLogger) Warnf(format string, args ...any) {
	l.Warn().Msgf(format, args...)
}

func (l *CustomLogger) Infof(format string, args ...any) {
	l.Info().Msgf(format, args...)
}

func (l *CustomLogger) SetLogger(logger zerolog.Logger) {
	l.Logger = logger
}

func (f LogFormat) String() string {
	if s, ok := logFormatToString[f]; ok {
		return s
	}
	return "unknown"
}

// Get all available log format values from the map
func AvailableLogFormats() []string {
	formats := make([]string, 0, len(logFormatToString))
	for _, value := range logFormatToString {
		formats = append(formats, value)
	}
	return formats
}

func ParseLogFormat(logFormat string) (LogFormat, error) {
	if logFormat == "" {
		return LOG_FORMAT_CONSOLE, nil
	}
	if format, ok := stringToLogFormat[logFormat]; ok {
		return format, nil
	}

	return LOG_FORMAT_CONSOLE, fmt.Errorf("invalid log format '%s'. Provide one of: %s", logFormat, AvailableLogFormats())
}

func SetLoggerLogFormat(logFormat string) error {
	format, err := ParseLogFormat(logFormat)
	if err != nil {
		return err
	}

	switch format {
	case LOG_FORMAT_CONSOLE:
		Logger.SetLogger(NewLogger().Logger)
	case LOG_FORMAT_JSON:
		Logger.SetLogger(NewJsonLogger().Logger)
	}
	return nil
}

func NewLogger() *CustomLogger {
	var output io.Writer

	logFormat, err := ParseLogFormat(os.Getenv(ENV_LOG_FORMAT))
	if err != nil {
		panic(err)
	}
	output = os.Stderr
	if logFormat == LOG_FORMAT_CONSOLE {
		output = zerolog.ConsoleWriter{
			Out:        PreferredWriter(),
			TimeFormat: time.RFC3339,
		}
	}

	logLevel, err := ParseLevel(os.Getenv(ENV_LOG_LEVEL))
	if err != nil {
		panic(err)
	}
	if logLevel == NoLevel {
		logLevel = InfoLevel
	}

	l := zerolog.New(output).
		With().
		Timestamp().
		Logger()
	return &CustomLogger{
		l.Level(logLevel),
	}
}

func NewJsonLogger() *CustomLogger {
	return &CustomLogger{
		zerolog.New(os.Stderr).
			With().
			Timestamp().
			Logger().
			Level(zerolog.InfoLevel),
	}
}

func PreferredWriter() io.Writer {
	return io.MultiWriter(
		new(bytes.Buffer),
		os.Stderr,
	)
}

func SetLoggerLogLevel(level string) error {
	if len(level) == 0 {
		level = InfoLevel.String()
	}
	parsedLevel, err := ParseLevel(level)
	if err != nil {
		return err
	}
	Logger.SetLogger(Logger.Level(parsedLevel))
	return nil
}
