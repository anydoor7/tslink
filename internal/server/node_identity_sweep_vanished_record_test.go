package server

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// A record that disappears between the sweep's ReadDir and its read left
// nothing to prune. It used to be reported as "does not match service" and
// logged as a record that "cannot be read safely", which sends an operator
// looking for a corrupt file that does not exist. Both ways the race shows up
// are covered: the read finds no file, or the file vanishes inside the read
// (after the mode check's Lstat, before its chmod).
func TestNodeIdentitySweepSkipsRecordThatVanishedAfterListing(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	kept := registry.Service{Name: "files", Type: registry.TypeFile, Path: t.TempDir()}
	writeRegistry(t, []registry.Service{kept})
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)

	paths := map[string]string{}
	for _, name := range []string{"gone", "gone-mid-read", "mismatch"} {
		path, err := s.nodeIdentityPath(name)
		if err != nil {
			t.Fatal(err)
		}
		owner := name
		if name == "mismatch" {
			owner = "someone-else"
		}
		identity := requestedNodeIdentity(registry.Service{Name: owner, Type: registry.TypeProxy, Target: "http://localhost:3000"}, "", identityPreparedBeforeUp)
		if err := writeNodeIdentity(path, identity); err != nil {
			t.Fatal(err)
		}
		paths[name] = path
	}

	oldRead := readNodeIdentityFn
	readNodeIdentityFn = func(path string) (nodeIdentity, bool, error) {
		switch path {
		case paths["gone"]:
			if err := os.Remove(path); err != nil {
				t.Errorf("remove vanishing record: %v", err)
			}
			return oldRead(path)
		case paths["gone-mid-read"]:
			if err := os.Remove(path); err != nil {
				t.Errorf("remove vanishing record: %v", err)
			}
			return nodeIdentity{}, false, &os.PathError{Op: "chmod", Path: path, Err: os.ErrNotExist}
		}
		return oldRead(path)
	}
	t.Cleanup(func() { readNodeIdentityFn = oldRead })

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	if err := s.removeAbsentNodeIdentities(map[string]registry.Service{kept.Name: kept}); err != nil {
		t.Fatalf("sweep error = %v", err)
	}
	logs := logBuf.String()
	var mismatchWarned bool
	for _, line := range strings.Split(logs, "\n") {
		if !strings.Contains(line, "it cannot be read safely") {
			continue
		}
		switch {
		case strings.Contains(line, "service=gone "), strings.Contains(line, "service=gone-mid-read "):
			t.Errorf("vanished record reported as unreadable: %s", line)
		case strings.Contains(line, "service=mismatch ") && strings.Contains(line, "does not match service"):
			mismatchWarned = true
		}
	}
	// Control: a record that is present but names another service must still
	// be reported, or the assertions above could pass on a silent sweep.
	if !mismatchWarned {
		t.Fatalf("mismatched record was not reported; logs:\n%s", logs)
	}
	if _, err := os.Stat(paths["mismatch"]); err != nil {
		t.Fatalf("mismatched record was not kept: %v", err)
	}
}
