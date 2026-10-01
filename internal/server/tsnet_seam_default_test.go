package server

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

// refuseRealTSNetServer is newTSNetServerFn in this test binary until a test
// installs its own fake. The production constructor returns a real
// tsnet.Server, which on Start/Up resolves and dials Tailscale's control plane
// and DERP servers and writes node state; a test that forgets its fake must not
// do that. The call is recorded, testenv.Main fails the package with the
// calling test's stack, and every method of the returned server refuses.
func refuseRealTSNetServer(registry.Service, string, string, string) tsnetServer {
	return unfakedTSNetServer{err: testenv.UnfakedHostSeam("internal/server.newTSNetServerFn")}
}

type unfakedTSNetServer struct{ err error }

func (s unfakedTSNetServer) Start() error                                 { return s.err }
func (s unfakedTSNetServer) Up(context.Context) (*ipnstate.Status, error) { return nil, s.err }
func (s unfakedTSNetServer) Listen(string, string) (net.Listener, error)  { return nil, s.err }
func (s unfakedTSNetServer) ListenTLS(string, string) (net.Listener, error) {
	return nil, s.err
}
func (s unfakedTSNetServer) ListenFunnel(string, string, ...tsnet.FunnelOption) (net.Listener, error) {
	return nil, s.err
}
func (s unfakedTSNetServer) LocalClient() (*LocalClient, error) { return nil, s.err }
func (s unfakedTSNetServer) CertDomains() []string              { return nil }
func (s unfakedTSNetServer) Close() error                       { return nil }

// TestNewTSNetServerFnRefusesUntilATestFakesIt pins the TestMain default: a
// test in this package that reaches node startup without a fake gets the
// refusing server, never a real tsnet node.
func TestNewTSNetServerFnRefusesUntilATestFakesIt(t *testing.T) {
	// %p of a func value is its code pointer; this package's tests do not
	// import reflect (TestInternalServerTestsDoNotImportReflectOrUnsafe).
	if got, want := fmt.Sprintf("%p", newTSNetServerFn), fmt.Sprintf("%p", refuseRealTSNetServer); got != want {
		t.Fatal("newTSNetServerFn is not the refusing TestMain default at the start of a test; a test that forgets its fake would start a real tsnet node")
	}
}
