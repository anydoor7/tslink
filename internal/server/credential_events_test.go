package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/testenv"
)

// newCredentialStateServer builds a Server whose config directory is a scratch
// directory, which is the only state these tests need.
func newCredentialStateServer(t *testing.T) *Server {
	t.Helper()
	return &Server{cfgDir: t.TempDir(), events: newEventHub()}
}

func writeCredentialFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func drained(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestCredentialStateChangeNotifiesSubscribers is the F3 contract in its three
// directions at once: a change notifies, a re-observation of the same state
// does not, and a removal notifies.
//
// The middle case is the one that needs asserting. "Publish on every event"
// would satisfy the first and third clauses and would push a frame to every
// open stream each time any of these files is touched, including the rewrites
// that carry no change.
func TestCredentialStateChangeNotifiesSubscribers(t *testing.T) {
	srv := newCredentialStateServer(t)
	changed, release := srv.events.subscribe()
	defer release()

	srv.primeCredentialState()
	if drained(changed) {
		t.Fatal("priming the credential state published a frame")
	}

	writeCredentialFile(t, srv.cfgDir, config.CredentialMetaFileName, `{"slots":{"api_key":{}}}`)
	if !srv.notifyCredentialStateChanged() {
		t.Fatal("storing a credential did not publish")
	}
	if !drained(changed) {
		t.Fatal("storing a credential did not reach subscribers")
	}

	if srv.notifyCredentialStateChanged() {
		t.Fatal("an unchanged credential state published a frame")
	}
	if drained(changed) {
		t.Fatal("an unchanged credential state reached subscribers")
	}

	// Rewriting the identical bytes is what an atomic save looks like to a
	// watcher when nothing actually changed.
	writeCredentialFile(t, srv.cfgDir, config.CredentialMetaFileName, `{"slots":{"api_key":{}}}`)
	if srv.notifyCredentialStateChanged() {
		t.Fatal("rewriting identical content published a frame")
	}

	if err := os.Remove(filepath.Join(srv.cfgDir, config.CredentialMetaFileName)); err != nil {
		t.Fatalf("remove credential metadata: %v", err)
	}
	if !srv.notifyCredentialStateChanged() {
		t.Fatal("removing a credential did not publish")
	}
	if !drained(changed) {
		t.Fatal("removing a credential did not reach subscribers")
	}
}

// TestCredentialStateCoversEveryCredentialFile walks each watched file
// independently. A digest that folded them together — or a path set that
// silently lost one — would still pass a test that only ever wrote one of them.
func TestCredentialStateCoversEveryCredentialFile(t *testing.T) {
	for _, name := range []string{
		config.AuthHandoffFileName,
		config.APIKeyFileName,
		config.ClientSecretFileName,
		config.CredentialMetaFileName,
	} {
		t.Run(name, func(t *testing.T) {
			srv := newCredentialStateServer(t)
			srv.primeCredentialState()
			writeCredentialFile(t, srv.cfgDir, name, "value-one")
			if !srv.notifyCredentialStateChanged() {
				t.Fatalf("creating %s did not publish", name)
			}
			writeCredentialFile(t, srv.cfgDir, name, "value-two")
			if !srv.notifyCredentialStateChanged() {
				t.Fatalf("changing %s did not publish", name)
			}
			if srv.notifyCredentialStateChanged() {
				t.Fatalf("re-reading an unchanged %s published", name)
			}
		})
	}
}

// TestCredentialStateExcludesTheRuntimeSnapshot is the anti-double-publish
// control. runtime.json already publishes from writeRuntimeSnapshotLocked; if
// it were also watched here, every runtime change would push two frames.
func TestCredentialStateExcludesTheRuntimeSnapshot(t *testing.T) {
	srv := newCredentialStateServer(t)
	paths := srv.credentialStatePaths()
	if _, watched := paths[filepath.Join(srv.cfgDir, "runtime.json")]; watched {
		t.Fatal("runtime.json is in the credential path set; it would publish twice per change")
	}
	if _, watched := paths[filepath.Join(srv.cfgDir, "registry.json")]; watched {
		t.Fatal("registry.json is in the credential path set; it would publish twice per change")
	}
	// Control: the set is not simply empty.
	if _, watched := paths[filepath.Join(srv.cfgDir, config.AuthHandoffFileName)]; !watched {
		t.Fatal("the credential path set does not contain the auth handoff file")
	}
}

// TestCredentialStateDigestDistinguishesAbsenceFromEmptiness keeps logout
// visible. An empty file and no file are different states, and a digest that
// conflated them would drop the transition a client most needs.
func TestCredentialStateDigestDistinguishesAbsenceFromEmptiness(t *testing.T) {
	srv := newCredentialStateServer(t)
	paths := srv.credentialStatePaths()
	absent := credentialStateDigest(paths)

	writeCredentialFile(t, srv.cfgDir, config.APIKeyFileName, "")
	empty := credentialStateDigest(paths)
	if empty == absent {
		t.Fatal("an empty credential file hashes the same as an absent one")
	}

	writeCredentialFile(t, srv.cfgDir, config.APIKeyFileName, "x")
	present := credentialStateDigest(paths)
	if present == empty || present == absent {
		t.Fatal("a populated credential file hashes the same as an empty or absent one")
	}
}

// TestCredentialFileChangeNotifiesThroughTheWatcher is the wiring proof: a real
// fsnotify watcher over the real config directory, with the file written the
// way `tslink login` writes it — from outside this process's control flow.
//
// It also pins that the credential branch does not run a registry sync: the
// two live in the same event loop, and routing a credential write into
// syncNodes would be a far worse bug than the one being fixed.
func TestCredentialFileChangeNotifiesThroughTheWatcher(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	cfgDir, err := config.Dir()
	if err != nil {
		t.Fatalf("config.Dir() error = %v", err)
	}

	srv, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	changed, release := srv.events.subscribe()
	defer release()

	startRegistryWatcherTest(t, srv)

	writeCredentialFile(t, cfgDir, config.CredentialMetaFileName, `{"slots":{"api_key":{"fingerprint":"abc"}}}`)

	select {
	case <-changed:
	case <-time.After(10 * time.Second):
		t.Fatal("a credential metadata write produced no event frame")
	}

	if count := srv.syncGeneration.Load(); count != 0 {
		t.Fatalf("a credential write triggered %d registry syncs", count)
	}
}
