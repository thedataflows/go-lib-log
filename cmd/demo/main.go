package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	log "github.com/thedataflows/go-lib-log"
)

func main() {
	fmt.Println("=== go-lib-log: Enhanced Demo with Event Grouping ===")

	// --- Configuration for Demo ---
	// These are set low to easily demonstrate buffer overflow and drop reporting.
	// Production values would be much higher.
	bufferSizeDemo := 10        // Small buffer
	rateLimitDemo := 5          // Low rate limit (messages per second)
	rateBurstDemo := 3          // Low burst capacity
	dropReportIntervalDemo := 3 // Short interval for drop reports (seconds)
	groupWindowDemo := 2        // Short window for event grouping (seconds)

	_ = os.Setenv("LOG_BUFFER_SIZE", strconv.Itoa(bufferSizeDemo))
	_ = os.Setenv("LOG_RATE_LIMIT", strconv.Itoa(rateLimitDemo))
	_ = os.Setenv("LOG_RATE_BURST", strconv.Itoa(rateBurstDemo))
	_ = os.Setenv("LOG_DROP_REPORT_INTERVAL", strconv.Itoa(dropReportIntervalDemo))
	_ = os.Setenv("LOG_GROUP_WINDOW", strconv.Itoa(groupWindowDemo))
	_ = os.Setenv("LOG_LEVEL", "info") // Set to info to see standard messages
	_ = os.Setenv("LOG_FORMAT", "console")

	// Initialize the global logger with event grouping enabled using builder pattern
	logger := log.NewLogger().
		WithBufferSize(bufferSizeDemo).
		WithRateLimit(rateLimitDemo).
		WithRateBurst(rateBurstDemo).
		WithGroupWindow(time.Duration(groupWindowDemo) * time.Second).
		Build()
	// Crucial: Ensure Close is called to flush buffers and stop background goroutines.
	defer logger.Close()

	fmt.Printf("\n--- Logger Configuration for this Demo ---\n")
	fmt.Printf("- Buffer Size: %d (Default: %d)\n", bufferSizeDemo, log.DEFAULT_BUFFER_SIZE)
	fmt.Printf("- Rate Limit: %d msgs/sec (Default: %d)\n", rateLimitDemo, log.DEFAULT_RATE_LIMIT)
	fmt.Printf("- Rate Burst: %d (Default: %d)\n", rateBurstDemo, log.DEFAULT_RATE_BURST)
	fmt.Printf("- Drop Report Interval: %d sec (Default: %d)\n", dropReportIntervalDemo, log.DEFAULT_DROP_REPORT_INTERVAL)
	fmt.Printf("- Event Group Window: %d sec (Default: %d)\n", groupWindowDemo, log.DEFAULT_GROUP_WINDOW)
	fmt.Printf("- Log Level: %s\n", os.Getenv("LOG_LEVEL"))
	fmt.Printf("- Log Format: %s\n", os.Getenv("LOG_FORMAT"))

	// --- 1. Normal Logging ---
	fmt.Printf("\n--- Section 1: Normal Logging ---\n")
	fmt.Println("Sending a few messages. These should be processed without issues.")
	logger.Info().Str("section", "1-normal").Msg("First normal log message.")
	logger.Warn().Str("section", "1-normal").Int("value", 42).Msg("Second normal log message (a warning).")
	// Brief pause to allow these messages to likely clear the buffer if processing is very fast.
	time.Sleep(100 * time.Millisecond)

	// --- 2. Event Grouping Demonstration ---
	fmt.Printf("\n--- Section 2: Event Grouping Demonstration ---\n")
	fmt.Printf("Event grouping reduces log noise by combining identical messages within a time window.\n")
	fmt.Printf("Sending 10 identical messages rapidly within the %d second window...\n", groupWindowDemo)

	// Send many identical messages rapidly to trigger grouping
	for range 10 {
		logger.Info().Str("section", "2-grouping").Msg("Database connection failed")
		// No delay - send as fast as possible to trigger grouping
	}

	fmt.Printf("Now waiting %d seconds for the grouping window to expire and see results...\n", groupWindowDemo+1)
	time.Sleep(time.Duration(groupWindowDemo+1) * time.Second)

	fmt.Printf("Sending 5 more identical messages to demonstrate another grouping window...\n")
	for range 5 {
		logger.Info().Str("section", "2-grouping").Msg("Redis connection timeout")
	}

	fmt.Printf("Waiting another %d seconds for the second grouping window...\n", groupWindowDemo+1)
	time.Sleep(time.Duration(groupWindowDemo+1) * time.Second)

	fmt.Printf("Event grouping demonstration complete. Notice the 'grouped_count' fields above!\n")

	// --- 3. Showcase Dropped Messages on Buffer Overflow ---
	fmt.Printf("\n--- Section 3: Demonstrating Buffer Overflow and Dropped Messages ---\n")
	numMessagesForOverflow := 30
	fmt.Printf("Attempting to send %d messages rapidly into a buffer of size %d.\n", numMessagesForOverflow, bufferSizeDemo)
	fmt.Printf("With a rate limit of %d/s and burst of %d, many messages are expected to be dropped.\n", rateLimitDemo, rateBurstDemo)
	fmt.Printf("Watch for a 'messages dropped' report which should appear within ~%d seconds after logging starts below.\n", dropReportIntervalDemo)

	for i := range numMessagesForOverflow {
		logger.Info().
			Int("msg_id", i+1).
			Str("section", "3-overflow").
			Msgf("Overflow test message #%d", i+1)
		if i < rateBurstDemo+2 { // Add a tiny delay for the first few messages to make their appearance more distinct
			time.Sleep(20 * time.Millisecond)
		}
		// For messages after the initial small delay, send them as fast as possible.
	}

	fmt.Printf("\nFinished sending %d messages for the overflow test.\n", numMessagesForOverflow)
	fmt.Printf("Waiting %d seconds to observe the drop report and some rate-limited processing...\n", dropReportIntervalDemo+2)
	// This sleep should be longer than dropReportIntervalDemo to reliably see the report.
	time.Sleep(time.Duration(dropReportIntervalDemo+2) * time.Second)

	// --- 4. Logging After Overflow Event ---
	fmt.Printf("\n--- Section 4: Logging After Overflow ---\n")
	fmt.Println("Sending a few more messages. The logger should now be recovering/processing normally.")
	for i := range rateLimitDemo {
		logger.Info().Str("section", "4-recovery").Int("msg_id", i+1).Msgf("Post-overflow message #%d", i+1)
		time.Sleep(250 * time.Millisecond) // Send them slower than rate limit to ensure they get through
	}

	fmt.Printf("\n\n--- Demo Complete ---\n")
	fmt.Println("Review the output above. You should have seen:")
	fmt.Println("1. Initial messages logged successfully.")
	fmt.Println("2. Event grouping in action - repeated messages were grouped with counts.")
	fmt.Println("3. During the overflow test, some messages logged, followed by a 'messages dropped' report.")
	fmt.Println("4. Subsequent messages logged successfully as the system caught up.")
	fmt.Println("\nNotice how event grouping helps reduce log noise from repeated messages!")
	fmt.Println("Check for 'grouped_count' fields in the grouped log entries.")
	fmt.Println("The logger will now be closed by the defer statement, flushing any remaining logs.")
}
