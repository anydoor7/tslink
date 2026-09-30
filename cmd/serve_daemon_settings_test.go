package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/server"
)

// ignoresDaemonSettings is a fake daemon's visible opt-out from the settings
// serve passes every daemon (daemonSettings). serve refuses a runner that
// lacks any of them, so a fake embeds this to accept and drop the settings its
// test does not look at. A setter the fake defines itself takes precedence.
type ignoresDaemonSettings struct{}

func (ignoresDaemonSettings) SetCredentialed(bool)                                  {}
func (ignoresDaemonSettings) SetControlURLUnverified(bool)                          {}
func (ignoresDaemonSettings) SetEnsureTagsFn(server.EnsureTagsFunc)                 {}
func (ignoresDaemonSettings) SetEnsureFunnelAttrFn(server.EnsureFunnelAttrFunc)     {}
func (ignoresDaemonSettings) SetAutoProvisionFunnel(bool)                           {}
func (ignoresDaemonSettings) SetLifecycleReconcileFn(server.LifecycleReconcileFunc) {}
func (ignoresDaemonSettings) SetAuthKeyProvider(server.AuthKeyProvider)             {}
func (ignoresDaemonSettings) SetUserSuppliedAuthKey(bool)                           {}

var _ daemonSettings = ignoresDaemonSettings{}

// runOnlyServer is a runner that takes none of the daemon settings.
type runOnlyServer struct{ runCalled bool }

func (m *runOnlyServer) Run(context.Context) error {
	m.runCalled = true
	return nil
}

// noLifecycleServer takes every daemon setting except the lifecycle
// reconciler, the one whose absence would leave expired Funnels public.
type noLifecycleServer struct{ runOnlyServer }

func (*noLifecycleServer) SetCredentialed(bool)                              {}
func (*noLifecycleServer) SetControlURLUnverified(bool)                      {}
func (*noLifecycleServer) SetEnsureTagsFn(server.EnsureTagsFunc)             {}
func (*noLifecycleServer) SetEnsureFunnelAttrFn(server.EnsureFunnelAttrFunc) {}
func (*noLifecycleServer) SetAutoProvisionFunnel(bool)                       {}
func (*noLifecycleServer) SetAuthKeyProvider(server.AuthKeyProvider)         {}
func (*noLifecycleServer) SetUserSuppliedAuthKey(bool)                       {}

// serve used to skip a setting the runner did not take and start the daemon
// without it. It now refuses, names what is missing, and never runs it.
func TestServeRefusesARunnerThatDoesNotTakeEveryDaemonSetting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runner  func() (serverRunner, *bool)
		missing []string
	}{
		{name: "no settings at all", runner: func() (serverRunner, *bool) {
			m := &runOnlyServer{}
			return m, &m.runCalled
		}, missing: []string{"SetCredentialed", "SetLifecycleReconcileFn", "SetUserSuppliedAuthKey"}},
		{name: "no lifecycle reconciler", runner: func() (serverRunner, *bool) {
			m := &noLifecycleServer{}
			return m, &m.runCalled
		}, missing: []string{"SetLifecycleReconcileFn"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			runner, runCalled := tc.runner()
			serveNewServerFn = func(string, string) (serverRunner, error) { return runner, nil }

			cmd := findServeCmd(t)
			err := cmd.RunE(cmd, nil)
			if err == nil {
				t.Fatal("serve started a runner that does not take every daemon setting")
			}
			for _, name := range tc.missing {
				if !strings.Contains(err.Error(), name) {
					t.Fatalf("error = %v, want it to name %s", err, name)
				}
			}
			if tc.name == "no lifecycle reconciler" && strings.Contains(err.Error(), "SetCredentialed") {
				t.Fatalf("error = %v, names a setting the runner takes", err)
			}
			if *runCalled {
				t.Fatal("serve ran the daemon without its settings")
			}
		})
	}
}

// Control: a runner that takes every setting starts.
func TestServeStartsARunnerThatTakesEveryDaemonSetting(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	mock := &mockServer{}
	serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
	cmd := findServeCmd(t)
	if err := cmd.RunE(cmd, nil); err != nil || !mock.runCalled {
		t.Fatalf("serve error = %v, run = %v", err, mock.runCalled)
	}
}
