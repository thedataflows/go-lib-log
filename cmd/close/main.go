package main

import (
	"fmt"

	log "github.com/thedataflows/go-lib-log"
)

func main() {
	logger := log.NewLoggerBuilder().Build()
	defer logger.Close() // Ensures all messages are flushed

	// Send many identical messages - these will be grouped
	const MESSAGES = 100
	fmt.Printf("Sending %d identical messages to demonstrate event grouping...\n", MESSAGES)
	for range MESSAGES {
		logger.Error().Msg("Database connection failed")
	}

	// Close() will flush the grouped message before exiting
	// Without Close(), the grouped message might be lost
}
