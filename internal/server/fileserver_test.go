package server

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewFileHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	handler, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler(%q) error = %v", dir, err)
	}

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

func TestSafeFSAllowsContainedDotDotNames(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "..config"), []byte("config"), 0o600); err != nil {
		t.Fatalf("WriteFile(..config) error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "..foo"), 0o700); err != nil {
		t.Fatalf("Mkdir(..foo) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "..foo", "bar"), []byte("bar"), 0o600); err != nil {
		t.Fatalf("WriteFile(..foo/bar) error = %v", err)
	}

	fsys := &safeFS{root: root}
	for _, name := range []string{"..config", "..foo/bar"} {
		t.Run(name, func(t *testing.T) {
			f, err := fsys.Open(name)
			if err != nil {
				t.Fatalf("Open(%q) error = %v", name, err)
			}
			defer f.Close()
			if _, err := io.ReadAll(f); err != nil {
				t.Fatalf("ReadAll(%q) error = %v", name, err)
			}
		})
	}
}

func TestSafeFSRejectsCanonicalTraversalAndSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "public")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir(root) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	if err := os.Symlink(parent, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	fsys := &safeFS{root: root}
	for _, name := range []string{"../secret", "escape/secret"} {
		t.Run(name, func(t *testing.T) {
			f, err := fsys.Open(name)
			if err == nil {
				f.Close()
				t.Fatalf("Open(%q) error = nil, want rejection", name)
			}
			if !errors.Is(err, fs.ErrInvalid) && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Open(%q) error = %v, want invalid/not-exist", name, err)
			}
		})
	}
}

func TestNewFileHandlerRejectsUnsafeRoot(t *testing.T) {
	// Defense-in-depth: direct construction with an empty or relative root must
	// return an error instead of silently serving the process working directory.
	cases := []struct {
		name string
		dir  string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"relative", "relative/path"},
		{"dot", "."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := NewFileHandler(tc.dir)
			if err == nil {
				t.Fatalf("NewFileHandler(%q) error = nil, want rejection", tc.dir)
			}
			if handler != nil {
				t.Fatalf("NewFileHandler(%q) returned a non-nil handler on error", tc.dir)
			}
		})
	}
}

func TestNewFileHandler_NotFound(t *testing.T) {
	handler, err := NewFileHandler(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileHandler() error = %v", err)
	}

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

	handler, err := NewFileHandler(servedDir)
	if err != nil {
		t.Fatalf("NewFileHandler() error = %v", err)
	}

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

func TestSafeFSRejectsConcurrentSymlinkSwapEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires privileges not guaranteed in CI")
	}

	parent := t.TempDir()
	root := filepath.Join(parent, "public")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir(root) error = %v", err)
	}
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0o600); err != nil {
		t.Fatalf("WriteFile(inside) error = %v", err)
	}
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}

	link := filepath.Join(root, "flip")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("Symlink() unavailable on this platform/user: %v", err)
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{inside, outside} {
				next := link + ".next"
				_ = os.Remove(next)
				if err := os.Symlink(target, next); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				if err := os.Rename(next, link); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})

	fsys := &safeFS{root: root}
	for i := 0; i < 5000; i++ {
		select {
		case err := <-errCh:
			t.Fatalf("symlink swap worker failed: %v", err)
		default:
		}
		f, err := fsys.Open("flip")
		if err != nil {
			continue
		}
		body, readErr := io.ReadAll(f)
		closeErr := f.Close()
		if readErr != nil {
			t.Fatalf("ReadAll(flip) error = %v", readErr)
		}
		if closeErr != nil {
			t.Fatalf("Close(flip) error = %v", closeErr)
		}
		if string(body) == "secret" {
			t.Fatalf("safeFS opened root-external file during concurrent symlink swap")
		}
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

	handler, err := NewFileHandler(servedDir)
	if err != nil {
		t.Fatalf("NewFileHandler() error = %v", err)
	}

	// Attempt directory traversal with ../
	req := httptest.NewRequest(http.MethodGet, "/../passwd", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusOK && w.Body.String() == "root:x:0:0" {
		t.Fatalf("dot-dot traversal was not blocked: got status %d with body %q", w.Code, w.Body.String())
	}
}
