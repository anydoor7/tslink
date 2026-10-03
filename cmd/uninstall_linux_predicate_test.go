//go:build linux

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLinuxUninstallStoppedPredicateNeedsBothHalves pins each half of the
// "stopped" predicate that decides whether a failed stop may still remove the
// unit. The older fixture (active, MainPID=288) violates both halves at once,
// so deleting either half left it green. Each refusal case here violates
// exactly one half; the two proceed cases are the control group that shows a
// refusal comes from the predicate and not from anything earlier.
func TestLinuxUninstallStoppedPredicateNeedsBothHalves(t *testing.T) {
	for _, tc := range []struct {
		name       string
		showOutput string
		wantRefuse string
	}{
		// ActiveState half: no main process, but systemd is still tearing the
		// unit down (or bringing it up), so its processes may be alive.
		{name: "deactivating without MainPID", showOutput: "ActiveState=deactivating\nMainPID=0\n", wantRefuse: `ActiveState="deactivating" MainPID="0"`},
		{name: "activating without MainPID", showOutput: "ActiveState=activating\nMainPID=0\n", wantRefuse: `ActiveState="activating" MainPID="0"`},
		// MainPID half: systemd calls the unit inactive, yet names a live main
		// process.
		{name: "inactive with MainPID", showOutput: "ActiveState=inactive\nMainPID=4242\n", wantRefuse: `ActiveState="inactive" MainPID="4242"`},
		{name: "inactive and stopped", showOutput: "ActiveState=inactive\nMainPID=0\n"},
		{name: "failed and stopped", showOutput: "ActiveState=failed\nMainPID=0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			oldHome, oldSystemctl := linuxUserHomeDirFn, systemctlCombinedOutput
			t.Cleanup(func() {
				linuxUserHomeDirFn, systemctlCombinedOutput = oldHome, oldSystemctl
				uninstallCmd.SetOut(nil)
				uninstallCmd.SetErr(nil)
			})
			linuxUserHomeDirFn = func() (string, error) { return home, nil }
			path := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			const original = "unit\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			var calls []string
			systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				calls = append(calls, args[1])
				switch args[1] {
				case "stop":
					return []byte("stop refused"), errors.New("stop failed")
				case "show":
					return []byte(tc.showOutput), nil
				}
				if tc.wantRefuse != "" {
					t.Fatalf("uninstall mutated manager although shutdown was not confirmed: %v", args)
				}
				return nil, nil
			}
			var out, errOut bytes.Buffer
			uninstallCmd.SetOut(&out)
			uninstallCmd.SetErr(&errOut)
			err := uninstallCmd.RunE(uninstallCmd, nil)
			got, readErr := os.ReadFile(path)

			if tc.wantRefuse != "" {
				if err == nil || !strings.Contains(err.Error(), "unit retained because shutdown could not be confirmed") || !strings.Contains(err.Error(), tc.wantRefuse) {
					t.Fatalf("uninstall error = %v, want refusal naming %s", err, tc.wantRefuse)
				}
				if strings.Join(calls, ",") != "stop,show" {
					t.Fatalf("manager calls = %v, want stop and read-only show only", calls)
				}
				if readErr != nil || string(got) != original {
					t.Fatalf("unit bytes changed although shutdown was not confirmed: %q, %v", got, readErr)
				}
				if strings.Contains(out.String(), "removed") {
					t.Fatalf("uninstall claimed removal: %s", out.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("uninstall error = %v, want the confirmed-stopped unit removed with a warning", err)
			}
			if !os.IsNotExist(readErr) {
				t.Fatalf("unit still present after a confirmed stop: %q, %v", got, readErr)
			}
			if !strings.Contains(errOut.String(), "stop failed") || !strings.Contains(out.String(), "systemd user service removed") {
				t.Fatalf("stdout=%q stderr=%q, want removal with the stop failure as a warning", out.String(), errOut.String())
			}
		})
	}
}
