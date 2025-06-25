# go-lib-log

A simple, high-performance, and configurable logging library for Go, built on [zerolog](https://github.com/rs/zerolog).

This library aims to provide a consistent logging interface with advanced features like rate limiting, buffering, and structured logging, suitable for high-throughput applications.

## Overview

`go-lib-log` offers a wrapper around `zerolog` to simplify common logging tasks and provide robust production-ready features. It supports:

* Multiple log levels (Trace, Debug, Info, Warn, Error, Fatal, Panic).
* Structured logging in JSON or human-readable console format.
* High-performance logging with buffering and rate limiting to prevent log flooding.
* Event Grouping: Automatically groups similar log messages within configurable time windows to reduce noise and improve log readability.
* Automatic reporting of dropped log messages due to backpressure.
* Configuration via environment variables for easy setup in different environments.
* Compatibility layer for `go-logging.v1`.

## Features

* **Structured Logging**: Output logs in JSON for easy parsing by log management systems, or in a human-friendly console format for development.
* **Configurable Log Levels**: Set log levels dynamically via environment variables or code.
* **Buffering**: Asynchronously writes logs through a configurable buffer to improve application performance.
* **Rate Limiting**: Controls the rate of log messages written to the output, preventing overload.
* **Event Grouping (Default)**: Groups identical log messages within configurable time windows, showing a single instance with a count. This significantly reduces log noise from repeated errors or warnings. Enabled by default with a 1-second window.
* **Dropped Message Reporting**: If the log buffer is full and messages are dropped, the logger will periodically report the count of dropped messages.
* **Environment Variable Configuration**: Easily configure log level, format, buffer size, rate limits, group windows, and drop report intervals using environment variables.
* **Package-Specific Logging**: Include package name automatically in log entries for better context.
* **Pretty Printing**: Utility functions to pretty-print data structures (e.g., YAML) in logs.
* **`go-logging.v1` Compatibility**: Includes a backend to bridge with applications using `gopkg.in/op/go-logging.v1`.

## Performance

`go-lib-log` is designed for high performance, leveraging `zerolog`\'s efficient JSON marshaling and introducing its own optimizations for buffering and rate limiting.

In benchmarks, `go-lib-log` with its `BufferedRateLimitedWriter` (which includes features like asynchronous processing, rate limiting, and drop reporting) demonstrates significantly better performance compared to the standard library\'s `log` package, especially in high-throughput scenarios.

Here\'s a comparative overview of approximate performance for a simple logging operation (logging a short string and an integer):

| Feature / Logger                                       | `go-lib-log` (Buffered) | Standard Library `log` | `zerolog` (Direct) |
| :----------------------------------------------------- | :---------------------- | :--------------------- | :----------------- |
| **Approx. ns/op**                                      | ~50-55 ns/op            | ~300-500+ ns/op        | ~30-40 ns/op       |
| **Event Grouping Overhead**                            | ~2-7% (when enabled)    | N/A                    | N/A                |
| **Asynchronous Processing**                            | Yes (Buffered)          | No (Synchronous)       | No (Synchronous)   |
| **Structured Logging (JSON)**                          | Yes (via zerolog)       | Manual / Verbose       | Yes (Core)         |
| **Rate Limiting**                                      | Yes                     | No                     | No                 |
| **Buffering**                                          | Yes                     | No                     | No                 |
| **Dropped Message Reporting**                          | Yes                     | No                     | No                 |
| **Zero-Allocation JSON (underlying)**                  | Yes (via zerolog)       | No                     | Yes (Core)         |

**Notes:**

* Performance numbers can vary based on the specific benchmark, hardware, and logging configuration (e.g., output destination, log format).
* Event grouping adds minimal overhead (~2-7%) but provides significant benefits in reducing log noise and storage costs.
* The Standard Library `log` figures are for attempts to achieve similar structured-like output; basic `log.Println` would be faster but unstructured.
* `zerolog` (Direct) serves as a baseline for the underlying logging engine without the additional features of `go-lib-log`\'s writer.

The key takeaway is that `go-lib-log` aims to provide advanced logging features (like buffering and rate limiting) with a minimal performance overhead compared to raw `zerolog`, and a substantial improvement over less optimized or more feature-heavy logging solutions, including the standard library logger when structured output and asynchronous behavior are desired.

The performance benefits are primarily due to:

* **Asynchronous Processing**: Log messages are written to an in-memory buffer and processed by a separate goroutine, minimizing blocking in the application\'s hot paths.
* **Efficient Serialization**: `zerolog`\'s focus on zero-allocation JSON marshaling.
* **Reduced Lock Contention**: Recent optimizations have further reduced internal lock contention, particularly in the `BufferedRateLimitedWriter`.

When comparing, ensure that the standard library logger is configured to produce a similar output format (e.g., JSON-like or structured text) and consider its synchronous nature by default, which can be a bottleneck in concurrent applications.

## Installation

```bash
go get github.com/thedataflows/go-lib-log
```

## Usage

### Basic Logging

```go
package main

import (
 "errors"
 "os"

 "github.com/thedataflows/go-lib-log/log" // Adjust import path
)

func main() {
 // The global logger is initialized automatically with event grouping enabled.
 // Ensure to close it on application shutdown to flush buffers.
 defer log.Close()

 log.Info("main", "Application started")
 log.Debug("main", "This is a debug message.")
 log.Warnf("main", "Something to be aware of: %s", "potential issue")
 err := errors.New("something went wrong")
 log.Error("main", err, "An error occurred")

 // Example of logging with package context
 myFunction()

 // Demonstrate event grouping with repeated messages
 for i := 0; i < 5; i++ {
  log.Error("main", errors.New("connection failed"), "Database connection error")
  // These identical messages will be grouped automatically
 }

 // To see Fatal or Panic in action (uncomment to test):
 // log.Fatal("main", err, "A fatal error occurred, exiting.")
 // log.Panic("main", err, "A panic occurred.")
}

func myFunction() {
 log.Info("myFunction", "Executing myFunction")
 data := map[string]interface{}{"key": "value", "number": 123}
 log.Debugf("myFunction", "Processing data: %s", log.PrettyYamlString(data))
}

```

### Configuration

The logger can be configured using the following environment variables:

* `LOG_LEVEL`: Sets the minimum log level.
  * Supported values: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`.
  * Default: `info`.
* `LOG_FORMAT`: Sets the log output format.
  * Supported values: `console`, `json`.
  * Default: `console`.
* `LOG_BUFFER_SIZE`: Sets the size of the internal log message buffer.
  * Default: `100000`.
* `LOG_RATE_LIMIT`: Sets the maximum number of log messages per second.
  * Default: `50000`.
* `LOG_RATE_BURST`: Sets the burst size for the rate limiter.
  * Default: `10000`.
* `LOG_GROUP_WINDOW`: Sets the time window (in seconds) for grouping similar log events.
  * Default: `1` (1 second, > 0 enables event grouping).
  * Set to `0` to disable event grouping.
* `ENV_LOG_DROP_REPORT_INTERVAL`: Sets the interval (in seconds) for reporting dropped log messages.
  * Default: `10`.

## Event Grouping

Event grouping is a powerful feature that reduces log noise by grouping identical messages within a configurable time window. **This feature is enabled by default** with a 1-second grouping window. When multiple identical log messages are received within the window, only the first one is logged immediately, and subsequent identical messages increment a counter. When the window expires, a final grouped message is emitted showing the total count.

### How It Works

1. **Message Hashing**: Each log message is hashed based on its content and level
2. **Time Windows**: Messages are grouped within configurable time windows (default: 1 second)
3. **Atomic Operations**: Uses lock-free atomic operations for high performance
4. **Zero Allocation**: Maintains zero-copy performance where possible

### Grouped Message Format

When messages are grouped, the final log entry includes additional fields:

```json
{
  "time": "2025-06-13T10:30:00Z",
  "level": "error",
  "message": "Database connection failed",
  "group_count": 15,
  "group_window": "1s",
  "group_first": "2025-06-13T10:29:59Z"
}
```

### Using Event Grouping

```go
// Default logger with event grouping enabled (1 second window)
logger := log.NewLogger()
defer logger.Close()

// These messages will be grouped if sent within 1 second
logger.Error().Msg("Database connection failed")
logger.Error().Msg("Database connection failed") // Will be grouped
logger.Error().Msg("Database connection failed") // Will be grouped

// Create logger with custom grouping window
logger := log.NewLoggerWithGrouping(2 * time.Second)
defer logger.Close()

// Create logger with grouping explicitly disabled
logger := log.NewLoggerWithoutGrouping()
defer logger.Close()

// Different messages won't be grouped
logger.Error().Msg("Redis connection failed") // Separate message

// Environment variable configuration
os.Setenv("LOG_GROUP_WINDOW", "2") // 2 second window
logger := log.NewLogger() // Will use environment configuration

// Disable grouping via environment variable
os.Setenv("LOG_GROUP_WINDOW", "0") // Disables grouping
logger := log.NewLogger()
```

### Programmatic Configuration

You can also configure the logger programmatically:

```go
package main

import (
 "github.com/thedataflows/go-lib-log/log" // Adjust import path
 "github.com/rs/zerolog"
 "os"
 "time"
)

func main() {
 defer log.Close()

 // Set log level
 err := log.SetLoggerLogLevel("debug")
 if err != nil {
  log.Error("main", err, "Failed to set log level")
 }

 // Set log format (this re-initializes parts of the logger)
 err = log.SetLoggerLogFormat("json")
 if err != nil {
  log.Error("main", err, "Failed to set log format")
 }

 log.Info("main", "Logger configured programmatically.")

 // For more advanced custom logger setup:
 // customOutput := zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
 // logger := log.NewCustomLogger(customOutput) // This creates a new instance, not modifying the global one directly
 // logger.Info().Msg("This is from a custom logger instance")
 // defer logger.Close() // Important if you create separate instances
}
```

### Logger Constructors

The library provides several constructor functions to suit different needs:

```go
// Default logger with event grouping enabled (1 second window)
logger := log.NewLogger()

// Logger with custom event grouping window
logger := log.NewLoggerWithGrouping(5 * time.Second)

// Logger with event grouping explicitly disabled
logger := log.NewLoggerWithoutGrouping()

// JSON-only logger (always outputs JSON format)
logger := log.NewJsonLogger()
```

## Important: Close() Method

**Always call `Close()` on your logger instances** to ensure proper cleanup and message flushing:

```go
logger := log.NewLogger()
defer logger.Close() // Critical for proper cleanup
```

The `Close()` method performs several important operations:

1. **Flushes Pending Grouped Messages**: Any messages waiting in the event grouper are immediately flushed
2. **Processes Buffered Messages**: All messages in the internal buffer are processed through rate limiting
3. **Stops Background Goroutines**: Cleanly shuts down the processor and drop reporter goroutines
4. **Prevents Message Loss**: Ensures no messages are lost during application shutdown

### Close() Behavior

* **Thread-Safe**: Can be called multiple times safely (subsequent calls are no-ops)
* **Blocking**: Will wait for all pending messages to be processed
* **Rate-Limited**: Pending grouped messages go through normal rate limiting during flush
* **Atomic**: Uses atomic operations to prevent race conditions during shutdown

### Example with Proper Cleanup

```go
func main() {
    logger := log.NewLogger()
    defer logger.Close() // Ensures all messages are flushed

    // Send many identical messages - these will be grouped
    for i := 0; i < 100; i++ {
        logger.Error().Msg("Database connection failed")
    }

    // Close() will flush the grouped message before exiting
    // Without Close(), the grouped message might be lost
}
```

**Note**: The global logger used by package-level functions (`log.Info()`, `log.Error()`, etc.) should also be closed with `log.Close()` for proper cleanup.

### Using the Global Logger vs Instance Logger

You can use the library in two ways:

```go
// Option 1: Global logger (backwards compatible)
import log "github.com/thedataflows/go-lib-log"

func main() {
    defer log.Close() // Close global logger
    log.Info("pkg", "Using global logger")
}

// Option 2: Instance logger (recommended for new code)
import log "github.com/thedataflows/go-lib-log"

func main() {
    logger := log.NewLogger()
    defer logger.Close()
    logger.Info().Msg("Using instance logger")
}
```

## Migration Note

**For existing users**: If you prefer the previous behavior without event grouping, you can:

1. Use `log.NewLoggerWithoutGrouping()` instead of `log.NewLogger()`
2. Set the environment variable `LOG_GROUP_WINDOW=0` to disable grouping
3. Use `log.NewLoggerWithGrouping(0)` to explicitly disable grouping

This change is backward compatible - your existing code will continue to work, but will now benefit from automatic event grouping.

## License

[MIT License](LICENSE)
