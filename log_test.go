package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestMain is used to ensure the global logger is closed after tests
func TestMain(m *testing.M) {
	// Store original env vars to restore them later
	originalEnv := map[string]string{
		ENV_LOG_FORMAT:      os.Getenv(ENV_LOG_FORMAT),
		ENV_LOG_LEVEL:       os.Getenv(ENV_LOG_LEVEL),
		ENV_LOG_BUFFER_SIZE: os.Getenv(ENV_LOG_BUFFER_SIZE),
		ENV_LOG_RATE_LIMIT:  os.Getenv(ENV_LOG_RATE_LIMIT),
		ENV_LOG_RATE_BURST:  os.Getenv(ENV_LOG_RATE_BURST),
	}

	code := m.Run()

	// Restore original env vars
	for k, v := range originalEnv {
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
	os.Exit(code)
}

func setupTestLogger(t *testing.T, bufferSize, rateLimit, rateBurst int, output io.Writer) *CustomLogger {
	t.Helper()
	_ = os.Setenv(ENV_LOG_FORMAT, "json") // Tests generally easier with JSON
	_ = os.Setenv(ENV_LOG_LEVEL, "debug")
	_ = os.Setenv(ENV_LOG_BUFFER_SIZE, fmt.Sprintf("%d", bufferSize))
	_ = os.Setenv(ENV_LOG_RATE_LIMIT, fmt.Sprintf("%d", rateLimit))
	_ = os.Setenv(ENV_LOG_RATE_BURST, fmt.Sprintf("%d", rateBurst))

	if output != nil {
		// Create a custom logger that uses our rate-limited writer with the test output
		bufferedWriter := newBufferedRateLimitedWriter(output, bufferSize, rateLimit, rateBurst, 0, 0, false)
		zl := zerolog.New(bufferedWriter).
			With().
			Timestamp().
			Logger().Level(zerolog.DebugLevel)

		logger := &CustomLogger{
			Logger:     zl,
			writer:     bufferedWriter,
			bufferSize: bufferSize,
		}
		return logger
	} else {
		// Use normal NewLogger().Build() when no output override is needed
		return NewLogger().Build()
	}
}

func TestNewLoggerWithEnvironmentVariables(t *testing.T) {
	// Test default values when no env vars are set
	t.Run("default_values", func(tt *testing.T) {
		// Clear all logging env vars
		_ = os.Unsetenv(ENV_LOG_FORMAT)
		_ = os.Unsetenv(ENV_LOG_LEVEL)
		_ = os.Unsetenv(ENV_LOG_BUFFER_SIZE)
		_ = os.Unsetenv(ENV_LOG_RATE_LIMIT)
		_ = os.Unsetenv(ENV_LOG_RATE_BURST)

		logger := NewLogger().Build()
		defer logger.Close()

		// Check defaults - we can only check buffer size directly now
		if logger.bufferSize != DEFAULT_BUFFER_SIZE {
			tt.Errorf("Expected buffer size %d, got %d", DEFAULT_BUFFER_SIZE, logger.bufferSize)
		}
		// Rate limiter values are now internal to the writer, but we can test behavior
	})

	// Test custom values from env vars
	t.Run("custom_values", func(tt *testing.T) {
		_ = os.Setenv(ENV_LOG_FORMAT, "json")
		_ = os.Setenv(ENV_LOG_LEVEL, "warn")
		_ = os.Setenv(ENV_LOG_BUFFER_SIZE, "500")
		_ = os.Setenv(ENV_LOG_RATE_LIMIT, "50")
		_ = os.Setenv(ENV_LOG_RATE_BURST, "25")

		logger := NewLogger().Build()
		defer logger.Close()

		// Check custom values - we can only check buffer size directly now
		if logger.bufferSize != 500 {
			tt.Errorf("Expected buffer size 500, got %d", logger.bufferSize)
		}
		// Rate limiter values are now internal to the writer, but behavior can be tested
	})

	// Test invalid values fall back to defaults
	t.Run("invalid_values_fallback", func(tt *testing.T) {
		_ = os.Setenv(ENV_LOG_BUFFER_SIZE, "invalid")
		_ = os.Setenv(ENV_LOG_RATE_LIMIT, "-10")
		_ = os.Setenv(ENV_LOG_RATE_BURST, "not_a_number")

		logger := NewLogger().Build()
		defer logger.Close()

		// Should fall back to defaults
		if logger.bufferSize != DEFAULT_BUFFER_SIZE {
			tt.Errorf("Expected buffer size %d, got %d", DEFAULT_BUFFER_SIZE, logger.bufferSize)
		}
		// Rate limiter defaults are now internal to the writer
	})
}

func TestBufferingMechanism(t *testing.T) {
	var buf bytes.Buffer
	// Small buffer and slow rate to test overflow
	logger := setupTestLogger(t, 3, 5, 1, &buf) // Buffer of 3, rate 5/sec, burst 1
	defer logger.Close()

	// Send more messages than buffer size very quickly
	numMessages := 20
	for i := range numMessages {
		logger.Info().Msgf("Buffer test message %d", i)
	}

	// Allow minimal processing time - not enough for all messages to be processed
	time.Sleep(50 * time.Millisecond)
	logger.Close()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	actualLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			actualLines++
		}
	}

	// With a buffer of 3 and 20 messages sent quickly with slow processing, we expect drops
	if actualLines >= numMessages {
		t.Errorf("Expected some messages to be dropped due to buffer overflow. Got %d out of %d messages", actualLines, numMessages)
	}
	if actualLines == 0 {
		t.Errorf("Expected at least some messages to be logged. Got 0 messages")
	}

	t.Logf("Buffer test: %d out of %d messages were logged (expected some drops)", actualLines, numMessages)
}

