package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelTrace is more verbose than debug and used for very chatty messages, like
// every MQTT message or CEC command sent.
const LevelTrace = slog.LevelDebug - 4

// setupLogging installs the default slog logger.
func setupLogging(w io.Writer, level string, format string) error {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "trace":
		lvl = LevelTrace
	case "debug":
		lvl = slog.LevelDebug
	case "info", "":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	// panic and fatal are accepted for compatibility with the logrus based versions.
	case "error", "fatal", "panic":
		lvl = slog.LevelError
	default:
		return fmt.Errorf("unknown log level %q", level)
	}

	options := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.LevelKey && len(groups) == 0 {
				if l, ok := attr.Value.Any().(slog.Level); ok && l == LevelTrace {
					attr.Value = slog.StringValue("TRACE")
				}
			}
			return attr
		},
	}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text", "":
		handler = slog.NewTextHandler(w, options)
	case "json":
		handler = slog.NewJSONHandler(w, options)
	default:
		return fmt.Errorf("unknown log format %q", format)
	}

	slog.SetDefault(slog.New(handler))

	return nil
}

// trace logs a message at LevelTrace using the given logger.
func trace(logger *slog.Logger, msg string, args ...any) {
	logger.Log(context.Background(), LevelTrace, msg, args...)
}

// logTrace logs a message at LevelTrace using the default logger.
func logTrace(msg string, args ...any) {
	trace(slog.Default(), msg, args...)
}

// logFatal logs a message at error level and exits the process.
func logFatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}
