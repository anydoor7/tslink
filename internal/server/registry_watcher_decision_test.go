package server

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/fsnotify/fsnotify"
)

func TestWatchRegistry_RevertingToAppliedStateSupersedesPendingTarget(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		regPath := writeRegistry(t, nil)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		appliedFile := s.appliedWatchFile
		appliedFingerprint := s.appliedWatchFingerprint
		blockedGeneration := s.syncGeneration.Load() + 1
		previous := afterDesiredLoadedFn
		defer func() { afterDesiredLoadedFn = previous }()
		loaded := make(chan struct{})
		afterDesiredLoadedFn = func(ctx context.Context, generation uint64) error {
			if generation == blockedGeneration {
				close(loaded)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		done, err := s.startRegistryWatcher(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); <-done }()
		writeRegistry(t, []registry.Service{{Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		<-loaded

		writeRegistry(t, nil)
		if err := os.Chtimes(regPath, appliedFile.ModTime(), appliedFile.ModTime()); err != nil {
			t.Fatal(err)
		}
		revertedFile, err := os.Stat(regPath)
		if err != nil || !sameWatchedRegistryFile(appliedFile, revertedFile) {
			t.Fatalf("invalid fixture: reverted file does not match applied identity: %v", err)
		}
		w.events <- fsnotify.Event{Name: regPath, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		if s.syncGeneration.Load() != blockedGeneration+1 || s.inFlightWatchTarget != nil {
			t.Fatal("reverting to the applied state did not cancel and finish the pending generation")
		}
		if s.appliedWatchFingerprint != appliedFingerprint || s.nodeRunning("pending") {
			t.Fatal("reverted state was not applied")
		}
		advanceWatcherTime(90 * time.Second)
		if s.syncGeneration.Load() != blockedGeneration+1 {
			t.Fatal("periodic checks duplicated the completed reverted state")
		}
	})
}

func TestWatchRegistry_DelayedOlderDecisionCannotCancelNewTarget(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		s := newLossRecoveryServer(t)
		regPath := writeRegistry(t, nil)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		writeRegistry(t, []registry.Service{{Name: "older", Type: registry.TypeFile, Path: t.TempDir()}})
		older := s.decideWatchedRegistry(context.Background(), regPath)
		writeRegistry(t, []registry.Service{{Name: "newer", Type: registry.TypeFile, Path: t.TempDir()}})
		newer := s.decideWatchedRegistry(context.Background(), regPath)
		if older == nil || newer == nil || newer.generation <= older.generation {
			t.Fatal("invalid fixture: two changed states did not reserve ordered generations")
		}
		previous := afterDesiredLoadedFn
		defer func() { afterDesiredLoadedFn = previous }()
		loaded, resume := make(chan struct{}), make(chan struct{})
		afterDesiredLoadedFn = func(_ context.Context, generation uint64) error {
			if generation == newer.generation {
				close(loaded)
				<-resume
			}
			return nil
		}
		done := make(chan struct{})
		go func() { s.syncWatchedRegistryTarget(context.Background(), newer); close(done) }()
		<-loaded
		// Start the older decision only after the newer generation is installed.
		s.syncWatchedRegistryTarget(context.Background(), older)
		if s.inFlightWatchTarget != newer || s.syncGeneration.Load() != newer.generation {
			t.Error("older callback superseded or cleared the newer target")
		}
		for range 1000 {
			s.reconcileWatchedRegistry(context.Background(), regPath)
		}
		if s.syncGeneration.Load() != newer.generation {
			t.Error("identical callbacks duplicated the blocked newer generation")
		}
		close(resume)
		<-done
		if !s.nodeRunning("newer") || s.nodeRunning("older") || s.inFlightWatchTarget != nil {
			t.Fatal("newer target did not finish successfully")
		}
		s.reconcileWatchedRegistry(context.Background(), regPath)
		if s.syncGeneration.Load() != newer.generation {
			t.Fatal("completed newer target was applied twice")
		}
	})
}

func TestWatchRegistry_OverlappingDecisionsDoNotQueueCallbacks(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		s := newLossRecoveryServer(t)
		regPath := writeRegistry(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Hold the local decision gate. Every competing callback must return
		// before it is released, even with a noncancelled context.
		s.registryWatchGate.Store(registryWatchDecisionOwned)
		returned := make(chan struct{}, 1000)
		for range cap(returned) {
			go func() {
				s.reconcileWatchedRegistry(ctx, regPath)
				returned <- struct{}{}
			}()
		}
		synctest.Wait()
		count := len(returned)
		pending := s.registryWatchGate.Load()
		cancel()
		synctest.Wait()
		s.registryWatchGate.Store(0)
		if count != cap(returned) || s.syncGeneration.Load() != 0 {
			t.Fatalf("callbacks queued behind a decision: returned %d/%d", count, cap(returned))
		}
		if pending != registryWatchDecisionOwned|registryWatchDecisionDirty {
			t.Fatalf("busy callbacks did not retain one pending recheck: state %d", pending)
		}
	})
}

