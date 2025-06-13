# go-lib-log

A simple, high-performance, and configurable logging library for Go, built on [zerolog](https://github.com/rs/zerolog).

This library aims to provide a consistent logging interface with advanced features like rate limiting, buffering, and structured logging, suitable for high-throughput applications.

## Overview

`go-lib-log` offers a wrapper around `zerolog` to simplify common logging tasks and provide robust production-ready features. It supports:

* Multiple log levels (Trace, Debug, Info, Warn, Error, Fatal, Panic).
* Structured logging in JSON or human-readable console format.
* High-performance logging with buffering and rate limiting to prevent log flooding.
* Automatic reporting of dropped log messages due to backpressure.
* Configuration via environment variables for easy setup in different environments.
* Compatibility layer for `go-logging.v1`.

## Features

* **Structured Logging**: Output logs in JSON for easy parsing by log management systems, or in a human-friendly console format for development.
* **Configurable Log Levels**: Set log levels dynamically via environment variables or code.
* **Buffering**: Asynchronously writes logs through a configurable buffer to improve application performance.
* **Rate Limiting**: Controls the rate of log messages written to the output, preventing overload.
* **Dropped Message Reporting**: If the log buffer is full and messages are dropped, the logger will periodically report the count of dropped messages.
* **Environment Variable Configuration**: Easily configure log level, format, buffer size, rate limits, and drop report intervals using environment variables.
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
| **Asynchronous Processing**                            | Yes (Buffered)          | No (Synchronous)       | No (Synchronous)   |
| **Structured Logging (JSON)**                          | Yes (via zerolog)       | Manual / Verbose       | Yes (Core)         |
| **Rate Limiting**                                      | Yes                     | No                     | No                 |
| **Buffering**                                          | Yes                     | No                     | No                 |
| **Dropped Message Reporting**                          | Yes                     | No                     | No                 |
| **Zero-Allocation JSON (underlying)**                  | Yes (via zerolog)       | No                     | Yes (Core)         |

**Notes:**

* Performance numbers can vary based on the specific benchmark, hardware, and logging configuration (e.g., output destination, log format).
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
 // The global logger is initialized automatically.
 // Ensure to close it on application shutdown to flush buffers.
 defer log.Close()

 log.Info("main", "Application started")
 log.Debug("main", "This is a debug message.")
 log.Warnf("main", "Something to be aware of: %s", "potential issue")
 err := errors.New("something went wrong")
 log.Error("main", err, "An error occurred")

 // Example of logging with package context
 myFunction()

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
* `ENV_LOG_DROP_REPORT_INTERVAL`: Sets the interval (in seconds) for reporting dropped log messages.
  * Default: `10`.

Example:

```bash
export LOG_LEVEL="debug"
export LOG_FORMAT="json"
export LOG_BUFFER_SIZE="50000"
export LOG_RATE_LIMIT="1000"
export LOG_RATE_BURST="100"
./your-application
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

## License

[MIT License](LICENSE)
