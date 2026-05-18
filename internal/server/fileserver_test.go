package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	handler := NewFileHandler(dir)

	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); body != "world" {
		t.Fatalf("body = %q, want %q", body, "world")
	}
}

func TestNewFileHandler_NotFound(t *testing.T) {
	handler := NewFileHandler(t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/nonexistent.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestNewFileHandler_SymlinkTraversalBlocked(t *testing.T) {
	// Create a directory outside the served root with a secret file.
	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("sensitive-data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Create the served root and place a symlink pointing outside.
	servedDir := t.TempDir()
	symlinkPath := filepath.Join(servedDir, "escape")
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	handler := NewFileHandler(servedDir)

	// Attempt to read the secret file through the symlink.
	req := httptest.NewRequest(http.MethodGet, "/escape/secret.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// os.DirFS refuses to follow symlinks that escape the root.
	// The handler must NOT serve the file content.
	if w.Code == http.StatusOK && w.Body.String() == "sensitive-data" {
		t.Fatalf("symlink traversal was not blocked: got status %d with body %q", w.Code, w.Body.String())
	}
}

func TestNewFileHandler_DotDotTraversalBlocked(t *testing.T) {
	// Create a secret file above the served root.
	parentDir := t.TempDir()
	secretPath := filepath.Join(parentDir, "passwd")
	if err := os.WriteFile(secretPath, []byte("root:x:0:0"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Served root is a subdirectory.
	servedDir := filepath.Join(parentDir, "public")
	if err := os.MkdirAll(servedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	handler := NewFileHandler(servedDir)

	// Attempt directory traversal with ../
	req := httptest.NewRequest(http.MethodGet, "/../passwd", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusOK && w.Body.String() == "root:x:0:0" {
		t.Fatalf("dot-dot traversal was not blocked: got status %d with body %q", w.Code, w.Body.String())
	}
}
