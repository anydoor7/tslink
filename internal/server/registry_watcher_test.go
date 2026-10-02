package server

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/fsnotify/fsnotify"
)

// channelRegistryWatcher controls event delivery without relying on a kernel's
// event count or on how quickly a goroutine is scheduled under the race detector.
type channelRegistryWatcher struct {
	events chan fsnotify.Event
	errors chan error
	add    func(string) error
}

func (w *channelRegistryWatcher) Add(path string) error {
	if w.add != nil {
		return w.add(path)
	}
	return nil
}

func (w *channelRegistryWatcher) Close() error                  { return nil }
func (w *channelRegistryWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *channelRegistryWatcher) Errors() <-chan error          { return w.errors }

func installChannelRegistryWatcher(t *testing.T) *channelRegistryWatcher {
	t.Helper()
	w := &channelRegistryWatcher{events: make(chan fsnotify.Event), errors: make(chan error)}
	previous := newRegistryWatcherFn
	newRegistryWatcherFn = func() (registryWatcher, error) { return w, nil }
	t.Cleanup(func() { newRegistryWatcherFn = previous })
	return w
}

// advanceWatcherTime advances synctest's virtual clock after all event handlers
// have blocked. This never waits for wall time or grants a scheduler margin.
func advanceWatcherTime(d time.Duration) {
	synctest.Wait()
	<-time.After(d)
	synctest.Wait()
}

func TestRegistryWatcherFixtureWaitsForRegistration(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		adding, armed, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
		w.add = func(string) error {
			close(adding)
			<-armed
			return nil
		}
		s := newCredentialStateServer(t)
		go func() {
			startRegistryWatcherTest(t, s)
			close(returned)
		}()
		<-adding
		synctest.Wait()
		var early bool
		select {
		case <-returned:
			early = true
		default:
		}
		close(armed)
		<-returned
		if early {
			t.Fatal("watcher fixture returned before the directory watch was armed")
		}
	})
}

func TestCredentialWatcherFixturePreservesFirstChange(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		adding, armed, written := make(chan struct{}), make(chan struct{}), make(chan struct{})
		w.add = func(string) error {
			close(adding)
			<-armed
			return nil
		}
		s := newCredentialStateServer(t)
		changed, release := s.events.subscribe()
		defer release()
		go func() {
			startRegistryWatcherTest(t, s)
			writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "changed")
			w.events <- fsnotify.Event{
				Name: filepath.Join(s.cfgDir, config.CredentialMetaFileName), Op: fsnotify.Create,
			}
			close(written)
		}()
		<-adding
		// Hold registration until all runnable work has stopped. With the old
		// fixture, the write lands before Add and becomes the primed baseline;
		// even delivering its event later cannot recover that first change.
		synctest.Wait()
		close(armed)
		<-written
		advanceWatcherTime(credentialStateDebounce)
		select {
		case <-changed:
		default:
			t.Fatal("the first credential write was swallowed by watcher startup")
		}
	})
}

func replaceWatcherFile(t *testing.T, path string, content []byte) {
	t.Helper()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".watcher-replace-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		t.Fatalf("replace watched file: %v", err)
	}
}

func TestWatchRegistry_ReactsToAtomicReplacement(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath := writeRegistry(t, nil)
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	startRegistryWatcherTest(t, s)
	// Replacing the same path twice also proves the watch survived replacement
	// of the original inode. Wait for business completion between transactions.
	for i := uint64(1); i <= 2; i++ {
		s.mu.Lock()
		s.nodes["stale"] = newNode(t, registry.Service{Name: "stale"})
		s.mu.Unlock()
		generation := s.syncGeneration.Load() + 1
		replaceWatcherFile(t, regPath, []byte("{\"services\":[]}\n"))
		if result := waitRegistrySyncTest(t, s, generation); result.err != nil {
			t.Fatal(result.err)
		}
		s.mu.RLock()
		_, exists := s.nodes["stale"]
		s.mu.RUnlock()
		if exists {
			t.Fatal("atomic replacement did not remove the stale node")
		}
	}
}

func TestCredentialWatcherObservesReplacementAndRemoval(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.cfgDir, config.CredentialMetaFileName)
	writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "initial")
	changed, release := s.events.subscribe()
	defer release()
	startRegistryWatcherTest(t, s)
	for _, value := range []string{"replacement-one", "replacement-two", ""} {
		if value == "" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		} else {
			replaceWatcherFile(t, path, []byte(value))
		}
		select {
		case <-changed:
		case <-time.After(10 * time.Second):
			t.Fatalf("credential transition %q produced no frame", value)
		}
	}
	if count := s.syncGeneration.Load(); count != 0 {
		t.Fatalf("credential transitions triggered %d registry syncs", count)
	}
}
