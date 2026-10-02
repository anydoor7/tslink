// Ported from the WP4d review probe active_removal_test.go; assertions and controls retained.
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

func TestReviewPendingEnrollmentMustNotPreventActiveServiceRemoval(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		w := installChannelRegistryWatcher(t)
		fake := &fakeInteractiveTSNetServer{}
		status := &gatedInteractiveStatusClient{ready: make(chan struct{})}
		oldNew, oldStatus := newTSNetServerFn, tsnetStatusClientFn
		newTSNetServerFn = func(svc registry.Service, _, _, _ string) tsnetServer {
			if svc.Name == "pending" {
				return fake
			}
			return &fakeTSNetServer{}
		}
		tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return status, nil }
		defer func() { newTSNetServerFn = oldNew; tsnetStatusClientFn = oldStatus }()
		s, err := New("", "")
		if err != nil {
			t.Fatal(err)
		}
		defer s.closeAllNodes()
		keep := registry.Service{Name: "keep", Type: registry.TypeFile, Path: t.TempDir()}
		s.SetAuthKeyProvider(func(_ context.Context, svc registry.Service) (string, error) {
			if svc.Name == "pending" {
				return "", nil
			}
			return "key", nil
		})
		writeRegistry(t, []registry.Service{keep})
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
		path := writeRegistry(t, []registry.Service{keep, {Name: "pending", Type: registry.TypeFile, Path: t.TempDir()}})
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(200 * time.Millisecond)
		select {
		case <-handoff:
		default:
			t.Fatal("invalid probe: enrollment never started")
		}
		if !s.nodeRunning("keep") {
			t.Fatal("invalid probe: initial active service not running")
		}
		generation := s.syncGeneration.Load()
		writeRegistry(t, nil)
		w.events <- fsnotify.Event{Name: path, Op: fsnotify.Write}
		advanceWatcherTime(90 * time.Second)
		if fake.closeCount.Load() != 1 || s.syncGeneration.Load() <= generation {
			t.Errorf("removed interactive enrollment still pending after deletion event and three ticks: generation=%d want >%d; Close=%d want 1", s.syncGeneration.Load(), generation, fake.closeCount.Load())
		}
		if s.nodeRunning("keep") {
			t.Error("removed active service is still running behind the blocked interactive enrollment")
		}
		// Positive control: existing generation machinery can cancel the same wait.
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if fake.closeCount.Load() != 1 || (s.nodeRunning("pending") || s.nodeRunning("keep")) {
			t.Fatal("invalid probe: direct superseding sync did not close the removed enrollment")
		}
		t.Log("direct superseding sync closed removed enrollment; watcher/lifecycle shutdown joins complete")
	})
}
