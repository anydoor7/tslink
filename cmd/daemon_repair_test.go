package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

func TestRepairLiveDaemonRequiresSupervision(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Supervision
		ok   bool
	}{
		{"manual", Supervision{Manager: "manual"}, false},
		{"none", Supervision{Manager: "none"}, false},
		{"unknown", Supervision{Manager: "other", Installed: true, Autostart: true, RestartOnExit: true}, false},
		{"uninstalled", Supervision{Manager: "launchd", Autostart: true, RestartOnExit: true}, false},
		{"disabled", Supervision{Manager: "launchd", Installed: true, RestartOnExit: true}, false},
		{"no_restart", Supervision{Manager: "systemd", Installed: true, Autostart: true}, false},
		{"launchd_owned", Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true}, true},
		{"systemd_owned", Supervision{Manager: "systemd", Installed: true, Autostart: true, RestartOnExit: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBootstrap(t)
			isRunningFn = func(string) bool { return true }
			inspected := 0
			detectSupervisionFn = func(string, bool, int) Supervision { inspected++; return tc.s }
			err := ensureDaemon(context.Background(), io.Discard, false)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var coded registry.CodedError
				if !errors.As(err, &coded) || coded.Code != "daemon_supervision_unverified" {
					t.Fatalf("unsafe live daemon accepted or wrong recovery: %v", err)
				}
				for _, action := range []string{"tslink doctor", "tslink stop", "tslink install"} {
					if !strings.Contains(err.Error(), action) {
						t.Errorf("missing recovery %q: %v", action, err)
					}
				}
			}
			if inspected != 1 {
				t.Errorf("ownership inspections=%d, want 1", inspected)
			}
		})
	}
}

func TestRepairOptOutDoesNotClaimSupervision(t *testing.T) {
	isolateBootstrap(t)
	isRunningFn = func(string) bool { return true }
	detectSupervisionFn = func(string, bool, int) Supervision { t.Fatal("opt-out inspected supervisor"); return Supervision{} }
	if err := ensureDaemon(context.Background(), io.Discard, true); err != nil {
		t.Fatal(err)
	}
}

func TestRepairLiveDaemonRejectsUnreadablePID(t *testing.T) {
	for _, bad := range []string{"unreadable", "zero"} {
		t.Run(bad, func(t *testing.T) {
			isolateBootstrap(t)
			isRunningFn = func(string) bool { return true }
			readPIDFn = func(string) (int, error) {
				if bad == "zero" {
					return 0, nil
				}
				return 0, errors.New("PID unreadable")
			}
			detectSupervisionFn = func(string, bool, int) Supervision {
				t.Error("invalid PID reached ownership detector")
				return Supervision{}
			}
			err := ensureDaemon(context.Background(), io.Discard, false)
			var coded registry.CodedError
			if !errors.As(err, &coded) || coded.Code != "daemon_supervision_unverified" {
				t.Fatalf("invalid PID accepted: %v", err)
			}
		})
	}
}
