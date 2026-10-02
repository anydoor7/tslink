// Ported from the WP4d review probe long_sync_test.go; assertions and controls retained.
package server

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/fsnotify/fsnotify"
)

func TestReviewWatcherCancelsRemovedInteractiveEnrollment(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		fake := &fakeInteractiveTSNetServer{}
		status := &gatedInteractiveStatusClient{ready: make(chan struct{})}
		oldNew, oldStatus := newTSNetServerFn, tsnetStatusClientFn
		newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
		tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return status, nil }
		defer func() { newTSNetServerFn = oldNew; tsnetStatusClientFn = oldStatus }()
		s, err := New("", "")
		if err != nil {
			t.Fatal(err)
		}
		defer s.closeAllNodes()
		writeRegistry(t, nil)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		// CLI's lifecycle callback returns false for an ordinary external edit.
		s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) { return false, nil })
		ctx, cancel := context.WithCancel(context.Background())
		done, err := s.startRegistryWatcher(ctx)
		if err != nil {
			t.Fatal(err)
		}
		lifecycleDone := s.startLifecycleTicker(ctx)
		defer func() { cancel(); <-done; <-lifecycleDone }()
		handoff := make(chan struct{}, 1)
		s.SetAuthHandoffFunc(func(context.Context, AuthHandoff) error { handoff <- struct{}{}; return nil })
		path := writeRegistry(t, []registry.Service{{Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		select {
		case <-handoff:
		default:
			t.Fatal("invalid probe: enrollment never started")
		}
		generation := s.syncGeneration.Load()
		writeRegistry(t, nil)
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(90 * time.Second)
		if fake.closeCount.Load() != 1 || s.syncGeneration.Load() <= generation {
			t.Errorf("removed interactive enrollment still pending after deletion event and three ticks: generation=%d want >%d; Close=%d want 1", s.syncGeneration.Load(), generation, fake.closeCount.Load())
		}
		// Positive control: existing generation machinery can cancel the same wait.
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if fake.closeCount.Load() != 1 || s.nodeRunning("pending") {
			t.Fatal("invalid probe: direct superseding sync did not close the removed enrollment")
		}
		t.Log("direct superseding sync closed removed enrollment; watcher/lifecycle shutdown joins complete")
	})
}

func TestReviewWatcherCancellationJoinsQueuedChecks(t *testing.T) {
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
		old := afterDesiredLoadedFn
		defer func() { afterDesiredLoadedFn = old }()
		entered := make(chan struct{}, 1)
		afterDesiredLoadedFn = func(ctx context.Context, _ uint64) error { entered <- struct{}{}; <-ctx.Done(); return ctx.Err() }
		ctx, cancel := context.WithCancel(context.Background())
		done, err := s.startRegistryWatcher(ctx)
		if err != nil {
			t.Fatal(err)
		}
		path := writeRegistry(t, []registry.Service{{Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		<-entered
		advanceWatcherTime(90 * time.Second)
		cancel()
		synctest.Wait()
		select {
		case <-done:
		default:
			t.Fatal("watcher did not join active sync and queued periodic checks after cancellation")
		}
	})
}
