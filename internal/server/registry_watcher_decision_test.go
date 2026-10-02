package server

import (
	"context"
	"os"
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
		s.registryWatchGate <- struct{}{}
		returned := make(chan struct{}, 1000)
		for range cap(returned) {
			go func() {
				s.reconcileWatchedRegistry(ctx, regPath)
				returned <- struct{}{}
			}()
		}
		synctest.Wait()
		count := len(returned)
		cancel()
		synctest.Wait()
		<-s.registryWatchGate
		if count != cap(returned) || s.syncGeneration.Load() != 0 {
			t.Fatalf("callbacks queued behind a decision: returned %d/%d", count, cap(returned))
		}
	})
}
