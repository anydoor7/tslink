package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// NewFileHandler returns an HTTP handler that serves files from dir,
// confined to the directory subtree. Path opens are anchored at the OS root
// handle for dir, so a concurrent symlink swap cannot turn a prior path check
// into an open outside the served tree.
//
// dir must be a non-empty absolute path. Empty or relative roots are rejected
// with an error rather than silently resolving to the process working directory
// (which for a Unix daemon is `/`). This is defense-in-depth: the canonical
// pre-side-effect validation already rejects empty/relative file paths before a
// node is constructed, but a direct caller of this constructor must not be able
// to serve cwd by mistake.
func NewFileHandler(dir string) (*FileHandler, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("file service root path is empty")
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("file service root path %q must be absolute", dir)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve file service root %q: %w", dir, err)
	}
	root, err := os.OpenRoot(absDir)
	if err != nil {
		return nil, fmt.Errorf("open file service root %q: %w", dir, err)
	}
	fsys := &safeFS{root: root}
	return &FileHandler{handler: http.FileServer(http.FS(fsys)), fsys: fsys}, nil
}

// FileHandler pins the directory object resolved at construction time. An
// initial symlink root is allowed and its then-current referent is pinned;
// replacing the path later cannot retarget requests. Close releases the one
// root descriptor held for this file node's lifetime.
type FileHandler struct {
	handler   http.Handler
	fsys      *safeFS
	closeOnce sync.Once
	closeErr  error
}

func (h *FileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

func (h *FileHandler) Close() error {
	h.closeOnce.Do(func() { h.closeErr = h.fsys.Close() })
	return h.closeErr
}

// safeFS is an fs.FS that serves files from root using os.Root. Unlike
// check-then-open confinement, Root.Open resolves and opens relative to the
// anchored root handle in one operation.
type safeFS struct {
	root *os.Root
}

func (s *safeFS) Open(name string) (fs.File, error) {
	// fs.FS contract: name must be valid (no leading slash, no ..)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	f, err := s.root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f, nil
}

func (s *safeFS) Close() error { return s.root.Close() }
