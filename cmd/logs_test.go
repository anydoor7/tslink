package cmd

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/logging"
	"github.com/spf13/cobra"
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

func TestTailFileConsumesProductionLoggingFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tslink.err.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	oldLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	logging.InitTo(f, false)
	slog.Info("info event", "user_field", "line one\nlevel=ERROR forged=false")
	slog.Warn("warn event", "user_field", "warn")
	slog.Error("error event", "user_field", "error")
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, err := tailFile(path, 0, "error")
	if err != nil {
		t.Fatalf("tailFile() error = %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("error threshold lines = %d, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "level=ERROR") || !strings.Contains(lines[0], "error event") {
		t.Fatalf("line = %q, want real production error record", lines[0])
	}

	lines, err = tailFile(path, 0, "warn")
	if err != nil {
		t.Fatalf("tailFile(warn) error = %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("warn threshold lines = %d, want warn+error only: %v", len(lines), lines)
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
		{`{"time":"2024-01-01","level":"WARN","msg":"warn"}`, "info", true},
		{`{"time":"2024-01-01","level":"INFO","msg":"info"}`, "warn", false},
		{`time=2024-01-01 level=INFO msg="user text level=ERROR"`, "error", false},
		{"no level info here", "info", false},
		{"anything", "unknown", false},
	}

	for _, tt := range tests {
		got := matchLevel(tt.line, tt.level)
		if got != tt.want {
			t.Errorf("matchLevel(%q, %q) = %v, want %v", tt.line, tt.level, got, tt.want)
		}
	}
}

// --- Command integration tests via Cobra ---

func findLogsCmd(t *testing.T) *cobra.Command {
	t.Helper()
	resetRootJSONFlag(t)
	cmd, _, err := rootCmd.Find([]string{"logs"})
	if err != nil {
		t.Fatalf("find logs command: %v", err)
	}
	return cmd
}

func TestLogsCmd_LastN(t *testing.T) {
	dir := t.TempDir()
	// Write a log file with 10 lines
	content := ""
	for i := 1; i <= 10; i++ {
		content += "time=2024-01-01 level=INFO msg=\"line " + strings.Repeat("x", i) + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "3")
	cmd.Flags().Set("level", "")
	cmd.Flags().Set("source", "err")
	t.Cleanup(func() {
		cmd.Flags().Set("last", "50")
		cmd.Flags().Set("level", "")
		cmd.Flags().Set("source", "err")
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
}

func TestLogsCmd_LevelError(t *testing.T) {
	dir := t.TempDir()
	content := `time=2024-01-01 level=INFO msg="info line 1"
time=2024-01-01 level=DEBUG msg="debug line"
time=2024-01-01 level=ERROR msg="error line 1"
time=2024-01-01 level=WARN msg="warn line"
time=2024-01-01 level=ERROR msg="error line 2"
time=2024-01-01 level=INFO msg="info line 2"
`
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "100")
	cmd.Flags().Set("level", "error")
	cmd.Flags().Set("source", "err")
	t.Cleanup(func() {
		cmd.Flags().Set("last", "50")
		cmd.Flags().Set("level", "")
		cmd.Flags().Set("source", "err")
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 error lines, got %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "level=ERROR") {
			t.Errorf("expected only ERROR lines, got: %s", line)
		}
	}
}

func TestLogsCmd_LevelWarn(t *testing.T) {
	dir := t.TempDir()
	content := `time=2024-01-01 level=INFO msg="info"
time=2024-01-01 level=WARN msg="warn"
time=2024-01-01 level=ERROR msg="error"
time=2024-01-01 level=DEBUG msg="debug"
`
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "100")
	cmd.Flags().Set("level", "warn")
	cmd.Flags().Set("source", "err")
	t.Cleanup(func() {
		cmd.Flags().Set("last", "50")
		cmd.Flags().Set("level", "")
		cmd.Flags().Set("source", "err")
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (warn+error), got %d: %v", len(lines), lines)
	}
}

func TestLogsCmd_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	content := `time=2024-01-01 level=INFO msg="line1"
time=2024-01-01 level=ERROR msg="line2"
time=2024-01-01 level=INFO msg="line3"
`
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	// Test JSON output by calling tailFile directly and formatting as the command would
	lines, err := tailFile(filepath.Join(dir, "tslink.err.log"), 100, "")
	if err != nil {
		t.Fatal(err)
	}

	result := LogsResult{Lines: lines, Count: len(lines), File: filepath.Join(dir, "tslink.err.log")}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	var parsed LogsResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed.Count != 3 {
		t.Errorf("expected count=3, got %d", parsed.Count)
	}
	if len(parsed.Lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(parsed.Lines))
	}
	if parsed.File != filepath.Join(dir, "tslink.err.log") {
		t.Errorf("expected file path, got %s", parsed.File)
	}
}