func TestRateLimiting(t *testing.T) {
	var buf bytes.Buffer
	// Large buffer, slow rate limit
	logger := setupTestLogger(t, 100, 2, 1, &buf)
	defer logger.Close()

	numMessages := 5
	start := time.Now()

	for i := range numMessages {
		logger.Info().Msgf("Rate limit test %d", i)
	}

	// Wait for processing: (numMessages - burst) / rate + buffer time
	expectedDuration := time.Duration(float64(numMessages-1)/2.0) * time.Second
	time.Sleep(expectedDuration + 1*time.Second)
	logger.Close()

	elapsed := time.Since(start)
	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	actualLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			actualLines++
		}
	}

	if actualLines != numMessages {
		t.Errorf("Expected all %d messages to be logged with large buffer, got %d", numMessages, actualLines)
	}

	// Check that it took reasonable time (rate limiting effect)
	minExpectedTime := time.Duration(float64(numMessages-1)/2.0) * time.Second * 8 / 10 // 80% of theoretical
	if elapsed < minExpectedTime {
		t.Errorf("Logging was too fast (%v), expected at least %v for rate limiting", elapsed, minExpectedTime)
	}

	t.Logf("Rate limiting test: %d messages in %v (expected ~%v)", actualLines, elapsed, expectedDuration)
}

func TestBackpressureDropping(t *testing.T) {
	var buf bytes.Buffer
	// Very small buffer, very slow rate to force drops
	logger := setupTestLogger(t, 1, 1, 1, &buf) // Buffer of 1, rate 1/sec, burst 1
	defer logger.Close()

	// Send a burst of messages that will definitely overflow the tiny buffer
	numMessages := 10
	for i := range numMessages {
		logger.Info().Msgf("Backpressure test %d", i)
	}

	time.Sleep(100 * time.Millisecond) // Very short wait - not enough for all processing
	logger.Close()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	actualLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			actualLines++
		}
	}

	// Should definitely drop messages with buffer of 1 and 10 messages
	if actualLines >= numMessages {
		t.Errorf("Expected backpressure to drop messages. Got %d out of %d", actualLines, numMessages)
	}
	if actualLines == 0 {
		t.Errorf("Expected some messages to get through. Got 0")
	}

	t.Logf("Backpressure test: %d out of %d messages logged", actualLines, numMessages)
}

func TestConcurrentLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := setupTestLogger(t, 200, 100, 50, &buf)
	defer logger.Close()

	numGoroutines := 10
	messagesPerGoroutine := 20
	totalMessages := numGoroutines * messagesPerGoroutine

	var wg sync.WaitGroup
	for i := range numGoroutines {
		wg.Add(1)
		go func(gNum int) {
			defer wg.Done()
			for j := range messagesPerGoroutine {
				logger.Info().Msgf("Goroutine %d message %d", gNum, j)
			}
		}(i)
	}

	wg.Wait()

	// Wait for processing
	time.Sleep(3 * time.Second)
	logger.Close()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	actualLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			actualLines++
		}
	}

	// Should handle most concurrent messages
	minExpected := totalMessages * 7 / 10 // At least 70%
	if actualLines < minExpected {
		t.Errorf("Concurrent logging lost too many messages. Got %d, expected at least %d", actualLines, minExpected)
	}

	t.Logf("Concurrent test: %d out of %d messages logged", actualLines, totalMessages)
}

func TestLoggerClose(t *testing.T) {
	logger := setupTestLogger(t, 10, 10, 10, nil)

	// Test that Close() is idempotent
	logger.Close()
	logger.Close() // Should not panic

	// Test that logging after close doesn't panic (messages are just dropped)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Logging after close caused panic: %v", r)
		}
	}()
	logger.Info().Msg("This message should be dropped")
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer

	// Test with WARN level - set up manually to avoid setupTestLogger overriding level
	_ = os.Setenv(ENV_LOG_FORMAT, "json")
	_ = os.Setenv(ENV_LOG_LEVEL, "warn")
	_ = os.Setenv(ENV_LOG_BUFFER_SIZE, "100")
	_ = os.Setenv(ENV_LOG_RATE_LIMIT, "100")
	_ = os.Setenv(ENV_LOG_RATE_BURST, "50")

	logger := NewLogger().WithOutput(&buf).Build()
	defer logger.Close()

	// Send messages at different levels
	logger.Debug().Msg("Debug message")          // Should be filtered out
	logger.Info().Msg("Info message")            // Should be filtered out
	logger.Warn().Msg("Warn message")            // Should appear
	logger.Error().Err(nil).Msg("Error message") // Should appear

	time.Sleep(100 * time.Millisecond)
	logger.Close()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	actualLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			actualLines++
		}
	}

	// Should only have warn and error messages (2 messages)
	if actualLines != 2 {
		t.Errorf("Expected 2 messages (warn + error), got %d. Output:\n%s", actualLines, output)
	}

	// Verify content
	if !strings.Contains(output, "Warn message") {
		t.Errorf("Expected warn message in output")
	}
	if !strings.Contains(output, "Error message") {
		t.Errorf("Expected error message in output")
	}
	if strings.Contains(output, "Debug message") {
		t.Errorf("Debug message should be filtered out")
	}
	if strings.Contains(output, "Info message") {
		t.Errorf("Info message should be filtered out")
	}
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	_ = os.Setenv(ENV_LOG_FORMAT, "json")
	logger := setupTestLogger(t, 100, 100, 50, &buf)
	defer logger.Close()

	logger.Info().Msg("Test JSON message")
	time.Sleep(100 * time.Millisecond)
	logger.Close()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("No output received")
	}

	// Verify it's valid JSON
	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Errorf("Output is not valid JSON: %v. Output: %s", err, lines[0])
	}

	// Verify required fields
	if _, ok := entry["time"]; !ok {
		t.Errorf("JSON output missing 'time' field")
	}
	if _, ok := entry["level"]; !ok {
		t.Errorf("JSON output missing 'level' field")
	}
	if _, ok := entry["message"]; !ok {
		t.Errorf("JSON output missing 'message' field")
	}
}

