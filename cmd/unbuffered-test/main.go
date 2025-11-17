package main

import (
	"os"
	"time"

	log "github.com/thedataflows/go-lib-log"
)

func main() {
	// Example 1: Use environment variable to disable buffering
	_ = os.Setenv(log.ENV_LOG_BUFFERING_DISABLED, "true")

	logger1 := log.NewLoggerBuilder().Build()
	logger1.Info().Msg("This message uses unbuffered logging (via env var)")

	// Example 2: Explicitly create unbuffered logger using builder pattern
	logger2 := log.NewLoggerBuilder().WithoutBuffering().Build()
	logger2.Info().Msg("This message uses unbuffered logging (explicit)")

	// Example 3: Unbuffered with no grouping using builder pattern
	logger3 := log.NewLoggerBuilder().WithoutBuffering().WithGroupWindow(-1).Build()
	logger3.Info().Msg("This message uses unbuffered logging with no grouping")

	// Example 4: Demonstrate immediate writing (no buffering delay)
	logger4 := log.NewLoggerBuilder().WithoutBuffering().Build()
	logger4.Info().Msg("Message 1 - should appear immediately")
	logger4.Info().Msg("Message 2 - should appear immediately")
	logger4.Info().Msg("Message 3 - should appear immediately")

	// No need to wait or flush - messages are written immediately

	// Clean up
	logger1.Close()
	logger2.Close()
	logger3.Close()
	logger4.Close()

	// Demonstrate the difference with buffered logging
	_ = os.Setenv(log.ENV_LOG_BUFFERING_DISABLED, "false")
	logger5 := log.NewLoggerBuilder().Build()
	logger5.Info().Msg("This message uses buffered logging")

	// For buffered logging, you might want to close to ensure all messages are flushed
	logger5.Close()

	// Give a moment for any background processes to complete
	time.Sleep(100 * time.Millisecond)
}
