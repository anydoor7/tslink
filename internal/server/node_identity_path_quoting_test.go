package server

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// The node identity messages quoted their paths with %q, which doubles every
// backslash: on Windows that is every separator, so the per-service failure
// and both recovery steps named a record that does not exist
// ("C:\\Users\\..."), and the node identity tests that look for the record
// path failed there. It is reproduced on every OS by putting a backslash in
// the path itself, which unix allows in a file name and Windows reads as a
// separator. This is the node identity half of W-4.
func TestNodeIdentityMessagesShowThePathVerbatim(t *testing.T) {
	home := filepath.Join(t.TempDir(), `back\slash`)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	testenv.SetHome(t, home)
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	recordPath, err := s.nodeIdentityPath("api")
	if err != nil {
		t.Fatal(err)
	}
	identitiesDir := filepath.Dir(recordPath)
	// Control: the escaped and the verbatim spelling must differ, or the
	// negative assertions below cannot tell them apart.
	if strconv.Quote(identitiesDir) == `"`+identitiesDir+`"` {
		t.Fatalf("control: %s has nothing %%q would escape", identitiesDir)
	}
	assertVerbatim := func(t *testing.T, text, path string) {
		t.Helper()
		if !strings.Contains(text, `"`+path+`"`) {
			t.Errorf("%q does not name %s verbatim", text, path)
		}
		if strings.Contains(text, strconv.Quote(path)) {
			t.Errorf("%q names %s with its backslashes doubled", text, path)
		}
	}

	t.Run("unreadable record", func(t *testing.T) {
		if err := os.MkdirAll(identitiesDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordPath, []byte("{bad"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(identitiesDir) })
		_, err := s.prepareNodeIdentity(t.Context(), registry.Service{Name: "api", Type: registry.TypeProxy, Target: "http://localhost:3000"})
		var recordErr *nodeIdentityReadError
		if !errors.As(err, &recordErr) {
			t.Fatalf("prepareNodeIdentity() error = %v, want a node identity read error", err)
		}
		failure := nodeIdentityFailure(registry.Service{Name: "api"}, err)
		if failure.Error == nil || len(failure.Error.Next) < 2 {
			t.Fatalf("failure = %+v, want a message and recovery steps", failure)
		}
		for _, text := range append([]string{failure.Error.Message}, failure.Error.Next[:2]...) {
			assertVerbatim(t, text, recordPath)
		}
	})
	t.Run("unsafe identities directory", func(t *testing.T) {
		if err := os.WriteFile(identitiesDir, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(identitiesDir) })
		err := s.removeAbsentNodeIdentities(map[string]registry.Service{})
		if err == nil {
			t.Fatal("sweep over a node-identities file returned no error")
		}
		assertVerbatim(t, err.Error(), identitiesDir)
	})
}
