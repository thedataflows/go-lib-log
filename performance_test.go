package log

import (
	"bytes"
	stdlog "log"
	"os"
	"sync"
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

	// Create custom logger with buffer output using builder pattern
	logger := NewLoggerBuilder().WithOutput(&buf).Build()
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
		logger := NewLoggerBuilder().WithGroupWindow(groupWindow).Build()
		defer logger.Close()

		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger.Info().Msg("Benchmark grouping")
			}
		})
	})

	b.Run("without_grouping", func(bb *testing.B) {
		logger := NewLoggerBuilder().WithGroupWindow(-1).Build() // No grouping
		defer logger.Close()

		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger.Info().Msg("Benchmark without grouping")
			}
		})
	})
}

// BenchmarkGlobalLoggerAccess benchmarks the performance of accessing the global logger
// This demonstrates the performance advantage of using atomic.Pointer vs mutex for read-heavy workloads
func BenchmarkGlobalLoggerAccess(b *testing.B) {
	b.Run("current_atomic_implementation", func(bb *testing.B) {
		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger := Logger()
				_ = logger // Prevent optimization
			}
		})
	})

	// Comparison with a theoretical mutex-based approach
	b.Run("mutex_based_comparison", func(bb *testing.B) {
		mutexLogger := &mutexBasedGlobalLogger{
			logger: NewLoggerBuilder().Build(),
		}
		defer mutexLogger.logger.Close()

		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger := mutexLogger.Get()
				_ = logger // Prevent optimization
			}
		})
	})

	// Direct atomic access without function call overhead
	b.Run("direct_atomic_load", func(bb *testing.B) {
		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger := globalLogger.Load()
				_ = logger // Prevent optimization
			}
		})
	})
}

// mutexBasedGlobalLogger simulates the old mutex-based approach for comparison
type mutexBasedGlobalLogger struct {
	mu     sync.RWMutex
	logger *CustomLogger
}

func (m *mutexBasedGlobalLogger) Get() *CustomLogger {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.logger
}

// BenchmarkGlobalLoggerWithRealLogging benchmarks the complete logging pipeline
// using the global logger to show real-world performance
func BenchmarkGlobalLoggerWithRealLogging(b *testing.B) {
	// Setup a buffer to capture output for consistent benchmarking
	var buf bytes.Buffer

	// Create a custom logger with buffer output for benchmarking
	originalLogger := globalLogger.Load()
	testLogger := NewLoggerBuilder().WithOutput(&buf).Build()
	globalLogger.Store(testLogger)

	// Restore original logger after benchmark
	defer func() {
		testLogger.Close()
		globalLogger.Store(originalLogger)
	}()

	b.Run("global_logger_info_messages", func(bb *testing.B) {
		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			counter := 0
			for pb.Next() {
				Logger().Info().
					Str("component", "benchmark").
					Int("counter", counter).
					Msg("Performance test message")
				counter++
			}
		})
	})

	b.Run("global_logger_with_fields", func(bb *testing.B) {
		bb.ResetTimer()
		bb.RunParallel(func(pb *testing.PB) {
			msgCounter := 0
			for pb.Next() {
				Logger().Info().
					Str("component", "benchmark").
					Str("operation", "field_logging").
					Int("counter", msgCounter).
					Time("timestamp", time.Now()).
					Bool("is_benchmark", true).
					Msg("Complex benchmark message")
				msgCounter++
			}
		})
	})
}

// BenchmarkGlobalLoggerConcurrentAccess benchmarks concurrent access patterns
// that are common in real applications
func BenchmarkGlobalLoggerConcurrentAccess(b *testing.B) {
	// Test concurrent read access (most common pattern)
	b.Run("concurrent_reads", func(bb *testing.B) {
		bb.ResetTimer()
		bb.SetParallelism(10) // Simulate 10 concurrent goroutines
		bb.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				logger := Logger()
				// Simulate some work with the logger
				_ = logger.Info()
			}
		})
	})

	// Test mixed read/write access (less common but important)
	b.Run("mixed_read_write", func(bb *testing.B) {
		bb.ResetTimer()
		bb.SetParallelism(10)
		bb.RunParallel(func(pb *testing.PB) {
			readCount := 0
			for pb.Next() {
				readCount++
				if readCount%1000 == 0 {
					// Occasional write operation (1 in 1000)
					originalLogger := Logger()
					newLogger := NewLoggerBuilder().Build()
					globalLogger.Store(newLogger)
					// Restore original (in real usage, you wouldn't do this)
					globalLogger.Store(originalLogger)
					newLogger.Close()
				} else {
					// Normal read operation
					logger := Logger()
					_ = logger
				}
			}
		})
	})
}
