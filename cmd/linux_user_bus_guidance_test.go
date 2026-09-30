//go:build linux

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
)

func TestLinuxUserBusFailurePreservesActionableBoundedStderr(t *testing.T) {
	installLinuxUnitFixture(t, "", "no\n", nil)
	managerOutputFn = func(string, ...string) ([]byte, error) {
		return []byte("Failed to connect to user scope bus: No such file or directory\n" + strings.Repeat("x", 8000)), errors.New("exit status 1")
	}
	_, err := systemdObservation()
	if err == nil {
		t.Fatal("missing bus must be refused")
	}
	for _, want := range []string{"systemd user manager unavailable", "Failed to connect to user scope bus", "login session", "XDG_RUNTIME_DIR", "tslink serve", "--no-daemon-install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing bus error lost %q: %.600s", want, err.Error())
		}
	}
	if len(err.Error()) > 5000 {
		t.Fatalf("systemctl diagnostic is unbounded: %d bytes", len(err.Error()))
	}
}

func TestLinuxUserBusFailureInstallStatusAndDoctorGuidance(t *testing.T) {
	for _, installed := range []bool{false, true} {
		name := "absent unit"
		if installed {
			name = "installed unit"
		}
		t.Run(name, func(t *testing.T) {
			unit := installLinuxUnitFixture(t, "", "no\n", nil)
			if !installed {
				if err := os.Remove(unit); err != nil {
					t.Fatal(err)
				}
			}
			pid := filepath.Join(t.TempDir(), "tslink.pid")
			oldPID, oldRunning := pidPathFn, isRunningFn
			t.Cleanup(func() { pidPathFn, isRunningFn = oldPID, oldRunning })
			pidPathFn = func() (string, error) { return pid, nil }
			isRunningFn = func(string) bool { return false }
			err := detectInstallDaemonConflict("retry install")
			if output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "--no-daemon-install") {
				t.Fatalf("install must refuse with a manual route: %v", err)
			}
			r := StatusResult{Supervision: detectSupervision(pid, false, 0), ServiceCount: 1, AuthStatus: authStatusNotAuthenticated}
			setStatusContinuation(&r)
			var out bytes.Buffer
			formatStatus(r, &out)
			if !strings.Contains(out.String(), "XDG_RUNTIME_DIR") || !strings.Contains(out.String(), "tslink serve") || strings.Contains(out.String(), "Next: tslink install") {
				t.Fatalf("status must explain prerequisite instead of immediate retry: %s", out.String())
			}
			for _, next := range r.Next {
				if next == "tslink install" {
					t.Fatalf("status next loops on unavailable manager: %v", r.Next)
				}
			}
			d := DoctorResult{Paths: DoctorPaths{PID: pid}}
			diagnoseDaemon(&d, 1)
			if len(d.Findings) != 1 || !strings.Contains(d.Findings[0].Message, "XDG_RUNTIME_DIR") {
				t.Fatalf("doctor lost user-bus prerequisite: %+v", d.Findings)
			}
		})
	}
}