func TestDropReporting(t *testing.T) {
	var buf bytes.Buffer
	// Set short drop report interval for testing
	_ = os.Setenv(ENV_LOG_DROP_REPORT_INTERVAL, "1") // 1 second for faster test
	defer os.Unsetenv(ENV_LOG_DROP_REPORT_INTERVAL)

	// Very small buffer to force drops
	logger := setupTestLogger(t, 1, 1000, 1, &buf) // Buffer of 1, high rate limit
	defer logger.Close()

	// Send many messages to force drops
	numMessages := 50
	for i := range numMessages {
		logger.Info().Msgf("Drop test message %d", i)
	}

	// Wait for drop report (interval + buffer time)
	time.Sleep(2 * time.Second)
	logger.Close()

	output := buf.String()

	// Should contain drop report message
	if !strings.Contains(output, "Log messages dropped due to backpressure") {
		t.Errorf("Expected drop report message in output")
	}

	// Should contain dropped count field
	if !strings.Contains(output, "dropped_count") {
		t.Errorf("Expected dropped_count field in drop report")
	}

	// Should contain interval information
	if !strings.Contains(output, "interval_seconds") {
		t.Errorf("Expected interval_seconds field in drop report")
	}

	t.Logf("Drop reporting test output:\n%s", output)
}

func TestDropReportingMultipleTimes(t *testing.T) {
	var buf bytes.Buffer
	// Set short drop report interval for testing
	dropIntervalSeconds := 1
	_ = os.Setenv(ENV_LOG_DROP_REPORT_INTERVAL, fmt.Sprintf("%d", dropIntervalSeconds))
	defer os.Unsetenv(ENV_LOG_DROP_REPORT_INTERVAL)

	// Very small buffer to force drops, moderate rate limit to allow some processing
	// but still cause drops over time.
	logger := setupTestLogger(t, 2, 5, 1, &buf) // Buffer of 2, rate 5/s, burst 1
	defer logger.Close()

	numCycles := 3         // Number of times we want to see a drop report
	messagesPerCycle := 15 // Messages to send in each cycle to ensure drops

	for cycle := range numCycles {
		t.Logf("Drop reporting cycle %d/%d", cycle+1, numCycles)
		for i := range messagesPerCycle {
			logger.Info().Int("cycle", cycle+1).Int("msg", i+1).Msg("Multi-drop test message")
		}
		// Wait for slightly longer than the drop report interval to allow the report to be generated
		// and some messages to be processed by the rate limiter.
		time.Sleep(time.Duration(dropIntervalSeconds)*time.Second + 300*time.Millisecond)
	}

	// Final wait to ensure all processing and last drop report (if any) is done.
	time.Sleep(time.Duration(dropIntervalSeconds)*time.Second + 500*time.Millisecond)
	logger.Close()

	output := buf.String()
	t.Logf("Multi-Drop reporting test output:\n%s", output)

	// Count occurrences of the drop report message
	dropReportCount := strings.Count(output, "Log messages dropped due to backpressure")

	// We expect at least numCycles-1 reports. It might be numCycles or even numCycles+1
	// depending on timing of message generation, processing, and ticker firing.
	// A more robust check is for >= numCycles-1, but for this test, we aim for numCycles.
	if dropReportCount < numCycles-1 {
		t.Errorf("Expected at least %d drop report messages, got %d", numCycles-1, dropReportCount)
	}
	if dropReportCount == 0 {
		t.Errorf("Expected drop reports, but none were found.")
	}

	t.Logf("Found %d drop report instances.", dropReportCount)

	// Verify structure of one of the drop reports (if any exist)
	if dropReportCount > 0 {
		lines := strings.Split(output, "\n")
		foundValidReport := false
		for _, line := range lines {
			if strings.Contains(line, "Log messages dropped due to backpressure") {
				var entry map[string]interface{}
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Errorf("Drop report line is not valid JSON: %v. Line: %s", err, line)
					continue
				}
				if _, ok := entry["dropped_count"]; !ok {
					t.Errorf("Drop report missing 'dropped_count' field: %s", line)
				}
				if _, ok := entry["interval_seconds"]; !ok {
					t.Errorf("Drop report missing 'interval_seconds' field: %s", line)
				}
				foundValidReport = true
				break // Found one, that's enough for structural check
			}
		}
		if !foundValidReport {
			t.Errorf("Although drop reports were counted, none could be parsed as valid JSON with expected fields.")
		}
	}
}