// Ported from wp4d2-review-astra/probes/latency_test.go. The loader seam
// pauses only after the real read; production timers and registry data are used.
func TestReviewOrdinaryEditDuringDecisionIsPrompt(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		svc := registry.Service{Name: "active", Type: registry.TypeFile, Path: t.TempDir()}
		path := writeRegistry(t, []registry.Service{svc})
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !s.nodeRunning("active") {
			t.Fatal("invalid fixture: service is not running")
		}
		entered, resume := make(chan struct{}), make(chan struct{})
		var release sync.Once
		unblock := func() { release.Do(func() { close(resume) }) }
		var first atomic.Bool
		old := registryWatchLoadFn
		registryWatchLoadFn = func(path string) (*registry.Registry, []registry.ServiceIssue, error) {
			reg, issues, err := old(path)
			if first.CompareAndSwap(false, true) {
				close(entered)
				<-resume
			}
			return reg, issues, err
		}
		defer func() { registryWatchLoadFn = old }()
		ctx, cancel := context.WithCancel(context.Background())
		done, err := s.startRegistryWatcher(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); unblock(); <-done }()
		// Pause an unchanged periodic decision after its REAL registry read.
		advanceWatcherTime(30 * time.Second)
		<-entered
		advanceWatcherTime(time.Millisecond)
		editAt := time.Now()
		writeRegistry(t, nil)
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		if !s.nodeRunning("active") {
			t.Fatal("invalid fixture: old decision not isolated")
		}
		unblock()
		synctest.Wait()
		stale := s.nodeRunning("active")
		t.Logf("after releasing decision at edit+%s: removed service running=%t", time.Since(editAt), stale)
		if stale {
			advanceWatcherTime(29798 * time.Millisecond)
			if !s.nodeRunning("active") {
				t.Fatal("unexpected early state recovery")
			}
			advanceWatcherTime(time.Millisecond)
		}
		if s.nodeRunning("active") {
			t.Fatal("periodic recovery did not remove service")
		}
		t.Logf("removal latency=%s; edit was ordinary Write, no lost kernel event", time.Since(editAt))
		// Positive control: the same delivery path applies another edit in 200ms.
		controlAt := time.Now()
		writeRegistry(t, []registry.Service{svc})
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		if !s.nodeRunning("active") {
			t.Fatal("invalid fixture: ordinary-event control failed")
		}
		t.Logf("ordinary control latency=%s", time.Since(controlAt))
		if stale {
			t.Error("different-state callback was discarded; no immediate recheck after local decision completed")
		}
	})
}

func TestWatchRegistry_BusyDecisionCoalescesOneFreshRead(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		s := newLossRecoveryServer(t)
		regPath := writeRegistry(t, nil)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		generation := s.syncGeneration.Load()
		writeRegistry(t, []registry.Service{{Name: "fresh", Type: registry.TypeFile, Path: t.TempDir()}})
		entered, resume := make(chan struct{}), make(chan struct{})
		var reads atomic.Int32
		old := registryWatchLoadFn
		registryWatchLoadFn = func(path string) (*registry.Registry, []registry.ServiceIssue, error) {
			reg, issues, err := old(path)
			if reads.Add(1) == 1 {
				close(entered)
				<-resume
			}
			return reg, issues, err
		}
		defer func() { registryWatchLoadFn = old }()
		decided := make(chan *watchedRegistryTarget, 1)
		go func() { decided <- s.decideWatchedRegistry(context.Background(), regPath) }()
		<-entered
		returned := make(chan struct{}, 1000)
		for range cap(returned) {
			go func() {
				s.reconcileWatchedRegistry(context.Background(), regPath)
				returned <- struct{}{}
			}()
		}
		synctest.Wait()
		count := len(returned)
		close(resume)
		target := <-decided
		if count != cap(returned) {
			t.Fatalf("busy callbacks queued: returned %d/%d", count, cap(returned))
		}
		if reads.Load() != 2 || target == nil || target.generation != generation+1 {
			t.Fatalf("expected one fresh read and the original changed target: reads=%d target=%+v", reads.Load(), target)
		}
		s.syncWatchedRegistryTarget(context.Background(), target)
		if !s.nodeRunning("fresh") {
			t.Fatal("the unchanged recheck discarded the earlier changed decision")
		}
		s.reconcileWatchedRegistry(context.Background(), regPath)
		if s.syncGeneration.Load() != target.generation {
			t.Fatal("completed target was applied twice")
		}
	})
}

func TestWatchRegistry_PendingRecheckPrecedesRemoteSync(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		s := newLossRecoveryServer(t)
		active := registry.Service{Name: "active", Type: registry.TypeFile, Path: t.TempDir()}
		path := writeRegistry(t, []registry.Service{active})
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		olderGeneration := s.syncGeneration.Load() + 1
		entered, resume := make(chan struct{}), make(chan struct{})
		var first atomic.Bool
		var release sync.Once
		unblock := func() { release.Do(func() { close(resume) }) }
		oldLoad, oldDesired := registryWatchLoadFn, afterDesiredLoadedFn
		registryWatchLoadFn = func(path string) (*registry.Registry, []registry.ServiceIssue, error) {
			reg, issues, err := oldLoad(path)
			if first.CompareAndSwap(false, true) {
				close(entered)
				<-resume
			}
			return reg, issues, err
		}
		remoteStarted := make(chan struct{}, 1)
		afterDesiredLoadedFn = func(ctx context.Context, generation uint64) error {
			if generation == olderGeneration {
				remoteStarted <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		defer func() { registryWatchLoadFn, afterDesiredLoadedFn = oldLoad, oldDesired }()
		// The owner sees a changed target whose sync would wait indefinitely.
		writeRegistry(t, []registry.Service{active, {Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})
		ctx, cancel := context.WithCancel(context.Background())
		done, err := s.startRegistryWatcher(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { cancel(); unblock(); <-done }()
		advanceWatcherTime(30 * time.Second)
		<-entered
		editAt := time.Now()
		writeRegistry(t, nil)
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		unblock()
		synctest.Wait()
		if s.nodeRunning("active") || s.nodeRunning("pending") {
			t.Fatal("pending recheck did not remove the services before remote sync")
		}
		if len(remoteStarted) != 0 || s.syncGeneration.Load() != olderGeneration+1 {
			t.Fatal("remote sync started before the fresh decision superseded its generation")
		}
		t.Logf("changed-owner removal latency=%s; obsolete remote sync was never entered", time.Since(editAt))
	})
}
