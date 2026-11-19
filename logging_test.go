package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestSetupLogging(t *testing.T) {
	defer slog.SetDefault(slog.Default())

	var buf bytes.Buffer
	if err := setupLogging(&buf, "trace", "text"); err != nil {
		t.Fatal(err)
	}

	logTrace("hello", "key", "value")
	if out := buf.String(); !strings.Contains(out, "level=TRACE") || !strings.Contains(out, "key=value") {
		t.Errorf("unexpected trace output: %q", out)
	}

	buf.Reset()
	if err := setupLogging(&buf, "info", "json"); err != nil {
		t.Fatal(err)
	}

	slog.Debug("hidden")
	logTrace("hidden")
	slog.Info("shown", "key", "value")

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected exactly one JSON log line, got %q: %v", buf.String(), err)
	}
	if entry["msg"] != "shown" || entry["key"] != "value" {
		t.Errorf("unexpected JSON entry: %v", entry)
	}
}

func TestSetupLoggingInvalid(t *testing.T) {
	defer slog.SetDefault(slog.Default())

	if err := setupLogging(&bytes.Buffer{}, "verbose", "text"); err == nil {
		t.Error("expected error for unknown level")
	}
	if err := setupLogging(&bytes.Buffer{}, "info", "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
