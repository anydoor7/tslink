package server

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
)

// NewFileHandler returns an HTTP handler that serves files from dir,
// confined to the directory subtree. Symlinks that resolve outside dir
// are rejected to prevent directory traversal escapes.
func NewFileHandler(dir string) http.Handler {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}
	return http.FileServer(http.FS(&safeFS{root: absDir}))
}

// safeFS is an fs.FS that serves files from root while rejecting any
// path component that is a symlink resolving outside the root tree.
type safeFS struct {
	root string
}

func (s *safeFS) Open(name string) (fs.File, error) {
	// fs.FS contract: name must be valid (no leading slash, no ..)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}

	fullPath := filepath.Join(s.root, filepath.FromSlash(name))

	// Resolve the real path after following all symlinks and verify it
	// stays within the root.
	realPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	// Also resolve root in case root itself has symlinks in its path.
	realRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	// Ensure the resolved path is within the resolved root.
	// filepath.Rel returns a clean relative path; if it starts with ".."
	// the target is outside the root.
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || len(rel) > 1 && rel[:2] == ".." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}

	return os.Open(realPath)
}