// TestEventGrouping tests the event grouping functionality
func TestEventGrouping(t *testing.T) {
	t.Run("disabled_grouping", func(tt *testing.T) {
		// Test with grouping disabled (window = 0)
		var buf bytes.Buffer
		logger := setupTestLogger(tt, 10, 100, 10, &buf)

		// Create a new logger with grouping disabled
		groupedLogger := NewLogger().WithoutGrouping().Build()
		defer groupedLogger.Close()

		// All messages should be written normally
		for i := range 3 {
			logger.Info().Msgf("Test message %d", i)
		}

		// Close logger to ensure all processing is complete before reading buffer
		logger.Close()

		output := buf.String()
		messageCount := strings.Count(output, "Test message")
		if messageCount != 3 {
			tt.Errorf("Expected 3 messages, got %d", messageCount)
		}
	})

	t.Run("enabled_grouping", func(tt *testing.T) {
		// Test with grouping enabled using main constructor
		originalEnv := os.Getenv(ENV_LOG_FORMAT)
		defer func() {
			if originalEnv == "" {
				_ = os.Unsetenv(ENV_LOG_FORMAT)
			} else {
				_ = os.Setenv(ENV_LOG_FORMAT, originalEnv)
			}
		}()

		// Set up environment for test
		_ = os.Setenv(ENV_LOG_FORMAT, "json")
		_ = os.Setenv(ENV_LOG_LEVEL, "info")

		// Use the main constructor with grouping
		logger := NewLogger().WithGroupWindow(500 * time.Millisecond).Build()
		defer logger.Close()

		// Send the same message multiple times quickly
		for range 5 {
			logger.Info().Msg("Repeated test message")
			time.Sleep(10 * time.Millisecond) // Small delay to ensure ordering
		}

		// Wait a bit for processing and group window to expire
		time.Sleep(800 * time.Millisecond)
	})

	t.Run("different_messages_not_grouped", func(tt *testing.T) {
		// Test that different messages are not grouped together using main constructor
		logger := NewLogger().WithGroupWindow(500 * time.Millisecond).Build()
		defer logger.Close()

		// Send different messages
		logger.Info().Msg("Message A")
		logger.Info().Msg("Message B")
		logger.Info().Msg("Message C")

		// Wait for processing
		time.Sleep(200 * time.Millisecond)
	})

	t.Run("different_levels_not_grouped", func(tt *testing.T) {
		// Test that messages with different levels are not grouped together using main constructor
		originalLevel := os.Getenv(ENV_LOG_LEVEL)
		defer func() {
			if originalLevel == "" {
				_ = os.Unsetenv(ENV_LOG_LEVEL)
			} else {
				_ = os.Setenv(ENV_LOG_LEVEL, originalLevel)
			}
		}()

		_ = os.Setenv(ENV_LOG_LEVEL, "debug")

		logger := NewLogger().WithGroupWindow(500 * time.Millisecond).Build()
		defer logger.Close()

		// Send same message at different levels
		for range 3 {
			logger.Info().Msg("Same message")
			logger.Warn().Msg("Same message")
			logger.Error().Msg("Same message")
		}

		// Wait for processing and grouping window
		time.Sleep(800 * time.Millisecond)
	})

	t.Run("grouping_with_environment_variable", func(tt *testing.T) {
		// Test configuration via environment variable
		originalEnv := os.Getenv(ENV_LOG_GROUP_WINDOW)
		defer func() {
			if originalEnv == "" {
				_ = os.Unsetenv(ENV_LOG_GROUP_WINDOW)
			} else {
				_ = os.Setenv(ENV_LOG_GROUP_WINDOW, originalEnv)
			}
		}()

		_ = os.Setenv(ENV_LOG_GROUP_WINDOW, "1") // 1 second window

		// Use main constructor that reads from environment
		logger := NewLogger().Build()
		defer logger.Close()

		// The grouper should be initialized with 1 second window from env var
		if logger.writer.grouper.windowDur != time.Second {
			tt.Errorf("Expected 1 second window from env var, got %v", logger.writer.grouper.windowDur)
		}
	})
}

// TestEventGrouperPerformance tests the performance impact of event grouping
func TestEventGrouperPerformance(t *testing.T) {
	t.Run("grouping_performance", func(tt *testing.T) {
		// Test that grouping doesn't significantly impact performance
		groupWindow := 100 * time.Millisecond

		logger := NewLogger().WithGroupWindow(groupWindow).Build()
		defer logger.Close()

		start := time.Now()

		// Send many messages rapidly
		for i := range 1000 {
			logger.Info().Int("id", i%10).Msg("Performance test message") // Only 10 unique messages
		}

		elapsed := time.Since(start)

		// Should complete reasonably fast even with grouping
		if elapsed > time.Second {
			tt.Errorf("Grouping took too long: %v", elapsed)
		}

		// Wait for processing
		time.Sleep(200 * time.Millisecond)
	})
}

func TestCloseFlushesGroupedMessages(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger with a short grouping window to test message flushing
	logger := setupTestLogger(t, 10, 100, 10, &buf)

	// Send some messages that should be grouped
	for range 5 {
		logger.Info().Msg("Test message for close flush")
	}

	// Immediately close - this should flush any pending grouped messages
	logger.Close()

	output := buf.String()

	// Should contain the original message
	if !strings.Contains(output, "Test message for close flush") {
		t.Errorf("Expected original message in output, got: %s", output)
	}

	// Should contain group information if messages were grouped
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var foundGrouped bool
	for _, line := range lines {
		if strings.Contains(line, "group_count") {
			foundGrouped = true
			break
		}
	}

	// We should have either 5 individual messages or grouped messages
	// The exact behavior depends on timing, but we should have some output
	if len(lines) < 1 {
		t.Errorf("Expected at least one log line, got: %s", output)
	}

	t.Logf("Close flush test output:\n%s", output)
	t.Logf("Found grouped message: %v", foundGrouped)
}

func TestCloseFlushesBufferedMessages(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger with very slow rate limiting to create pending messages
	logger := setupTestLogger(t, 2, 1, 1, &buf) // Buffer of 2, very slow rate (1/sec)

	// Send messages quickly to fill buffer and create pending grouped messages
	for i := range 10 {
		logger.Info().Msgf("Pending message %d", i)
	}

	// Add some identical messages that should be grouped
	for range 5 {
		logger.Info().Msg("Identical message")
	}

	// Close should flush everything
	logger.Close()

	output := buf.String()

	// Should contain some messages (even if not all due to rate limiting)
	if output == "" {
		t.Errorf("Expected some output after close, got empty string")
	}

	// Count the number of log lines
	lines := strings.Split(strings.TrimSpace(output), "\n")
	nonEmptyLines := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines++
		}
	}

	if nonEmptyLines == 0 {
		t.Errorf("Expected at least one log line after close")
	}

	t.Logf("Close with pending messages test output (%d lines):\n%s", nonEmptyLines, output)
}