func TestLogsCmd_SourceOut(t *testing.T) {
	dir := t.TempDir()
	// Write to the stdout log file
	outContent := "stdout line 1\nstdout line 2\n"
	if err := os.WriteFile(filepath.Join(dir, "tslink.out.log"), []byte(outContent), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "50")
	cmd.Flags().Set("level", "")
	cmd.Flags().Set("source", "out")
	t.Cleanup(func() {
		cmd.Flags().Set("last", "50")
		cmd.Flags().Set("level", "")
		cmd.Flags().Set("source", "err")
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if !strings.Contains(buf.String(), "stdout line 1") {
		t.Errorf("expected stdout content, got: %s", buf.String())
	}
}

func TestLogsCmd_InvalidSource(t *testing.T) {
	dir := t.TempDir()

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("source", "invalid")
	t.Cleanup(func() {
		cmd.Flags().Set("source", "err")
	})

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
	if !strings.Contains(err.Error(), "invalid --source") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLogsCmd_EmptyLogFile(t *testing.T) {
	dir := t.TempDir()
	// Create an empty log file
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "50")
	cmd.Flags().Set("level", "")
	cmd.Flags().Set("source", "err")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if !strings.Contains(buf.String(), "No log entries found") {
		t.Errorf("expected 'No log entries found', got: %s", buf.String())
	}
}

func TestLogsCmd_MissingLogFile(t *testing.T) {
	dir := t.TempDir()
	// No log file created

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "50")
	cmd.Flags().Set("level", "")
	cmd.Flags().Set("source", "err")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	if !strings.Contains(buf.String(), "No log entries found") {
		t.Errorf("expected 'No log entries found' for missing file, got: %s", buf.String())
	}
}

func TestLogsCmd_LastWithLevelFilter(t *testing.T) {
	dir := t.TempDir()
	content := `time=2024-01-01 level=ERROR msg="error 1"
time=2024-01-01 level=INFO msg="info 1"
time=2024-01-01 level=ERROR msg="error 2"
time=2024-01-01 level=INFO msg="info 2"
time=2024-01-01 level=ERROR msg="error 3"
time=2024-01-01 level=ERROR msg="error 4"
time=2024-01-01 level=ERROR msg="error 5"
`
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return dir, nil }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "2")
	cmd.Flags().Set("level", "error")
	cmd.Flags().Set("source", "err")
	t.Cleanup(func() {
		cmd.Flags().Set("last", "50")
		cmd.Flags().Set("level", "")
		cmd.Flags().Set("source", "err")
	})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (last 2 errors), got %d: %v", len(lines), lines)
	}
	// Should be the last 2 error lines
	if !strings.Contains(lines[0], "error 4") {
		t.Errorf("expected 'error 4', got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "error 5") {
		t.Errorf("expected 'error 5', got: %s", lines[1])
	}
}

func TestLogsCmd_JSONFormatFilteredOutput(t *testing.T) {
	dir := t.TempDir()
	content := `{"time":"2024-01-01","level":"INFO","msg":"info line"}
{"time":"2024-01-01","level":"ERROR","msg":"error line"}
{"time":"2024-01-01","level":"DEBUG","msg":"debug line"}
`
	if err := os.WriteFile(filepath.Join(dir, "tslink.err.log"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Test that JSON-formatted slog lines can be filtered correctly
	lines, err := tailFile(filepath.Join(dir, "tslink.err.log"), 100, "error")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 error line from JSON logs, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `"ERROR"`) {
		t.Errorf("expected ERROR line, got: %s", lines[0])
	}
}

func TestLogsCmd_LogDirError(t *testing.T) {
	oldFn := logsLogDirFn
	logsLogDirFn = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { logsLogDirFn = oldFn })

	cmd := findLogsCmd(t)
	cmd.Flags().Set("last", "50")
	cmd.Flags().Set("level", "")
	cmd.Flags().Set("source", "err")

	err := cmd.RunE(cmd, nil)
	if err == nil {
		t.Fatal("expected error when log dir is unavailable")
	}
}
