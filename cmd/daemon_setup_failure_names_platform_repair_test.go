package cmd

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

// userManagerRoute is the way out of a missing systemd user manager that
// status gives (B6b-5): a login session that provides one, or a manual serve.
var userManagerRoute = []string{
	"Establish a login session with a working systemd user manager and XDG_RUNTIME_DIR",
	"tslink serve",
	"Use add/share --no-daemon-install to register services for manual serve",
}

// userManagerUnavailableError is the error a Linux bootstrap gets from
// systemdObservation when systemctl cannot reach the user bus.
func userManagerUnavailableError() error {
	return fmt.Errorf("%s: Failed to connect to bus: No medium found (exit status 1); automatic installation requires a login session with a working systemd user manager and XDG_RUNTIME_DIR. In CI or without a user bus, register with --no-daemon-install and run tslink serve manually", systemdUserManagerUnavailableMessage)
}

// assertDaemonSetupFailure checks the stable code and exit of a
// daemon_setup_failed error and returns its next steps.
func assertDaemonSetupFailure(t *testing.T, err error) []string {
	t.Helper()
	if code, _ := registry.ErrorCode(err); code != "daemon_setup_failed" {
		t.Fatalf("code = %q, want daemon_setup_failed: %v", code, err)
	}
	if got := output.ExitCode(err); got != output.ExitError {
		t.Fatalf("exit = %d, want %d: %v", got, output.ExitError, err)
	}
	var next interface{ NextCommands() []string }
	if !errors.As(err, &next) {
		t.Fatalf("no next steps: %v", err)
	}
	return next.NextCommands()
}

// TestDaemonSetupFailureNamesThePlatformRepair is B7-2, the B6b-5 residual. A
// bootstrap that fails for want of a systemd user manager is not repaired by
// installing again: install fails the same way until a login session provides
// one. daemon_setup_failed nevertheless ended every failure, on every platform,
// with "repairing with 'tslink install'" and a tslink install step, the loop
// B6b-5 took out of install, status and doctor. That failure now names the
// route status gives for the same state. Every other failure keeps
// reinstalling as its repair, which install's help on macOS, Linux and Windows
// names as the supported path.
func TestDaemonSetupFailureNamesThePlatformRepair(t *testing.T) {
	isolateBootstrap(t)

	t.Run("missing systemd user manager", func(t *testing.T) {
		err := daemonSetupError(userManagerUnavailableError())
		next := assertDaemonSetupFailure(t, err)
		if want := append([]string{"tslink logs", "tslink doctor"}, userManagerRoute...); !reflect.DeepEqual(next, want) {
			t.Fatalf("next = %q, want %q", next, want)
		}
		if strings.Contains(err.Error(), "repairing with 'tslink install'") || !strings.Contains(err.Error(), "installing again fails the same way until that login session exists") {
			t.Fatalf("message still sends the operator to reinstall: %v", err)
		}
		// The cause and its prerequisite reach the operator unchanged.
		for _, want := range []string{"Failed to connect to bus", "XDG_RUNTIME_DIR", "--no-daemon-install"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message lost %q: %v", want, err)
			}
		}
	})

	t.Run("any other failure", func(t *testing.T) {
		err := daemonSetupError(errors.New("daemon did not settle"))
		next := assertDaemonSetupFailure(t, err)
		if want := []string{"tslink logs", "tslink doctor", "tslink install"}; !reflect.DeepEqual(next, want) {
			t.Fatalf("next = %q, want %q", next, want)
		}
		if !strings.HasSuffix(err.Error(), "Inspect 'tslink logs' and 'tslink doctor' before repairing with 'tslink install'") {
			t.Fatalf("message = %v, want the reinstall repair", err)
		}
	})

	// The same state reads the same way on status.
	t.Run("status names the same route", func(t *testing.T) {
		r := StatusResult{Supervision: unmanagedSupervision(false, "No systemd user unit is installed. "+userManagerUnavailableError().Error()), ServiceCount: 1, AuthStatus: authStatusNotAuthenticated}
		setStatusContinuation(&r)
		if !reflect.DeepEqual(r.Next, userManagerRoute) {
			t.Fatalf("status next = %q, want %q", r.Next, userManagerRoute)
		}
	})
}
