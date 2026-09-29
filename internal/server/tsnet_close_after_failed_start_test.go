package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/ipn/ipnstate"
)

// errEarlyTSNetStart stands for a tsnet start that failed before tsnet built
// its internal state, for example because os.Executable failed (a Linux host
// without a readable /proc/self/exe).
var errEarlyTSNetStart = errors.New("tsnet: readlink /proc/self/exe: no such file or directory")

// panicOnCloseTSNetServer reproduces tailscale.com v1.102.4: Start and Up
// report the early start failure, and tsnet.Server.Close after it dereferences
// a nil s.sys and panics.
type panicOnCloseTSNetServer struct {
	*fakeTSNetServer
	closeCalls int
}

func (s *panicOnCloseTSNetServer) Start() error { return errEarlyTSNetStart }

func (s *panicOnCloseTSNetServer) Up(context.Context) (*ipnstate.Status, error) {
	return nil, fmt.Errorf("tsnet.Up: %w", errEarlyTSNetStart)
}

func (s *panicOnCloseTSNetServer) Close() error {
	s.closeCalls++
	panic("runtime error: invalid memory address or nil pointer dereference (simulated tsnet.Server.close after a failed start)")
}

// TestStartNodeLockedReturnsAnEarlyTSNetStartFailureInsteadOfPanicking: the
// failed start is reported as the service's start error, the node is still
// closed, and the daemon does not crash (and so is not restarted by launchd or
// systemd into the same crash).
func TestStartNodeLockedReturnsAnEarlyTSNetStartFailureInsteadOfPanicking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		authKey string
		wantMsg string
	}{
		// An auth key starts the node through Up.
		{name: "auth key", authKey: "synthetic-auth", wantMsg: `tsnet up for "app"`},
		// Interactive enrollment starts the node through Start.
		{name: "interactive", authKey: "", wantMsg: "app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatalf("EnsureDir() error = %v", err)
			}
			fake := &panicOnCloseTSNetServer{fakeTSNetServer: &fakeTSNetServer{}}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
			t.Cleanup(func() { newTSNetServerFn = oldNew })

			s, err := New(tc.authKey, "")
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			t.Cleanup(s.closeAllNodes)

			err = s.startNodeLocked(context.Background(), registry.Service{Name: "app", Type: registry.TypeFile, Path: t.TempDir()})
			if !errors.Is(err, errEarlyTSNetStart) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("startNodeLocked() error = %v, want the tsnet start failure (%v) naming the service", err, errEarlyTSNetStart)
			}
			if fake.closeCalls != 1 {
				t.Fatalf("tsnet Close calls = %d, want 1: the failed node must still be released", fake.closeCalls)
			}
			if _, ok := s.nodes["app"]; ok {
				t.Fatal("a node whose start failed was kept as running")
			}
		})
	}
}

// TestMCPControlPlaneReturnsAnEarlyTSNetStartFailureInsteadOfPanicking: the
// --mcp control-plane node has the same hazard as a service node. The failed
// start is returned from startMCPControlPlane, the node is still closed, and
// the daemon does not crash.
func TestMCPControlPlaneReturnsAnEarlyTSNetStartFailureInsteadOfPanicking(t *testing.T) {
	for _, tc := range []struct {
		name    string
		authKey string
		wantMsg string
	}{
		// An auth key starts the node through Up.
		{name: "auth key", authKey: "synthetic-auth", wantMsg: "tsnet up for mcp control plane"},
		// Zero-credential enrollment starts the node through Start.
		{name: "interactive", authKey: "", wantMsg: "tsnet start for"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &panicOnCloseTSNetServer{fakeTSNetServer: &fakeTSNetServer{}}
			s, constructed := newMCPControlPlaneTestServer(t, fake)
			s.SetAuthKeyProvider(func(context.Context, registry.Service) (string, error) { return tc.authKey, nil })
			s.SetMCPControlPlane(&MCPControlPlane{
				AllowedUsers: []string{"alice@example.com"},
				Handler:      &mcpProbeHandler{},
			})
			t.Cleanup(s.closeMCPControlPlane)

			err := s.startMCPControlPlane(context.Background())
			if !errors.Is(err, errEarlyTSNetStart) || !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("startMCPControlPlane() error = %v, want the tsnet start failure (%v) from %q", err, errEarlyTSNetStart, tc.wantMsg)
			}
			if *constructed != 1 {
				t.Fatalf("tsnet servers constructed = %d, want 1", *constructed)
			}
			if fake.closeCalls != 1 {
				t.Fatalf("tsnet Close calls = %d, want 1: the failed node must still be released", fake.closeCalls)
			}
			if s.mcpNode != nil {
				t.Fatal("a control-plane node whose start failed was kept as running")
			}
		})
	}
}
