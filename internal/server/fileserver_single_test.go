package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// newSingleFileFixture builds the shape `tslink share ./report.html` produces:
// one shared file with a sibling that must stay unreachable, plus a
// subdirectory and a secret one level above the served root.
func newSingleFileFixture(t *testing.T) (dir string, handler *FileHandler) {
	t.Helper()
	parent := t.TempDir()
	dir = filepath.Join(parent, "reports")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	for name, body := range map[string]string{
		"report.html":  "SHARED",
		"secrets.env":  "SIBLING SECRET",
		"index.html":   "SIBLING INDEX",
		"..config":     "DOTTED SIBLING",
		"Report v2.md": "SPACED SIBLING",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("Mkdir(sub) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("NESTED"), 0o600); err != nil {
		t.Fatalf("WriteFile(sub/nested.txt) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("OUTSIDE"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside.txt) error = %v", err)
	}

	handler, err := NewSingleFileHandler(dir, "report.html")
	if err != nil {
		t.Fatalf("NewSingleFileHandler() error = %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })
	return dir, handler
}

func get(t *testing.T, handler http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// TestSingleFileHandlerServesOnlyTheSharedFile is the control: the one path that
// must work has to work, otherwise every 404 below would pass against a handler
// that serves nothing at all.
func TestSingleFileHandlerServesOnlyTheSharedFile(t *testing.T) {
	_, handler := newSingleFileFixture(t)

	w := get(t, handler, "/report.html")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /report.html status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); body != "SHARED" {
		t.Fatalf("GET /report.html body = %q, want %q", body, "SHARED")
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
}

// TestSingleFileHandlerRootRedirectsToTheFile pins the one convenience path: the
// bare service URL is what a user pastes, and it has to land on the file rather
// than on a listing.
func TestSingleFileHandlerRootRedirectsToTheFile(t *testing.T) {
	_, handler := newSingleFileFixture(t)

	w := get(t, handler, "/")
	if w.Code != http.StatusFound {
		t.Fatalf("GET / status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/report.html" {
		t.Fatalf("GET / Location = %q, want %q", loc, "/report.html")
	}
	if body := w.Body.String(); strings.Contains(body, "secrets.env") {
		t.Fatalf("redirect body leaked a sibling name: %q", body)
	}
}

// TestSingleFileHandlerRefusesEverySiblingAndListing is the property the fix
// exists for. Each case is a way the previous directory handler would have
// answered 200.
func TestSingleFileHandlerRefusesEverySiblingAndListing(t *testing.T) {
	_, handler := newSingleFileFixture(t)

	cases := []struct {
		name   string
		target string
	}{
		{"sibling_file", "/secrets.env"},
		{"sibling_index_html", "/index.html"},
		{"sibling_dotted", "/..config"},
		{"sibling_with_space_encoded", "/Report%20v2.md"},
		{"directory_listing_of_root", "/?x=1"},
		{"subdirectory_listing", "/sub/"},
		{"nested_file", "/sub/nested.txt"},
		{"dotdot_escape", "/../outside.txt"},
		{"encoded_dotdot_escape", "/%2e%2e/outside.txt"},
		{"encoded_slash_sibling", "/%2Fsecrets.env"},
		{"double_slash_prefix", "//report.html"},
		{"served_file_under_subpath", "/sub/report.html"},
		{"trailing_slash_on_served_file", "/report.html/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := get(t, handler, tc.target)
			if w.Code == http.StatusOK {
				t.Fatalf("GET %s status = 200, want a refusal; body=%q", tc.target, w.Body.String())
			}
			for _, leak := range []string{"SIBLING SECRET", "SIBLING INDEX", "NESTED", "OUTSIDE", "DOTTED SIBLING", "SPACED SIBLING"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Fatalf("GET %s leaked %q; body=%q", tc.target, leak, w.Body.String())
				}
			}
			// A listing names its entries even when it answers 200 through a
			// different status, so check the body independently of the code.
			if strings.Contains(w.Body.String(), "secrets.env") {
				t.Fatalf("GET %s produced a directory listing: %q", tc.target, w.Body.String())
			}
		})
	}
}

// TestSingleFileHandlerSupportsRangeRequests pins that narrowing the surface did
// not cost the ordinary file-serving behaviour the directory handler had.
func TestSingleFileHandlerSupportsRangeRequests(t *testing.T) {
	_, handler := newSingleFileFixture(t)

	req := httptest.NewRequest(http.MethodGet, "/report.html", nil)
	req.Header.Set("Range", "bytes=0-2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("range status = %d, want %d", w.Code, http.StatusPartialContent)
	}
	if body := w.Body.String(); body != "SHA" {
		t.Fatalf("range body = %q, want %q", body, "SHA")
	}
	if w.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("Accept-Ranges = %q, want bytes", w.Header().Get("Accept-Ranges"))
	}
}

// TestSingleFileHandlerRefusesADirectoryTarget covers the name that stops being
// a file after registration. Serving it through the file-server path would
// produce the listing this type exists to prevent.
func TestSingleFileHandlerRefusesADirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.txt"), []byte("NESTED"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	handler, err := NewSingleFileHandler(dir, "sub")
	if err != nil {
		t.Fatalf("NewSingleFileHandler() error = %v", err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	w := get(t, handler, "/sub")
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /sub status = %d, want %d; body=%q", w.Code, http.StatusNotFound, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "nested.txt") {
		t.Fatalf("directory target produced a listing: %q", w.Body.String())
	}
}

// TestSingleFileHandlerRejectsUnsafeArguments is defense-in-depth at the
// constructor, matching TestNewFileHandlerRejectsUnsafeRoot for the root.
func TestSingleFileHandlerRejectsUnsafeArguments(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		dir  string
		file string
	}{
		{"empty_file", dir, ""},
		{"whitespace_file", dir, "   "},
		{"tab_only_file", dir, "\t"},
		{"embedded_newline_file", dir, "a\nb"},
		{"embedded_nul_file", dir, "a\x00b"},
		{"separator_file", dir, "sub/report.html"},
		{"windows_separator_file", dir, `sub\report.html`},
		{"parent_file", dir, ".."},
		{"current_file", dir, "."},
		{"absolute_file", dir, "/etc/passwd"},
		{"relative_dir", "relative/path", "report.html"},
		{"empty_dir", "", "report.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, err := NewSingleFileHandler(tc.dir, tc.file)
			if err == nil {
				_ = handler.Close()
				t.Fatalf("NewSingleFileHandler(%q, %q) error = nil, want rejection", tc.dir, tc.file)
			}
			if handler != nil {
				t.Fatalf("NewSingleFileHandler(%q, %q) returned a non-nil handler on error", tc.dir, tc.file)
			}
		})
	}
	// Control: the same constructor accepts the shape share actually produces,
	// so the table above is rejecting arguments rather than rejecting always.
	handler, err := NewSingleFileHandler(dir, "report.html")
	if err != nil {
		t.Fatalf("NewSingleFileHandler(valid) error = %v", err)
	}
	_ = handler.Close()
}

// TestFileHandlerSelectionFollowsTheRegistryField pins the dispatch the daemon
// performs: the presence of the file field is what decides between the two
// handlers, and its absence must still mean the whole directory.
func TestFileHandlerSelectionFollowsTheRegistryField(t *testing.T) {
	dir, _ := newSingleFileFixture(t)

	for _, tc := range []struct {
		file          string
		siblingStatus int
	}{
		{file: "report.html", siblingStatus: http.StatusNotFound},
		{file: "", siblingStatus: http.StatusOK},
	} {
		t.Run(fmt.Sprintf("file=%q", tc.file), func(t *testing.T) {
			var handler *FileHandler
			var err error
			if tc.file != "" {
				handler, err = NewSingleFileHandler(dir, tc.file)
			} else {
				handler, err = NewFileHandler(dir)
			}
			if err != nil {
				t.Fatalf("handler construction error = %v", err)
			}
			t.Cleanup(func() { _ = handler.Close() })

			if w := get(t, handler, "/secrets.env"); w.Code != tc.siblingStatus {
				t.Fatalf("GET /secrets.env status = %d, want %d", w.Code, tc.siblingStatus)
			}
		})
	}
}

// TestStartNodeDispatchesFileServiceOnTheRegistryFileField exercises the branch
// inside startNodeLocked rather than the two constructors, because that branch
// is what turns a registry field into a reachable surface. It asserts through
// the node's real handler chain, so a dispatch that ignored svc.File would show
// up here as a sibling answering 200.
func TestStartNodeDispatchesFileServiceOnTheRegistryFileField(t *testing.T) {
	for _, tc := range []struct {
		name          string
		file          string
		siblingStatus int
	}{
		{name: "narrowed_by_file_field", file: "report.html", siblingStatus: http.StatusNotFound},
		{name: "legacy_registry_without_file_field", file: "", siblingStatus: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			dir, _ := newSingleFileFixture(t)

			s, err := New("key", "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			svc := registry.Service{Name: "files", Type: registry.TypeFile, Path: dir, File: tc.file}
			if err := registry.ValidateService(svc); err != nil {
				t.Fatalf("ValidateService() error = %v", err)
			}
			path, err := registryPathFn()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			if err := s.startNodeLocked(context.Background(), svc); err != nil {
				t.Fatalf("startNodeLocked() error = %v", err)
			}
			node := s.nodes["files"]
			if node == nil || node.httpSrv == nil {
				t.Fatalf("node = %+v, want a started node with an HTTP server", node)
			}
			t.Cleanup(func() {
				s.mu.Lock()
				s.stopNodeLocked("files")
				s.mu.Unlock()
			})

			if w := get(t, node.httpSrv.Handler, "/report.html"); w.Code != http.StatusOK {
				t.Fatalf("GET /report.html status = %d, want 200 in both modes", w.Code)
			}
			w := get(t, node.httpSrv.Handler, "/secrets.env")
			if w.Code != tc.siblingStatus {
				t.Fatalf("GET /secrets.env status = %d, want %d", w.Code, tc.siblingStatus)
			}
			if tc.siblingStatus == http.StatusNotFound && strings.Contains(w.Body.String(), "SIBLING SECRET") {
				t.Fatalf("sibling content leaked through the node handler: %q", w.Body.String())
			}
		})
	}
}

// TestServiceChanged_ServedFile pins that a change to the served file name
// restarts the node. The widening direction is the one that matters: dropping
// the field turns a single-file share back into its whole parent directory, and
// a daemon that did not notice would keep the old narrowing only by accident,
// until the next unrelated restart quietly published the directory.
func TestServiceChanged_ServedFile(t *testing.T) {
	base := registry.Service{Name: "report", Type: registry.TypeFile, Path: "/tmp/reports", File: "report.html"}

	narrowedElsewhere := base
	narrowedElsewhere.File = "other.html"
	if !serviceChanged(base, narrowedElsewhere) {
		t.Error("a different served file should restart the service")
	}

	widened := base
	widened.File = ""
	if !serviceChanged(base, widened) {
		t.Error("dropping the served file widens the share to the whole directory and should restart the service")
	}

	narrowed := widened
	if !serviceChanged(widened, base) {
		t.Errorf("adding a served file to %+v should restart the service", narrowed)
	}

	// Control: an unchanged service must still compare equal, otherwise the
	// assertions above would hold against a comparison that always reports a
	// change and restarts every node on every registry read.
	if serviceChanged(base, base) {
		t.Error("an unchanged single-file service should not restart")
	}
}

// TestHoldsNodeStateReportsOnlyRunningNodes pins the fact the lifecycle
// reconciliation depends on before it deletes a state directory. Both answers
// are asserted: a method that always said true would keep every orphan
// directory forever, and one that always said false would delete state
// underneath a live node.
func TestHoldsNodeStateReportsOnlyRunningNodes(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	if s.HoldsNodeState("files") {
		t.Fatal("HoldsNodeState(\"files\") = true before the node started")
	}
	if err := s.startNodeLocked(context.Background(), registry.Service{Name: "files", Type: registry.TypeFile, Path: t.TempDir()}); err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	if !s.HoldsNodeState("files") {
		t.Fatal("HoldsNodeState(\"files\") = false while the node is running")
	}
	if s.HoldsNodeState("other") {
		t.Fatal("HoldsNodeState(\"other\") = true for a name this server never started")
	}

	s.mu.Lock()
	s.stopNodeLocked("files")
	s.mu.Unlock()
	if s.HoldsNodeState("files") {
		t.Fatal("HoldsNodeState(\"files\") = true after the node stopped and released its state")
	}
}
