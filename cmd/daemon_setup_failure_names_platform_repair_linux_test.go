//go:build linux

package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// On Linux a missing user manager reaches daemon_setup_failed through the
// real bootstrap: with no unit installed, ensureDaemon asks systemctl about
// the unit before it installs anything, and systemctl cannot reach the user
// bus. The failure names the login-session route and nothing is installed
// (isolateBootstrap fails the test on any install).
func TestLinuxBootstrapWithoutAUserBusNamesTheLoginSessionRoute(t *testing.T) {
	isolateBootstrap(t)
	managerOutputFn = func(name string, args ...string) ([]byte, error) {
		if name != "systemctl" {
			t.Fatalf("unexpected manager %s %v", name, args)
		}
		return []byte("Failed to connect to bus: No medium found\n"), errors.New("exit status 1")
	}

	err := ensureDaemon(context.Background(), io.Discard, false)
	next := assertDaemonSetupFailure(t, err)
	if want := append([]string{"tslink logs", "tslink doctor"}, userManagerRoute...); !reflect.DeepEqual(next, want) {
		t.Fatalf("next = %q, want %q", next, want)
	}
	if !strings.Contains(err.Error(), systemdUserManagerUnavailableMessage) || !strings.Contains(err.Error(), "No medium found") {
		t.Fatalf("bootstrap failure lost the user-bus cause: %v", err)
	}
	if strings.Contains(err.Error(), "repairing with 'tslink install'") {
		t.Fatalf("bootstrap failure still sends the operator to reinstall: %v", err)
	}
}
