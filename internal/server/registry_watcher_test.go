package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testwait"
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
		case <-time.After(testwait.Budget(t)):
			t.Fatalf("credential transition %q produced no frame", value)
		}
	}
	if count := s.syncGeneration.Load(); count != 0 {
		t.Fatalf("credential transitions triggered %d registry syncs", count)
	}
}

func newLossRecoveryServer(t *testing.T) *Server {
	t.Helper()
	previous := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = previous })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	return s
}

func assertRecoveredWatcherState(t *testing.T, s *Server) {
	t.Helper()
	s.mu.RLock()
	_, stale := s.nodes["stale"]
	s.mu.RUnlock()
	if stale {
		t.Error("dropped final registry write left the removed service running")
	}
	s.credentialStateMu.Lock()
	cached := s.credentialStateDigest
	s.credentialStateMu.Unlock()
	if cached != credentialStateDigest(s.credentialStatePaths()) {
		t.Error("dropped final credential write left the published digest stale")
	}
}

// Port of the independent overflow witness: only the error is delivered after
// real final writes. Ordinary events are a positive control on the same loop.
func TestWatchRegistry_RecoversDroppedFinalWritesOnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"overflow", fsnotify.ErrEventOverflow},
		{"other-error", errors.New("synthetic watcher failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				w := installChannelRegistryWatcher(t)
				s := newLossRecoveryServer(t)
				defer s.closeAllNodes()
				regPath := writeRegistry(t, []registry.Service{{Name: "stale", Type: registry.TypeFile, Path: t.TempDir()}})
				if err := s.syncNodes(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer startRegistryWatcherTest(t, s)()
				generation := s.syncGeneration.Load()
				changed, release := s.events.subscribe()
				defer release()
				writeRegistry(t, nil)
				writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "final-state")
				w.errors <- tc.err
				// No debounce or periodic time has elapsed: errors must schedule
				// reconciliation immediately, rather than wait for another event.
				synctest.Wait()
				assertRecoveredWatcherState(t, s)
				if got := s.syncGeneration.Load(); got != generation+1 {
					t.Errorf("error recovery sync generation = %d, want %d", got, generation+1)
				}
				if !drained(changed) {
					t.Error("error recovery published no state change")
				}

				w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Create}
				w.events <- fsnotify.Event{Name: filepath.Join(s.cfgDir, config.CredentialMetaFileName), Op: fsnotify.Create}
				advanceWatcherTime(credentialStateDebounce)
				assertRecoveredWatcherState(t, s)
				if got := s.syncGeneration.Load(); got != generation+1 {
					t.Errorf("late file events duplicated recovery: generation = %d", got)
				}
				if drained(changed) {
					t.Error("late file events duplicated the recovered state notification")
				}
				t.Log("positive controls passed: ordinary events reconcile both final states")
			})
		})
	}
}

func TestWatchRegistry_PeriodicCheckRecoversCreateWatchGap(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		defer startRegistryWatcherTest(t, s)()
		changed, release := s.events.subscribe()
		defer release()
		regPath := writeRegistry(t, []registry.Service{{Name: "stale", Type: registry.TypeFile, Path: t.TempDir()}})
		credentialPath := filepath.Join(s.cfgDir, config.CredentialMetaFileName)
		writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "first")
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Create}
		w.events <- fsnotify.Event{Name: credentialPath, Op: fsnotify.Create}
		advanceWatcherTime(credentialStateDebounce)
		if !s.nodeRunning("stale") || !drained(changed) {
			t.Fatal("invalid fixture: initial Create callbacks did not apply state")
		}
		generation := s.syncGeneration.Load()
		// Model kqueue's gap after Create was consumed but before the per-file
		// watch exists. Both atomic replacements have no delivered event.
		replaceWatcherFile(t, regPath, []byte("{\"services\":[]}\n"))
		replaceWatcherFile(t, credentialPath, []byte("final"))
		advanceWatcherTime(30 * time.Second)
		assertRecoveredWatcherState(t, s)
		if got := s.syncGeneration.Load(); got != generation+1 {
			t.Errorf("periodic recovery sync generation = %d, want %d", got, generation+1)
		}
		if !drained(changed) {
			t.Error("periodic recovery published no state change")
		}
		// Delayed events and a second tick must use the same applied-state
		// comparison. Credential-only positive control must still be observed.
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Create}
		w.events <- fsnotify.Event{Name: credentialPath, Op: fsnotify.Create}
		advanceWatcherTime(30 * time.Second)
		if got := s.syncGeneration.Load(); got != generation+1 || drained(changed) {
			t.Errorf("unchanged state fired again: generation = %d", got)
		}
		writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "positive-control")
		w.events <- fsnotify.Event{Name: credentialPath, Op: fsnotify.Write}
		advanceWatcherTime(credentialStateDebounce)
		if !drained(changed) {
			t.Fatal("positive control: later credential write produced no notification")
		}
		assertRecoveredWatcherState(t, s)
	})
}

