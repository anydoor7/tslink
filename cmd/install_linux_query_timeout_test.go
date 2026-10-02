//go:build linux

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLinuxInstallOwnershipTreatsQueryTimeoutAsUnknown pins R4-10 on the
// ownership read that decides whether install may replace a running daemon.
// One 2s timeout of 'systemctl --user show' used to read as "not owned" and
// install refused with the manual-daemon conflict. A single timeout is now
// retried; two in a row are reported as unknown, never as "not owned". The
// last two cases are the control group: a clean answer that names another PID,
// and a non-timeout failure, still classify as not owned exactly as before.
func TestLinuxInstallOwnershipTreatsQueryTimeoutAsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		answers   []error
		wantOwned bool
		wantErr   string
		wantCalls int
	}{
		{name: "one timeout then owned", answers: []error{managerQueryTimeoutError("systemctl"), nil}, wantOwned: true, wantCalls: 2},
		{name: "two timeouts are unknown", answers: []error{managerQueryTimeoutError("systemctl"), managerQueryTimeoutError("systemctl")}, wantErr: "could not tell whether systemd owns the running TSLink daemon", wantCalls: 2},
		{name: "clean answer for another pid", answers: []error{nil}, wantErr: errManualDaemonConflict.Error(), wantCalls: 1},
		{name: "non-timeout failure", answers: []error{errors.New("exit status 1")}, wantErr: errManualDaemonConflict.Error(), wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldPIDPath, oldRunning, oldReadPID := pidPathFn, isRunningFn, readPIDFn
			oldSystemctl, oldConflict := systemctlCombinedOutput, installDaemonArtifactConflictFn
			t.Cleanup(func() {
				pidPathFn, isRunningFn, readPIDFn = oldPIDPath, oldRunning, oldReadPID
				systemctlCombinedOutput, installDaemonArtifactConflictFn = oldSystemctl, oldConflict
			})
			dir := t.TempDir()
			servicePath := filepath.Join(dir, systemdServiceName)
			if err := os.WriteFile(servicePath, []byte("unit\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			pidPathFn = func() (string, error) { return filepath.Join(dir, "tslink.pid"), nil }
			isRunningFn = func(string) bool { return true }
			readPIDFn = func(string) (int, error) { return 1775, nil }
			installDaemonArtifactConflictFn = func() error { return errManualDaemonConflict }
			calls := 0
			systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[1] != "show" {
					t.Fatalf("ownership read ran a non-read verb: %v", args)
				}
				index := min(calls, len(tc.answers)-1)
				calls++
				if tc.answers[index] != nil {
					return nil, tc.answers[index]
				}
				if tc.wantOwned {
					return []byte("MainPID=1775\n"), nil
				}
				return []byte("MainPID=1888\n"), nil
			}

			state, err := captureSystemdPreviousState(context.Background(), servicePath)
			if tc.wantErr == "" {
				if err != nil || !state.OwnedRunning {
					t.Fatalf("captureSystemdPreviousState(context.Background(), ) owned=%v err=%v, want owned after one retried timeout", state.OwnedRunning, err)
				}
				if calls != tc.wantCalls {
					t.Fatalf("systemctl show calls = %d, want %d", calls, tc.wantCalls)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("captureSystemdPreviousState(context.Background(), ) err=%v, want %q", err, tc.wantErr)
			}
			if tc.wantErr != errManualDaemonConflict.Error() && (errors.Is(err, errManualDaemonConflict) || !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("captureSystemdPreviousState(context.Background(), ) err=%v, want an unknown-ownership timeout, not the manual-daemon conflict", err)
			}
			if calls != tc.wantCalls {
				t.Fatalf("systemctl show calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// TestLinuxInstallVerificationRetriesOneQueryTimeout pins R4-10 on the
// post-restart verification. One timed-out sample used to fail the install
// and trigger rollback although the unit was healthy.
func TestLinuxInstallVerificationRetriesOneQueryTimeout(t *testing.T) {
	stubFastSystemdSettle(t)
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() { systemctlCombinedOutput = oldSystemctl })

	t.Run("one timeout then healthy", func(t *testing.T) {
		calls := 0
		systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			if calls == 1 {
				return nil, managerQueryTimeoutError("systemctl")
			}
			return runningSystemdState(), nil
		}
		if _, err := verifySystemdServiceRunning(context.Background()); err != nil {
			t.Fatalf("verify error = %v after one timed-out sample of a healthy unit, want success", err)
		}
	})

	t.Run("two timeouts in a row fail", func(t *testing.T) {
		calls := 0
		systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
			calls++
			return nil, managerQueryTimeoutError("systemctl")
		}
		_, err := verifySystemdServiceRunning(context.Background())
		if !errors.Is(err, context.DeadlineExceeded) || calls != 2 {
			t.Fatalf("verify err=%v calls=%d, want the timeout reported after exactly one retry", err, calls)
		}
	})
}
