package logger

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// InitLogger initializes the global logger based on environment variables
// LOG_LEVEL: debug, info, warn, error (default: info)
// LOG_FORMAT: json, pretty (default: json)
func InitLogger() {
	// Set log level from environment variable
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	switch logLevel {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn", "warning":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}

	// Set log format from environment variable
	logFormat := strings.ToLower(os.Getenv("LOG_FORMAT"))
	if logFormat == "pretty" {
		log.Logger = log.Output(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		})
	} else {
		// Default to JSON format
		zerolog.TimeFieldFormat = time.RFC3339
	}

	// Add caller information for debugging
	if logLevel == "debug" {
		log.Logger = log.With().Caller().Logger()
	}
}

// GetLogger returns a logger instance
func GetLogger() *zerolog.Logger {
	return &log.Logger
}

// Info logs an info level message
func Info() *zerolog.Event {
	return log.Info()
}

// Debug logs a debug level message
func Debug() *zerolog.Event {
	return log.Debug()
}

// Warn logs a warning level message
func Warn() *zerolog.Event {
	return log.Warn()
}

// Error logs an error level message
func Error() *zerolog.Event {
	return log.Error()
}

// Fatal logs a fatal level message and calls os.Exit(1)
func Fatal() *zerolog.Event {
	return log.Fatal()
}
