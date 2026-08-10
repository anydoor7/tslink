package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	oldDefault := slog.Default()
	oldStderr := os.Stderr

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
		slog.SetDefault(oldDefault)
		_ = r.Close()
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("w.Close() error = %v", err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}

	return string(out)
}

func TestInitTextHandler(t *testing.T) {
	out := captureStderr(t, func() {
		Init(false)
		slog.Info("hello", "foo", "bar")
	})

	if !strings.Contains(out, "level=INFO") {
		t.Fatalf("text output missing level: %q", out)
	}
	if !strings.Contains(out, "msg=hello") {
		t.Fatalf("text output missing message: %q", out)
	}
	if !strings.Contains(out, "foo=bar") {
		t.Fatalf("text output missing attribute: %q", out)
	}
}

func TestInitJSONHandler(t *testing.T) {
	out := captureStderr(t, func() {
		Init(true)
		slog.Info("hello", "foo", "bar")
	})

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, out)
	}

	if got["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", got["level"])
	}
	if got["msg"] != "hello" {
		t.Fatalf("msg = %v, want hello", got["msg"])
	}
	if got["foo"] != "bar" {
		t.Fatalf("foo = %v, want bar", got["foo"])
	}
	if _, ok := got["time"]; !ok {
		t.Fatalf("time field missing from JSON output: %v", got)
	}
}

func TestTSNetUserLogfRoutesThroughTSLinkLogger(t *testing.T) {
	oldDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldDefault) })
	var buf bytes.Buffer
	InitTo(&buf, true)

	TSNetUserLogf("Tailscale auth URL: %s", "https://login.tailscale.com/a/test")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; output = %q", err, buf.String())
	}
	if got["source"] != "tsnet" {
		t.Fatalf("source = %v, want tsnet", got["source"])
	}
	if !strings.Contains(got["msg"].(string), "https://login.tailscale.com/a/test") {
		t.Fatalf("msg = %v, want formatted user-facing URL", got["msg"])
	}
}
