//go:build darwin

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDarwinInstallOwnershipTreatsQueryTimeoutAsUnknown pins R4-10 on the
// launchd ownership read that decides whether install may replace a running
// daemon. One 2s timeout of 'launchctl print' used to read as "not owned" in
// that domain, and with no other domain owning the PID install refused with
// the manual-daemon conflict. A single timeout is now retried; a domain that
// times out twice leaves ownership unknown, never "not owned". The last two
// cases are the control group and keep the old classification.
func TestDarwinInstallOwnershipTreatsQueryTimeoutAsUnknown(t *testing.T) {
	gui := launchctlServiceTargetForDomain(launchctlDomain())
	for _, tc := range []struct {
		name        string
		guiAnswers  []error
		guiState    string
		wantOwned   bool
		wantErr     string
		wantGUICall int
	}{
		{name: "one timeout then owned", guiAnswers: []error{managerQueryTimeoutError("launchctl"), nil}, guiState: "state = running\npid = 1775\n", wantOwned: true, wantGUICall: 2},
		{name: "two timeouts are unknown", guiAnswers: []error{managerQueryTimeoutError("launchctl"), managerQueryTimeoutError("launchctl")}, wantErr: "could not tell whether launchd owns the running TSLink daemon", wantGUICall: 2},
		{name: "clean answer for another pid", guiAnswers: []error{nil}, guiState: "state = running\npid = 1888\n", wantErr: errManualDaemonConflict.Error(), wantGUICall: 1},
		{name: "non-timeout failure", guiAnswers: []error{errors.New("exit status 113")}, wantErr: errManualDaemonConflict.Error(), wantGUICall: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldPIDPath, oldRunning, oldReadPID := pidPathFn, isRunningFn, readPIDFn
			oldLaunchctl, oldConflict := launchctlCombinedOutput, installDaemonArtifactConflictFn
			t.Cleanup(func() {
				pidPathFn, isRunningFn, readPIDFn = oldPIDPath, oldRunning, oldReadPID
				launchctlCombinedOutput, installDaemonArtifactConflictFn = oldLaunchctl, oldConflict
			})
			dir := t.TempDir()
			plist := filepath.Join(dir, plistLabel+".plist")
			if err := os.WriteFile(plist, []byte("<plist/>\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			pidPathFn = func() (string, error) { return filepath.Join(dir, "tslink.pid"), nil }
			isRunningFn = func(string) bool { return true }
			readPIDFn = func(string) (int, error) { return 1775, nil }
			installDaemonArtifactConflictFn = func() error { return errManualDaemonConflict }
			guiCalls := 0
			launchctlCombinedOutput = func(args ...string) ([]byte, error) {
				if args[0] != "print" {
					t.Fatalf("ownership read ran a non-read verb: %v", args)
				}
				if args[1] != gui {
					// The user/<uid> domain answers cleanly and owns nothing.
					return []byte("Could not find service"), errors.New("exit status 113")
				}
				index := min(guiCalls, len(tc.guiAnswers)-1)
				guiCalls++
				if tc.guiAnswers[index] != nil {
					return nil, tc.guiAnswers[index]
				}
				return []byte(tc.guiState), nil
			}

			state, err := captureLaunchAgentPreviousState(plist)
			if tc.wantErr == "" {
				if err != nil || state.Target != gui {
					t.Fatalf("captureLaunchAgentPreviousState() target=%q err=%v, want owned by %s after one retried timeout", state.Target, err, gui)
				}
				if guiCalls != tc.wantGUICall {
					t.Fatalf("launchctl print %s calls = %d, want %d", gui, guiCalls, tc.wantGUICall)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("captureLaunchAgentPreviousState() err=%v, want %q", err, tc.wantErr)
			}
			if tc.wantErr != errManualDaemonConflict.Error() && (errors.Is(err, errManualDaemonConflict) || !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("captureLaunchAgentPreviousState() err=%v, want an unknown-ownership timeout, not the manual-daemon conflict", err)
			}
			if guiCalls != tc.wantGUICall {
				t.Fatalf("launchctl print %s calls = %d, want %d", gui, guiCalls, tc.wantGUICall)
			}
		})
	}
}

// TestDarwinInstallVerificationRetriesOneQueryTimeout pins R4-10 on the
// post-bootstrap verification. A timed-out print after the job was seen
// running read as "not running", which the settle check reports as the
// PID/state changing, and install rolled back a healthy job.
func TestDarwinInstallVerificationRetriesOneQueryTimeout(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	launchAgentVerifyTimeout = 5 * time.Second
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() { launchctlCombinedOutput = oldLaunchctl })

	t.Run("one timeout between healthy samples", func(t *testing.T) {
		calls := 0
		launchctlCombinedOutput = func(args ...string) ([]byte, error) {
			calls++
			if calls == 2 {
				return nil, managerQueryTimeoutError("launchctl")
			}
			return runningLaunchAgentState(), nil
		}
		if _, err := verifyLaunchAgentRunning("gui/501/" + plistLabel); err != nil {
			t.Fatalf("verify error = %v after one timed-out print of a running job, want success", err)
		}
	})

	// Control: before the job is first seen running, a timed-out print is
	// still just "not running yet" and polling continues, as it always did.
	t.Run("timeouts before the job is seen keep polling", func(t *testing.T) {
		calls := 0
		launchctlCombinedOutput = func(args ...string) ([]byte, error) {
			calls++
			if calls <= 2 {
				return nil, managerQueryTimeoutError("launchctl")
			}
			return runningLaunchAgentState(), nil
		}
		if _, err := verifyLaunchAgentRunning("gui/501/" + plistLabel); err != nil || calls != 4 {
			t.Fatalf("verify err=%v calls=%d, want success once the job answers", err, calls)
		}
	})

	t.Run("two timeouts in a row fail as unknown", func(t *testing.T) {
		calls := 0
		launchctlCombinedOutput = func(args ...string) ([]byte, error) {
			calls++
			if calls == 1 {
				return runningLaunchAgentState(), nil
			}
			return nil, managerQueryTimeoutError("launchctl")
		}
		_, err := verifyLaunchAgentRunning("gui/501/" + plistLabel)
		if !errors.Is(err, context.DeadlineExceeded) || calls != 3 || strings.Contains(err.Error(), "PID/state changed") {
			t.Fatalf("verify err=%v calls=%d, want the timeout reported after exactly one retry, not a PID/state change", err, calls)
		}
	})
}