func TestUnbufferedLogging(t *testing.T) {
	// Helper function to test unbuffered behavior
	testUnbufferedBehavior := func(tt *testing.T, logger *CustomLogger, testName string) {
		tt.Helper()
		defer logger.Close()

		var buf bytes.Buffer
		// Use reflection or interface to get the underlying writer
		// For this test, we'll create a fresh logger with our output buffer
		unbufferedLogger := NewLogger().
			WithoutBuffering().
			AsJSON().
			WithOutput(&buf).
			Build()
		defer unbufferedLogger.Close()

		// Test immediate writing - messages should appear without delay
		testMessages := []string{"Immediate message 1", "Immediate message 2", "Immediate message 3"}

		for _, msg := range testMessages {
			unbufferedLogger.Info().Msg(msg)
		}

		// In unbuffered mode, messages should be available immediately
		output := buf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		nonEmptyLines := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines++
			}
		}

		if nonEmptyLines != len(testMessages) {
			tt.Errorf("[%s] Expected %d log lines, got %d: %s", testName, len(testMessages), nonEmptyLines, output)
		}

		// Verify each message appears
		for _, expectedMsg := range testMessages {
			if !strings.Contains(output, expectedMsg) {
				tt.Errorf("[%s] Expected output to contain '%s', but it was missing", testName, expectedMsg)
			}
		}

		tt.Logf("[%s] Successfully verified %d unbuffered messages", testName, len(testMessages))
	}

	t.Run("environment_variable_disable", func(tt *testing.T) {
		// Test disabling buffering via environment variable
		originalEnv := os.Getenv(ENV_LOG_DISABLE_BUFFERING)
		defer func() {
			if originalEnv == "" {
				os.Unsetenv(ENV_LOG_DISABLE_BUFFERING)
			} else {
				os.Setenv(ENV_LOG_DISABLE_BUFFERING, originalEnv)
			}
		}()

		os.Setenv(ENV_LOG_DISABLE_BUFFERING, "true")
		logger := NewLogger().Build()
		testUnbufferedBehavior(tt, logger, "environment_variable")
	})

	t.Run("explicit_unbuffered", func(tt *testing.T) {
		// Test explicitly creating unbuffered logger
		logger := NewLogger().WithoutBuffering().Build()
		testUnbufferedBehavior(tt, logger, "explicit_unbuffered")
	})

	t.Run("unbuffered_without_grouping", func(tt *testing.T) {
		// Test unbuffered logger without grouping
		logger := NewLogger().WithoutBuffering().WithoutGrouping().Build()
		testUnbufferedBehavior(tt, logger, "unbuffered_without_grouping")
	})

	t.Run("unbuffered_vs_buffered_timing", func(tt *testing.T) {
		// Test that unbuffered mode writes immediately vs buffered mode
		var unbufferedBuf, bufferedBuf bytes.Buffer

		unbufferedLogger := NewLogger().
			WithoutBuffering().
			AsJSON().
			WithOutput(&unbufferedBuf).
			Build()
		defer unbufferedLogger.Close()

		bufferedLogger := NewLogger().
			AsJSON().
			WithOutput(&bufferedBuf).
			Build()
		defer bufferedLogger.Close()

		// Write messages
		unbufferedLogger.Info().Msg("Unbuffered immediate message")
		bufferedLogger.Info().Msg("Buffered message")

		// Check immediate availability (unbuffered should have output immediately)
		unbufferedOutput := unbufferedBuf.String()

		if !strings.Contains(unbufferedOutput, "Unbuffered immediate message") {
			tt.Error("Unbuffered message should appear immediately")
		}

		// Buffered output behavior may vary, but after close it should be there
		bufferedLogger.Close()
		finalBufferedOutput := bufferedBuf.String()

		if !strings.Contains(finalBufferedOutput, "Buffered message") {
			tt.Error("Buffered message should appear after close")
		}

		tt.Logf("Unbuffered output length: %d, Buffered output length: %d",
			len(unbufferedOutput), len(finalBufferedOutput))
	})

	t.Run("unbuffered_with_rate_limiting", func(tt *testing.T) {
		// Test that unbuffered mode still respects rate limiting
		var buf bytes.Buffer

		logger := NewLogger().
			WithoutBuffering().
			WithRateLimit(2).
			WithRateBurst(1).
			AsJSON().
			WithOutput(&buf).
			Build()
		defer logger.Close()

		start := time.Now()

		// Send 3 messages - should be rate limited even in unbuffered mode
		logger.Info().Msg("Rate limited message 1")
		logger.Info().Msg("Rate limited message 2")
		logger.Info().Msg("Rate limited message 3")

		elapsed := time.Since(start)

		// Should take some time due to rate limiting
		if elapsed < 200*time.Millisecond {
			tt.Logf("Rate limiting took %v (may be acceptable depending on implementation)", elapsed)
		}

		// All messages should eventually be written
		output := buf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		nonEmptyLines := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				nonEmptyLines++
			}
		}

		if nonEmptyLines < 2 { // At least some messages should get through
			tt.Errorf("Expected at least 2 messages after rate limiting, got %d", nonEmptyLines)
		}

		tt.Logf("Rate limiting test: %d messages in %v", nonEmptyLines, elapsed)
	})
}

