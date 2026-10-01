package server

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anydoor7/tslink/internal/registry"
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
	fsys, err := openServedRoot(dir)
	if err != nil {
		return nil, err
	}
	return &FileHandler{handler: http.FileServer(http.FS(fsys)), fsys: fsys}, nil
}

// NewSingleFileHandler returns an HTTP handler that serves exactly one file out
// of dir: GET /<file> is the file, GET / redirects to it, and every other path
// is 404 with no directory listing and no access to any sibling.
//
// It exists because a file share created from a regular file still has to
// anchor its opens somewhere, and the only anchor a file has is its parent
// directory. Serving that directory is what made `tslink share ./report.html`
// publish every sibling of report.html; the root here is still the directory,
// but the routing above is the whole reachable surface, so the sibling is not
// addressable rather than merely unlisted.
//
// file must be a bare file name. The caller has already enforced that at the
// registry boundary; repeating the check here is defense-in-depth for a direct
// caller of this constructor, the same reason NewFileHandler re-checks its own
// root. It calls the registry's own predicate rather than restating it, because
// a restated copy is a copy that can drift -- and the first version of this
// function did drift, accepting and rejecting different sets of whitespace.
func NewSingleFileHandler(dir, file string) (*FileHandler, error) {
	if err := registry.ValidateServedFileName(file); err != nil {
		return nil, fmt.Errorf("file service served file name: %w", err)
	}
	fsys, err := openServedRoot(dir)
	if err != nil {
		return nil, err
	}
	handler := &singleFileHandler{fsys: fsys, file: file, location: (&url.URL{Path: "/" + file}).String()}
	return &FileHandler{handler: handler, fsys: fsys}, nil
}

func openServedRoot(dir string) (*safeFS, error) {
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
	return &safeFS{root: root}, nil
}

// singleFileHandler routes by exact match on the decoded request path. Exact
// match rather than sanitisation is what makes the traversal cases uninteresting
// here: `/../secret`, `/%2e%2e/secret` and `/sibling.txt` all fail the same
// comparison and never reach an open. The open that does happen still goes
// through safeFS, so the os.Root anchoring of the directory case is unchanged.
type singleFileHandler struct {
	fsys     *safeFS
	file     string
	location string
}

func (h *singleFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		http.Redirect(w, r, h.location, http.StatusFound)
		return
	case "/" + h.file:
	default:
		http.NotFound(w, r)
		return
	}

	f, err := h.fsys.Open(h.file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		// A name that became a directory (or a device, or a socket) between
		// registration and this request is not this handler's file any more.
		// Falling through to http.FileServer's directory handling here is
		// exactly the listing this type exists to prevent.
		http.NotFound(w, r)
		return
	}
	seeker, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, "file service cannot range over this file", http.StatusInternalServerError)
		return
	}
	// ServeContent is what http.FileServer uses underneath, so Content-Type
	// sniffing, Last-Modified, If-Modified-Since, If-Range and Range replies
	// are the same as the directory case.
	http.ServeContent(w, r, h.file, info.ModTime(), seeker)
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
