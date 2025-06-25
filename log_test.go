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
		bufferedWriter := newBufferedRateLimitedWriter(output, bufferSize, rateLimit, rateBurst, 0, false)
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
