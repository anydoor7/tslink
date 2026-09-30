package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/lifecycle"
	"github.com/monody0007/tslink/internal/server"
)

// mockServerHoldingNodeState is a runner that can answer which node state it
// holds, the way server.Server does.
type mockServerHoldingNodeState struct {
	reconcile server.LifecycleReconcileFunc
	runAt     time.Time
	held      map[string]bool
	lockCalls int
}

func (m *mockServerHoldingNodeState) SetLifecycleReconcileFn(fn server.LifecycleReconcileFunc) {
	m.reconcile = fn
}

func (m *mockServerHoldingNodeState) HoldsNodeState(name string) bool { return m.held[name] }

func (m *mockServerHoldingNodeState) WithNodeStateLock(fn func() error) error {
	m.lockCalls++
	return fn()
}

func (m *mockServerHoldingNodeState) TryWithNodeStateLock(ctx context.Context, fn func() error) (bool, error) {
	if ctx.Err() != nil {
		return false, nil
	}
	m.lockCalls++
	return true, fn()
}

func (m *mockServerHoldingNodeState) Run(ctx context.Context) error {
	if m.reconcile == nil {
		return fmt.Errorf("lifecycle reconciler was not set")
	}
	_, err := m.reconcile(ctx, m.runAt)
	return err
}

type mockServerHoldingWithoutGate struct{ *mockServerWithLifecycle }

func (m *mockServerHoldingWithoutGate) HoldsNodeState(string) bool { return false }

// TestServeEnablesLocalNodeStateCleanupOnlyForARunnerThatCanAnswer pins the
// holder and startup-gate opt-in. Missing either leaves local cleanup off.
func TestServeEnablesLocalNodeStateCleanupOnlyForARunnerThatCanAnswer(t *testing.T) {
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)

	t.Run("runner answers", func(t *testing.T) {
		dir := t.TempDir()
		mockServeDefaults(t, dir)
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		mock := &mockServerHoldingNodeState{runAt: now, held: map[string]bool{"busy": true}}
		serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
		var got lifecycle.Options
		serveLifecycleReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
			got = options
			return lifecycle.Result{}, nil
		}
		if err := runForegroundWithOptions(filepath.Join(dir, "test.pid"), "fake-key", "", foregroundOptions{Credentialed: true}); err != nil {
			t.Fatal(err)
		}
		if !got.CleanLocalNodeState {
			t.Fatal("CleanLocalNodeState = false, want the daemon to enable local node-state cleanup")
		}
		if got.LocalNodeStateInUse == nil {
			t.Fatal("LocalNodeStateInUse = nil, want the runner's own answer wired through")
		}
		if !got.LocalNodeStateInUse("busy") {
			t.Fatal("LocalNodeStateInUse(\"busy\") = false, want the runner's answer, not a constant")
		}
		if got.LocalNodeStateInUse("idle") {
			t.Fatal("LocalNodeStateInUse(\"idle\") = true, want the runner's answer, not a constant")
		}
		if got.WithNodeStateLock == nil {
			t.Fatal("WithNodeStateLock = nil, want startup synchronization wired")
		}
		called := false
		if err := got.WithNodeStateLock(func() error { called = true; return nil }); err != nil || !called || mock.lockCalls != 1 {
			t.Fatalf("node-state synchronization not invoked: error=%v called=%v lockCalls=%d", err, called, mock.lockCalls)
		}
		if got.TryWithNodeStateLock == nil {
			t.Fatal("TryWithNodeStateLock = nil, want nonblocking startup synchronization wired")
		}
		called = false
		acquired, err := got.TryWithNodeStateLock(context.Background(), func() error { called = true; return nil })
		if err != nil || !acquired || !called || mock.lockCalls != 2 {
			t.Fatalf("nonblocking node-state synchronization not invoked: acquired=%v error=%v called=%v lockCalls=%d", acquired, err, called, mock.lockCalls)
		}
	})

	t.Run("runner holds state but cannot synchronize startup", func(t *testing.T) {
		dir := t.TempDir()
		mockServeDefaults(t, dir)
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		mock := &mockServerHoldingWithoutGate{&mockServerWithLifecycle{runAt: now}}
		serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
		var got lifecycle.Options
		serveLifecycleReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
			got = options
			return lifecycle.Result{}, nil
		}
		if err := runForegroundWithOptions(filepath.Join(dir, "test.pid"), "fake-key", "", foregroundOptions{Credentialed: true}); err != nil {
			t.Fatal(err)
		}
		if got.CleanLocalNodeState || got.LocalNodeStateInUse != nil || got.WithNodeStateLock != nil || got.TryWithNodeStateLock != nil {
			t.Fatalf("unsafe local cleanup enabled without a startup gate: %+v", got)
		}
	})

	t.Run("runner cannot answer", func(t *testing.T) {
		dir := t.TempDir()
		mockServeDefaults(t, dir)
		if err := config.EnsureDir(); err != nil {
			t.Fatal(err)
		}
		mock := &mockServerWithLifecycle{runAt: now}
		serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
		var got lifecycle.Options
		serveLifecycleReconcileFn = func(_ context.Context, options lifecycle.Options) (lifecycle.Result, error) {
			got = options
			return lifecycle.Result{}, nil
		}
		if err := runForegroundWithOptions(filepath.Join(dir, "test.pid"), "fake-key", "", foregroundOptions{Credentialed: true}); err != nil {
			t.Fatal(err)
		}
		if got.CleanLocalNodeState {
			t.Fatal("CleanLocalNodeState = true for a runner that cannot report held node state")
		}
		if got.LocalNodeStateInUse != nil {
			t.Fatal("LocalNodeStateInUse != nil for a runner that cannot report held node state")
		}
		if got.WithNodeStateLock != nil {
			t.Fatal("WithNodeStateLock != nil for a runner without node-state ownership")
		}
	})
}
