package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
func NewFileHandler(dir string) (http.Handler, error) {
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
	return http.FileServer(http.FS(&safeFS{root: absDir})), nil
}

// safeFS is an fs.FS that serves files from root using os.Root. Unlike
// check-then-open confinement, Root.Open resolves and opens relative to the
// anchored root handle in one operation.
type safeFS struct {
	root string
}

func (s *safeFS) Open(name string) (fs.File, error) {
	// fs.FS contract: name must be valid (no leading slash, no ..)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	defer root.Close()

	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f, nil
}