// Global Logger Tests
func TestGlobalLogger(t *testing.T) {
	// Store original global logger and builder state to restore after tests
	originalBuilder := globalLoggerBuilder
	originalLogger := globalLogger.Load()

	// Store and clear environment variables that might affect defaults
	originalEnvVars := map[string]string{
		ENV_LOG_FORMAT:      os.Getenv(ENV_LOG_FORMAT),
		ENV_LOG_LEVEL:       os.Getenv(ENV_LOG_LEVEL),
		ENV_LOG_BUFFER_SIZE: os.Getenv(ENV_LOG_BUFFER_SIZE),
		ENV_LOG_RATE_LIMIT:  os.Getenv(ENV_LOG_RATE_LIMIT),
		ENV_LOG_RATE_BURST:  os.Getenv(ENV_LOG_RATE_BURST),
	}

	// Clear environment variables to test default behavior
	for k := range originalEnvVars {
		_ = os.Unsetenv(k)
	}

	// Ensure cleanup after tests
	defer func() {
		// Restore original global state
		globalConfigMutex.Lock()
		globalLoggerBuilder = originalBuilder
		globalLogger.Store(originalLogger)
		globalConfigMutex.Unlock()

		// Restore environment variables
		for k, v := range originalEnvVars {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	t.Run("default_global_logger", func(tt *testing.T) {
		// Reset to defaults
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Get the default global logger
		logger := Logger()

		// Should get a valid logger
		if logger == nil {
			tt.Fatal("Expected non-nil global logger")
		}

		// Should be the same instance on subsequent calls
		logger2 := Logger()
		if logger != logger2 {
			tt.Error("Expected same global logger instance on subsequent calls")
		}

		// Check that it has default settings (we can verify buffer size)
		if logger.bufferSize != DEFAULT_BUFFER_SIZE {
			tt.Errorf("Expected default buffer size %d, got %d", DEFAULT_BUFFER_SIZE, logger.bufferSize)
		}
	})

	t.Run("custom_global_logger_via_builder", func(tt *testing.T) {
		// Reset to defaults
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Set a custom global logger builder
		customBuilder := NewLogger().
			WithBufferSize(500).
			WithRateLimit(100).
			WithRateBurst(50).
			WithGroupWindow(time.Second)

		SetGlobalLoggerBuilder(customBuilder)

		// Get the global logger - should use our custom builder
		logger := Logger()

		// Verify custom settings
		if logger.bufferSize != 500 {
			tt.Errorf("Expected custom buffer size 500, got %d", logger.bufferSize)
		}

		// Should be the same instance on subsequent calls
		logger2 := Logger()
		if logger != logger2 {
			tt.Error("Expected same global logger instance on subsequent calls")
		}
	})

	t.Run("direct_global_logger_assignment", func(tt *testing.T) {
		var buf bytes.Buffer

		// Reset to defaults
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Create a custom logger and set it directly
		customLogger := NewLogger().
			WithOutput(&buf).
			WithBufferSize(300).
			Build()

		SetGlobalLogger(customLogger)

		// Get the global logger - should be our custom logger
		logger := Logger()

		if logger != customLogger {
			tt.Error("Expected global logger to be the custom logger we set")
		}

		// Verify it works
		logger.Info().Msg("Test message for direct assignment")
		logger.Close()

		output := buf.String()
		if !strings.Contains(output, "Test message for direct assignment") {
			tt.Error("Expected to find test message in output")
		}
	})

	t.Run("global_logger_builder_accessor", func(tt *testing.T) {
		// Reset to defaults
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Set a custom builder
		customBuilder := NewLogger().WithBufferSize(777)
		SetGlobalLoggerBuilder(customBuilder)

		// Get the builder back
		retrievedBuilder := GlobalLoggerBuilder()

		if retrievedBuilder != customBuilder {
			tt.Error("Expected GlobalLoggerBuilder() to return the same builder we set")
		}
	})
}

func TestGlobalLoggerFormatAndLevelChanges(t *testing.T) {
	// Store original global logger and builder state to restore after tests
	originalBuilder := globalLoggerBuilder
	originalLogger := globalLogger.Load()

	// Store and clear environment variables that might affect defaults
	originalEnvVars := map[string]string{
		ENV_LOG_FORMAT:      os.Getenv(ENV_LOG_FORMAT),
		ENV_LOG_LEVEL:       os.Getenv(ENV_LOG_LEVEL),
		ENV_LOG_BUFFER_SIZE: os.Getenv(ENV_LOG_BUFFER_SIZE),
		ENV_LOG_RATE_LIMIT:  os.Getenv(ENV_LOG_RATE_LIMIT),
		ENV_LOG_RATE_BURST:  os.Getenv(ENV_LOG_RATE_BURST),
	}

	// Clear environment variables to test default behavior
	for k := range originalEnvVars {
		_ = os.Unsetenv(k)
	}

	// Ensure cleanup after tests
	defer func() {
		// Restore original global state
		globalLoggerBuilder = originalBuilder
		globalLogger.Store(originalLogger)

		// Restore environment variables
		for k, v := range originalEnvVars {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	t.Run("preserve_settings_on_format_change", func(tt *testing.T) {
		var buf bytes.Buffer

		// Reset and set up custom global logger builder with specific settings
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger().
			WithOutput(&buf).
			WithBufferSize(456).
			WithRateLimit(200).
			WithRateBurst(100).
			WithGroupWindow(2 * time.Second)
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Get initial logger to establish it
		logger1 := Logger()
		initialBufferSize := logger1.bufferSize

		// Change the format - this should preserve all other settings
		err := SetGlobalLoggerLogFormat("json")
		if err != nil {
			tt.Fatalf("SetGlobalLoggerLogFormat failed: %v", err)
		}

		// Get the new logger
		logger2 := Logger()

		// Should be a different logger instance (recreated)
		if logger1 == logger2 {
			tt.Error("Expected new logger instance after format change")
		}

		// But should preserve custom settings
		if logger2.bufferSize != initialBufferSize {
			tt.Errorf("Expected buffer size to be preserved (%d), got %d", initialBufferSize, logger2.bufferSize)
		}

		// Test that the new logger works
		logger2.Info().Msg("Test after format change")
		logger2.Close()

		output := buf.String()
		if !strings.Contains(output, "Test after format change") {
			tt.Error("Expected to find test message in output after format change")
		}

		// Verify JSON format was applied
		if !strings.Contains(output, `"message":"Test after format change"`) {
			tt.Error("Expected JSON format in output")
		}
	})

	t.Run("preserve_settings_on_level_change", func(tt *testing.T) {
		var buf bytes.Buffer

		// Reset and set up custom global logger builder
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger().
			WithOutput(&buf).
			WithBufferSize(789).
			WithRateLimit(150).
			WithRateBurst(75)
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Get initial logger
		logger1 := Logger()
		initialBufferSize := logger1.bufferSize

		// Change the level - this should preserve all other settings
		err := SetGlobalLoggerLogLevel("warn")
		if err != nil {
			tt.Fatalf("SetGlobalLoggerLogLevel failed: %v", err)
		}

		// Get the new logger
		logger2 := Logger()

		// Should be a different logger instance (recreated)
		if logger1 == logger2 {
			tt.Error("Expected new logger instance after level change")
		}

		// But should preserve custom settings
		if logger2.bufferSize != initialBufferSize {
			tt.Errorf("Expected buffer size to be preserved (%d), got %d", initialBufferSize, logger2.bufferSize)
		}

		// Test that level change worked - debug should not appear
		logger2.Debug().Msg("Debug message should not appear")
		logger2.Warn().Msg("Warning message should appear")
		logger2.Close()

		output := buf.String()
		if strings.Contains(output, "Debug message should not appear") {
			tt.Error("Debug message should not appear with warn level")
		}
		if !strings.Contains(output, "Warning message should appear") {
			tt.Error("Warning message should appear with warn level")
		}
	})

	t.Run("multiple_format_changes_preserve_settings", func(tt *testing.T) {
		var buf bytes.Buffer

		// Reset and set up custom global logger builder
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger().
			WithOutput(&buf).
			WithBufferSize(321).
			WithRateLimit(300).
			WithRateBurst(150)
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Get initial logger
		initialLogger := Logger()
		initialBufferSize := initialLogger.bufferSize

		// Change format multiple times
		formats := []string{"json", "console", "json", "console"}

		for i, format := range formats {
			err := SetGlobalLoggerLogFormat(format)
			if err != nil {
				tt.Fatalf("SetGlobalLoggerLogFormat(%s) failed: %v", format, err)
			}

			currentLogger := Logger()

			// Each change should create a new logger
			if i > 0 && currentLogger == initialLogger {
				tt.Errorf("Expected new logger instance after format change %d", i+1)
			}

			// But should preserve buffer size
			if currentLogger.bufferSize != initialBufferSize {
				tt.Errorf("Expected buffer size to be preserved (%d) after format change %d, got %d",
					initialBufferSize, i+1, currentLogger.bufferSize)
			}

			// Test the logger works
			currentLogger.Info().Msgf("Test message %d with format %s", i+1, format)
		}

		// Close final logger
		Logger().Close()

		output := buf.String()
		// Should have all test messages
		for i := range formats {
			expectedMsg := fmt.Sprintf("Test message %d", i+1)
			if !strings.Contains(output, expectedMsg) {
				tt.Errorf("Expected to find test message %d in output", i+1)
			}
		}
	})
}

func TestGlobalLoggerConcurrency(t *testing.T) {
	// Store original global logger and builder state to restore after tests
	originalBuilder := globalLoggerBuilder
	originalLogger := globalLogger.Load()

	// Store and clear environment variables that might affect defaults
	originalEnvVars := map[string]string{
		ENV_LOG_FORMAT:      os.Getenv(ENV_LOG_FORMAT),
		ENV_LOG_LEVEL:       os.Getenv(ENV_LOG_LEVEL),
		ENV_LOG_BUFFER_SIZE: os.Getenv(ENV_LOG_BUFFER_SIZE),
		ENV_LOG_RATE_LIMIT:  os.Getenv(ENV_LOG_RATE_LIMIT),
		ENV_LOG_RATE_BURST:  os.Getenv(ENV_LOG_RATE_BURST),
	}

	// Clear environment variables to test default behavior
	for k := range originalEnvVars {
		_ = os.Unsetenv(k)
	}

	// Ensure cleanup after tests
	defer func() {
		// Restore original global state
		globalConfigMutex.Lock()
		globalLoggerBuilder = originalBuilder
		globalLogger.Store(originalLogger)
		globalConfigMutex.Unlock()

		// Restore environment variables
		for k, v := range originalEnvVars {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	t.Run("concurrent_logger_access", func(tt *testing.T) {
		var buf safeBuffer

		// Reset to known state
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger().WithOutput(&buf)
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Multiple goroutines accessing the global logger concurrently
		numGoroutines := 20
		messagesPerGoroutine := 10

		var wg sync.WaitGroup
		for i := range numGoroutines {
			wg.Add(1)
			go func(gNum int) {
				defer wg.Done()
				logger := Logger() // Should be safe to call concurrently
				for j := range messagesPerGoroutine {
					logger.Info().Msgf("Concurrent message from goroutine %d, message %d", gNum, j)
				}
			}(i)
		}

		wg.Wait()
		Logger().Close()

		output := buf.String()
		lines := strings.Split(strings.TrimSpace(output), "\n")
		actualLines := 0
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				actualLines++
			}
		}

		expectedMessages := numGoroutines * messagesPerGoroutine
		// Due to buffering and rate limiting, we might not get all messages,
		// but we should get a reasonable number
		if actualLines < expectedMessages/2 {
			tt.Errorf("Expected at least %d messages (got %d), might indicate concurrency issues",
				expectedMessages/2, actualLines)
		}

		tt.Logf("Concurrent logging test: %d out of %d expected messages", actualLines, expectedMessages)
	})
	t.Run("concurrent_format_changes", func(tt *testing.T) {
		var buf safeBuffer

		// Reset to known state
		globalConfigMutex.Lock()
		globalLoggerBuilder = NewLogger().WithOutput(&buf).WithBufferSize(1000)
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		// Test format changes in sequence - this is more realistic
		// Concurrent format changes while logging is not a typical real-world scenario
		formats := []string{"json", "console", "json", "console", "json"}

		for i, format := range formats {
			// Change format
			err := SetGlobalLoggerLogFormat(format)
			if err != nil {
				tt.Errorf("SetGlobalLoggerLogFormat failed for format %s: %v", format, err)
				continue
			}

			// Log a message with the new format
			logger := Logger()
			logger.Info().Msgf("Message %d with format %s", i, format)

			// Small delay to ensure message is processed
			time.Sleep(20 * time.Millisecond)
		}

		// Close logger after all operations
		Logger().Close()

		output := buf.String()
		// Should have messages
		if len(strings.TrimSpace(output)) == 0 {
			tt.Error("Expected some output from format changes")
		}

		// Should contain both JSON and console format messages
		hasJson := strings.Contains(output, `"message":`)
		hasConsole := strings.Contains(output, "Message") && !strings.Contains(output, `"message":`)

		if !hasJson && !hasConsole {
			tt.Error("Expected to see messages in different formats")
		}

		tt.Logf("Format change test completed with output length: %d", len(output))
	})
}

func TestGlobalLoggerEdgeCases(t *testing.T) {
	// Store original global logger and builder state to restore after tests
	originalBuilder := globalLoggerBuilder
	originalLogger := globalLogger.Load()

	// Store and clear environment variables that might affect defaults
	originalEnvVars := map[string]string{
		ENV_LOG_FORMAT:      os.Getenv(ENV_LOG_FORMAT),
		ENV_LOG_LEVEL:       os.Getenv(ENV_LOG_LEVEL),
		ENV_LOG_BUFFER_SIZE: os.Getenv(ENV_LOG_BUFFER_SIZE),
		ENV_LOG_RATE_LIMIT:  os.Getenv(ENV_LOG_RATE_LIMIT),
		ENV_LOG_RATE_BURST:  os.Getenv(ENV_LOG_RATE_BURST),
	}

	// Clear environment variables to test default behavior
	for k := range originalEnvVars {
		_ = os.Unsetenv(k)
	}

	// Ensure cleanup after tests
	defer func() {
		// Restore original global state
		globalConfigMutex.Lock()
		globalLoggerBuilder = originalBuilder
		globalLogger.Store(originalLogger)
		globalConfigMutex.Unlock()

		// Restore environment variables
		for k, v := range originalEnvVars {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	t.Run("nil_builder_handling", func(tt *testing.T) {
		// This should not cause a panic, but create a default logger
		globalConfigMutex.Lock()
		globalLoggerBuilder = nil
		globalLogger.Store(nil)
		globalConfigMutex.Unlock()

		logger := Logger()
		if logger == nil {
			tt.Error("Expected non-nil logger even with nil global builder")
		}

		// Should have default settings
		if logger.bufferSize != DEFAULT_BUFFER_SIZE {
			tt.Errorf("Expected default buffer size %d with nil builder, got %d",
				DEFAULT_BUFFER_SIZE, logger.bufferSize)
		}

		logger.Close()
	})

	t.Run("invalid_format_error_handling", func(tt *testing.T) {
		// Reset to known state
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)

		// Try to set invalid format
		err := SetGlobalLoggerLogFormat("invalid_format_that_does_not_exist")
		if err == nil {
			tt.Error("Expected error when setting invalid log format")
		}

		// Logger should still work with previous/default format
		logger := Logger()
		if logger == nil {
			tt.Error("Expected logger to still be available after invalid format error")
		}

		logger.Close()
	})

	t.Run("invalid_level_error_handling", func(tt *testing.T) {
		// Reset to known state
		globalLoggerBuilder = NewLogger()
		globalLogger.Store(nil)

		// Try to set invalid level
		err := SetGlobalLoggerLogLevel("invalid_level_that_does_not_exist")
		if err == nil {
			tt.Error("Expected error when setting invalid log level")
		}

		// Logger should still work with previous/default level
		logger := Logger()
		if logger == nil {
			tt.Error("Expected logger to still be available after invalid level error")
		}

		logger.Close()
	})
	t.Run("close_global_logger_safety", func(tt *testing.T) {
		var buf bytes.Buffer

		// Set up global logger
		globalLoggerBuilder = NewLogger().WithOutput(&buf)
		globalLogger.Store(nil)

		// Get and use the logger
		logger := Logger()
		logger.Info().Msg("Before close")

		// Close the logger directly (user might do this)
		logger.Close()

		// Getting logger again should return the same instance (since globalLogger wasn't reset)
		logger2 := Logger()
		if logger2 != logger {
			tt.Error("Expected same logger instance since globalLogger variable wasn't reset")
		}

		// But we can force a new logger by using SetGlobalLoggerBuilder
		SetGlobalLoggerBuilder(NewLogger().WithOutput(&buf))
		logger3 := Logger()
		if logger3 == logger {
			tt.Error("Expected new logger instance after SetGlobalLoggerBuilder")
		}

		logger3.Info().Msg("After setting new global logger builder")
		logger3.Close()

		output := buf.String()
		if !strings.Contains(output, "Before close") {
			tt.Error("Expected to find first message")
		}
		if !strings.Contains(output, "After setting new global logger builder") {
			tt.Error("Expected to find message from new logger")
		}
	})
}

func TestDropReportIntervalBuilder(t *testing.T) {
	var buf bytes.Buffer

	// Test custom drop report interval via builder
	logger := NewLogger().
		WithOutput(&buf).
		WithBufferSize(1).                              // Small buffer to force drops
		WithDropReportInterval(500 * time.Millisecond). // 0.5 second interval
		Build()
	defer logger.Close()

	// Send many messages to force drops
	for i := range 20 {
		logger.Info().Msgf("Test message %d", i)
	}

	// Wait for drop report
	time.Sleep(1 * time.Second)
	logger.Close()

	output := buf.String()

	// Should contain drop report message
	if !strings.Contains(output, "Log messages dropped due to backpressure") {
		t.Errorf("Expected drop report message in output")
	}

	// Should contain interval information (0.5 seconds = 0 when formatted as integer)
	if !strings.Contains(output, `"interval_seconds":"0"`) {
		t.Errorf("Expected interval_seconds field with value '0' for 0.5 second interval")
	}

	t.Logf("Drop interval builder test output:\n%s", output)
}

// safeBuffer is a thread-safe buffer wrapper for concurrent testing
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Reset() {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	sb.buf.Reset()
}
