package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "line3" || lines[2] != "line5" {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestTailFile_AllLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	content := "a\nb\nc\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
}

func TestTailFile_MissingFile(t *testing.T) {
	lines, err := tailFile("/nonexistent/file.log", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if lines != nil {
		t.Fatalf("expected nil for missing file, got %v", lines)
	}
}

func TestTailFile_LevelFilter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	content := `time=2024-01-01 level=DEBUG msg="debug"
time=2024-01-01 level=INFO msg="info"
time=2024-01-01 level=WARN msg="warn"
time=2024-01-01 level=ERROR msg="error"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 100, "warn")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (warn+error), got %d: %v", len(lines), lines)
	}
}

func TestTailFile_LevelFilterJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	content := `{"time":"2024-01-01","level":"INFO","msg":"info"}
{"time":"2024-01-01","level":"ERROR","msg":"error"}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	lines, err := tailFile(path, 100, "error")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (error only), got %d: %v", len(lines), lines)
	}
}

func TestMatchLevel(t *testing.T) {
	tests := []struct {
		line  string
		level string
		want  bool
	}{
		{"level=DEBUG msg=test", "debug", true},
		{"level=DEBUG msg=test", "info", false},
		{"level=ERROR msg=test", "warn", true},
		{"level=INFO msg=test", "error", false},
		{`"level":"WARN"`, "info", true},
		{`"level":"INFO"`, "warn", false},
		{"no level info here", "info", false},
		{"anything", "unknown", true}, // unknown level shows everything
	}

	for _, tt := range tests {
		got := matchLevel(tt.line, tt.level)
		if got != tt.want {
			t.Errorf("matchLevel(%q, %q) = %v, want %v", tt.line, tt.level, got, tt.want)
		}
	}
}
