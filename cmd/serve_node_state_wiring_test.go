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
	ignoresDaemonSettings
	reconcile server.LifecycleReconcileFunc
	runAt     time.Time
	held      map[string]bool
}

func (m *mockServerHoldingNodeState) SetLifecycleReconcileFn(fn server.LifecycleReconcileFunc) {
	m.reconcile = fn
}

func (m *mockServerHoldingNodeState) HoldsNodeState(name string) bool { return m.held[name] }

func (m *mockServerHoldingNodeState) Run(ctx context.Context) error {
	if m.reconcile == nil {
		return fmt.Errorf("lifecycle reconciler was not set")
	}
	_, err := m.reconcile(ctx, m.runAt)
	return err
}

// TestServeEnablesLocalNodeStateCleanupOnlyForARunnerThatCanAnswer pins the
// opt-in. A runner that cannot say which state directories it holds gets no
// cleanup, rather than a cleanup running against an assumed answer: the cost of
// assuming "nothing holds it" is a live node losing its identity.
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
	})
}