func TestWatchRegistry_UnchangedPeriodicChecksDoNoRemoteWork(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		writeRegistry(t, []registry.Service{{Name: "keep", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:test"}}})
		var tagChecks int
		s.SetEnsureTagsFn(func(context.Context, []string) error {
			tagChecks++
			return nil
		})
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer startRegistryWatcherTest(t, s)()
		changed, release := s.events.subscribe()
		defer release()
		generation := s.syncGeneration.Load()
		advanceWatcherTime(90 * time.Second)
		if got := s.syncGeneration.Load(); got != generation || tagChecks != 1 {
			t.Errorf("unchanged periodic checks did remote work: generation=%d tag checks=%d", got, tagChecks)
		}
		if drained(changed) || !s.nodeRunning("keep") {
			t.Fatal("unchanged periodic checks changed visible state")
		}
	})
}

func TestWatchRegistry_PeriodicCheckSharesInFlightDebounce(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		regPath := writeRegistry(t, []registry.Service{{Name: "stale", Type: registry.TypeFile, Path: t.TempDir()}})
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer startRegistryWatcherTest(t, s)()
		previous := afterDesiredLoadedFn
		loaded, resume := make(chan struct{}, 1), make(chan struct{})
		afterDesiredLoadedFn = func(context.Context, uint64) error {
			select {
			case loaded <- struct{}{}:
			default:
			}
			<-resume
			return nil
		}
		t.Cleanup(func() { afterDesiredLoadedFn = previous })
		writeRegistry(t, nil)
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		<-loaded
		generation := s.syncGeneration.Load()
		writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "dropped-while-syncing")
		advanceWatcherTime(30 * time.Second)
		if got := s.syncGeneration.Load(); got != generation {
			t.Errorf("periodic check duplicated an in-flight sync: generation = %d", got)
		}
		s.credentialStateMu.Lock()
		cached := s.credentialStateDigest
		s.credentialStateMu.Unlock()
		if cached != credentialStateDigest(s.credentialStatePaths()) {
			t.Error("blocked registry sync starved periodic credential recovery")
		}
		close(resume)
		synctest.Wait()
		assertRecoveredWatcherState(t, s)
		if got := s.syncGeneration.Load(); got != generation {
			t.Errorf("periodic callback repeated the completed sync: generation = %d", got)
		}
	})
}

func TestWatchRegistry_PeriodicCheckSurvivesClosedChannels(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		writeRegistry(t, []registry.Service{{Name: "stale", Type: registry.TypeFile, Path: t.TempDir()}})
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer startRegistryWatcherTest(t, s)()
		close(w.events)
		close(w.errors)
		synctest.Wait()
		generation := s.syncGeneration.Load()
		writeRegistry(t, nil)
		writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "after-watcher-closed")
		advanceWatcherTime(30 * time.Second)
		assertRecoveredWatcherState(t, s)
		if got := s.syncGeneration.Load(); got != generation+1 {
			t.Errorf("closed watcher prevented periodic recovery: generation = %d", got)
		}
	})
}

func TestWatchRegistry_PeriodicCheckRetriesFailedSync(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		writeRegistry(t, nil)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer startRegistryWatcherTest(t, s)()
		var attempts int
		s.SetEnsureTagsFn(func(context.Context, []string) error {
			attempts++
			if attempts == 1 {
				return errors.New("synthetic preflight failure")
			}
			return nil
		})
		regPath := writeRegistry(t, []registry.Service{{Name: "retry", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:test"}}})
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		if !s.lastSyncFailed.Load() || s.nodeRunning("retry") {
			t.Fatal("invalid fixture: first sync did not fail")
		}
		advanceWatcherTime(30 * time.Second)
		if s.lastSyncFailed.Load() || !s.nodeRunning("retry") || attempts != 2 {
			t.Errorf("unchanged failed state was not retried: attempts = %d", attempts)
		}
		generation := s.syncGeneration.Load()
		advanceWatcherTime(30 * time.Second)
		if got := s.syncGeneration.Load(); got != generation || attempts != 2 {
			t.Errorf("successful retry was repeated: generation=%d attempts=%d", got, attempts)
		}
	})
}

func TestWatchRegistry_PeriodicCheckAppliesRepairedDecodeIssue(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		regPath := writeRegistry(t, []registry.Service{{Name: "repaired", Type: registry.TypeFile, Path: t.TempDir()}})
		valid, err := os.ReadFile(regPath)
		if err != nil {
			t.Fatal(err)
		}
		invalid := []byte(strings.Replace(string(valid), `"name":"repaired"`, `"unknown_key":true,"name":"repaired"`, 1))
		if err := os.WriteFile(regPath, invalid, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		if s.nodeRunning("repaired") || s.serviceFailures["repaired"].Error == nil {
			t.Fatal("invalid fixture: unknown key did not isolate the service")
		}
		defer startRegistryWatcherTest(t, s)()
		if err := os.WriteFile(regPath, valid, 0o600); err != nil {
			t.Fatal(err)
		}
		advanceWatcherTime(30 * time.Second)
		if !s.nodeRunning("repaired") {
			t.Fatal("repair with unchanged recognized service fields was not applied")
		}
	})
}

func TestWatchRegistry_PeriodicCheckRehashesSameSizeWrites(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		defer s.closeAllNodes()
		regPath := writeRegistry(t, []registry.Service{{Name: "stale", Type: registry.TypeFile, Path: t.TempDir()}})
		credentialPath := filepath.Join(s.cfgDir, config.CredentialMetaFileName)
		writeCredentialFile(t, s.cfgDir, config.CredentialMetaFileName, "first")
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer startRegistryWatcherTest(t, s)()
		data, err := os.ReadFile(regPath)
		if err != nil {
			t.Fatal(err)
		}
		for path, content := range map[string][]byte{
			regPath:        []byte(strings.Replace(string(data), `"stale"`, `"final"`, 1)),
			credentialPath: []byte("final"),
		} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(content)) != info.Size() {
				t.Fatal("invalid fixture: replacement must preserve file size")
			}
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}
		advanceWatcherTime(30 * time.Second)
		assertRecoveredWatcherState(t, s)
		if !s.nodeRunning("final") {
			t.Fatal("same-size registry write with preserved mtime was not applied")
		}
	})
}
