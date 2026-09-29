package cmd

import (
	"context"
	"reflect"
	"testing"

	"github.com/monody0007/tslink/internal/testenv"
	"github.com/monody0007/tslink/internal/testenv/localapitest"
	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
)

// installRefusingHostSeams replaces, for the whole test binary, every cmd seam
// whose production value reaches a real resource of the machine the tests run
// on. A test that exercises one of these paths installs its own fake; one that
// forgets gets a refusal, and testenv.Main fails the package with its stack.
//
//   - doctorTailscaleSSHFn / doctorLocalClientFn: the local tailscaled
//     (LocalAPI token lookup, then GET /localapi/v0/prefs).
//   - loginNewValidationServerFn: a real tsnet node brought Up against the
//     control plane with the candidate client secret.
//   - serveNewServerFn: server.New, whose nodes are real tsnet nodes (in this
//     binary internal/server's own refusing default is not installed).
//   - loginOpenBrowserFn / serveOpenBrowserFn: open, xdg-open or rundll32.
func installRefusingHostSeams() {
	doctorTailscaleSSHFn = refuseDoctorTailscaleSSH
	doctorLocalClientFn = refuseDoctorLocalClient
	loginNewValidationServerFn = refuseLoginValidationServer
	serveNewServerFn = refuseServeNewServer
	loginOpenBrowserFn = refuseOpenBrowser
	serveOpenBrowserFn = refuseOpenBrowser
}

func refuseDoctorTailscaleSSH(context.Context) (bool, error) {
	return false, testenv.UnfakedHostSeam("cmd.doctorTailscaleSSHFn")
}

func refuseDoctorLocalClient() *local.Client {
	_ = testenv.UnfakedHostSeam("cmd.doctorLocalClientFn")
	return localapitest.NewClient(nil)
}

func refuseLoginValidationServer(string, string, []string) loginValidationServer {
	return refusedValidationServer{err: testenv.UnfakedHostSeam("cmd.loginNewValidationServerFn")}
}

type refusedValidationServer struct{ err error }

func (s refusedValidationServer) Up(context.Context) (*ipnstate.Status, error) { return nil, s.err }
func (s refusedValidationServer) Close() error                                 { return nil }

func refuseServeNewServer(string, string) (serverRunner, error) {
	return nil, testenv.UnfakedHostSeam("cmd.serveNewServerFn")
}

func refuseOpenBrowser(string) error {
	return testenv.UnfakedHostSeam("cmd.openBrowser")
}

// TestHostSeamsRefuseUntilATestFakesThem pins the TestMain defaults: each of
// these seams is the refusing one at the start of every test.
func TestHostSeamsRefuseUntilATestFakesThem(t *testing.T) {
	for _, seam := range []struct {
		name      string
		got, want any
	}{
		{"doctorTailscaleSSHFn", doctorTailscaleSSHFn, refuseDoctorTailscaleSSH},
		{"doctorLocalClientFn", doctorLocalClientFn, refuseDoctorLocalClient},
		{"loginNewValidationServerFn", loginNewValidationServerFn, refuseLoginValidationServer},
		{"serveNewServerFn", serveNewServerFn, refuseServeNewServer},
		{"loginOpenBrowserFn", loginOpenBrowserFn, refuseOpenBrowser},
		{"serveOpenBrowserFn", serveOpenBrowserFn, refuseOpenBrowser},
	} {
		if reflect.ValueOf(seam.got).Pointer() != reflect.ValueOf(seam.want).Pointer() {
			t.Errorf("%s is not its refusing TestMain default at the start of a test", seam.name)
		}
	}
}
