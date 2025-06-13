package log

import (
	"bytes"
	stdlog "log"
	"os"
	"testing"
	"time"
)

func BenchmarkZeroCopyLogger(b *testing.B) {
	// Create a buffer to capture output without I/O overhead
	var buf bytes.Buffer

	// Use the public API to create a logger with custom output
	// Temporarily override env vars for consistent benchmark results
	originalFormat := os.Getenv(ENV_LOG_FORMAT)
	originalLevel := os.Getenv(ENV_LOG_LEVEL)
	originalBuffer := os.Getenv(ENV_LOG_BUFFER_SIZE)
	originalRate := os.Getenv(ENV_LOG_RATE_LIMIT)
	originalBurst := os.Getenv(ENV_LOG_RATE_BURST)

	_ = os.Setenv(ENV_LOG_FORMAT, "json")
	_ = os.Setenv(ENV_LOG_LEVEL, "info")
	_ = os.Setenv(ENV_LOG_BUFFER_SIZE, "10000") // Large buffer for benchmarking
	_ = os.Setenv(ENV_LOG_RATE_LIMIT, "100000") // High rate limit for benchmarking
	_ = os.Setenv(ENV_LOG_RATE_BURST, "10000")  // High burst for benchmarking

	// Create custom logger with buffer output
	logger := newCustomLogger(&buf)
	defer logger.Close()

	// Restore environment variables
	defer func() {
		if originalFormat == "" {
			_ = os.Unsetenv(ENV_LOG_FORMAT)
		} else {
			_ = os.Setenv(ENV_LOG_FORMAT, originalFormat)
		}
		if originalLevel == "" {
			_ = os.Unsetenv(ENV_LOG_LEVEL)
		} else {
			_ = os.Setenv(ENV_LOG_LEVEL, originalLevel)
		}
		if originalBuffer == "" {
			_ = os.Unsetenv(ENV_LOG_BUFFER_SIZE)
		} else {
			_ = os.Setenv(ENV_LOG_BUFFER_SIZE, originalBuffer)
		}
		if originalRate == "" {
			_ = os.Unsetenv(ENV_LOG_RATE_LIMIT)
		} else {
			_ = os.Setenv(ENV_LOG_RATE_LIMIT, originalRate)
		}
		if originalBurst == "" {
			_ = os.Unsetenv(ENV_LOG_RATE_BURST)
		} else {
			_ = os.Setenv(ENV_LOG_RATE_BURST, originalBurst)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Info().
				Str("component", "benchmark").
				Int("iteration", b.N).
				Time("timestamp", time.Now()).
				Msg("Performance test message")
		}
	})
}

func BenchmarkStandardLogger(b *testing.B) {
	// Compare against standard log package for reference
	var buf bytes.Buffer
	logger := stdlog.New(&buf, "", stdlog.LstdFlags)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.Printf("Performance test message with component=%s iteration=%d timestamp=%v",
				"benchmark", b.N, time.Now())
		}
	})
}

// BenchmarkEventGrouping benchmarks the event grouping functionality
func BenchmarkEventGrouping(b *testing.B) {
	_ = os.Setenv(ENV_LOG_FORMAT, "json")

	b.Run("with_grouping", func(bb *testing.B) {
		groupWindow := 100 * time.Millisecond
		logger := NewLoggerWithGrouping(groupWindow)
		defer logger.Close()

		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger.Info().Msg("Benchmark grouping")
			}
		})
	})

	b.Run("without_grouping", func(bb *testing.B) {
		logger := NewLoggerWithGrouping(0) // No grouping
		defer logger.Close()

		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger.Info().Msg("Benchmark without grouping")
			}
		})
	})
}
