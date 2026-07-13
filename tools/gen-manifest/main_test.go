package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMinimumGoVersionFromGoMod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n\ngo 1.26.5\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := minimumGoVersionFromGoMod(path)
	if err != nil {
		t.Fatalf("minimumGoVersionFromGoMod() error = %v", err)
	}
	if got != "1.26.5" {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want 1.26.5", got)
	}
}

func TestMinimumGoVersionFromGoModFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got, err := minimumGoVersionFromGoMod(path); err == nil {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want error for missing go directive", got)
	}
}
