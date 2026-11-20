package logging

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
	if err := Setup(&buf, "trace", "text"); err != nil {
		t.Fatal(err)
	}

	Trace("hello", "key", "value")
	if out := buf.String(); !strings.Contains(out, "level=TRACE") || !strings.Contains(out, "key=value") {
		t.Errorf("unexpected trace output: %q", out)
	}

	buf.Reset()
	if err := Setup(&buf, "info", "json"); err != nil {
		t.Fatal(err)
	}

	slog.Debug("hidden")
	Trace("hidden")
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

	if err := Setup(&bytes.Buffer{}, "verbose", "text"); err == nil {
		t.Error("expected error for unknown level")
	}
	if err := Setup(&bytes.Buffer{}, "info", "xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}
