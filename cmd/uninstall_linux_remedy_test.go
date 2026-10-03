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

const userBusRemedyMarker = "XDG_RUNTIME_DIR=/run/user/$UID"

// TestLinuxUninstallRefusalNamesUserManagerRemedy pins the way out of the
// fail-closed refusal. When systemctl --user cannot answer at all (no user
// bus under su/sudo -u/SSH without a session, or no systemctl), the unit is
// kept, and the error has to say how to finish: rerun with the user bus, or
// remove the unit file by hand. "still running" is the control: systemd did
// answer, so the bus remedy would point the user at the wrong problem.
func TestLinuxUninstallRefusalNamesUserManagerRemedy(t *testing.T) {
	const noBus = "Failed to connect to user scope bus via local transport: $DBUS_SESSION_BUS_ADDRESS and $XDG_RUNTIME_DIR not defined"
	for _, tc := range []struct {
		name       string
		stopErr    error
		stopOut    string
		showOut    string
		showErr    error
		wantRemedy bool
	}{
		{name: "no user bus", stopErr: errors.New("exit status 1"), stopOut: noBus, showOut: noBus, showErr: errors.New("exit status 1"), wantRemedy: true},
		{name: "systemctl absent", stopErr: errors.New(`exec: "systemctl": executable file not found in $PATH`), showErr: errors.New(`exec: "systemctl": executable file not found in $PATH`), wantRemedy: true},
		{name: "still running", stopErr: errors.New("exit status 1"), stopOut: "stop refused", showOut: "ActiveState=active\nMainPID=288\n"},
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
			systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[1] {
				case "stop":
					return []byte(tc.stopOut), tc.stopErr
				case "show":
					return []byte(tc.showOut), tc.showErr
				}
				t.Fatalf("uninstall mutated manager although shutdown was not confirmed: %v", args)
				return nil, nil
			}
			var out, errOut bytes.Buffer
			uninstallCmd.SetOut(&out)
			uninstallCmd.SetErr(&errOut)
			err := uninstallCmd.RunE(uninstallCmd, nil)
			if err == nil || !strings.Contains(err.Error(), "unit retained because shutdown could not be confirmed") {
				t.Fatalf("uninstall error = %v, want the unit retained", err)
			}
			if got, readErr := os.ReadFile(path); readErr != nil || string(got) != original {
				t.Fatalf("unit bytes changed: %q, %v", got, readErr)
			}
			hasBus := strings.Contains(err.Error(), userBusRemedyMarker)
			hasManual := strings.Contains(err.Error(), "remove "+path+" by hand")
			if tc.wantRemedy && (!hasBus || !hasManual) {
				t.Fatalf("uninstall error = %v\nwant both remedies: rerun with %s, or remove %s by hand", err, userBusRemedyMarker, path)
			}
			if !tc.wantRemedy && (hasBus || hasManual) {
				t.Fatalf("uninstall error = %v\nsystemd answered, so the user-bus remedy misdirects", err)
			}
		})
	}
}

// TestLinuxUninstallHelpNamesRetentionAndRemedy pins the same way out in
// 'tslink uninstall --help', together with the fail-closed rule it escapes.
func TestLinuxUninstallHelpNamesRetentionAndRemedy(t *testing.T) {
	for _, want := range []string{
		"the unit file is kept and the command fails",
		userBusRemedyMarker,
		"remove ~/.config/systemd/user/tslink.service by hand",
	} {
		if !strings.Contains(uninstallCmd.Long, want) {
			t.Errorf("uninstall help missing %q:\n%s", want, uninstallCmd.Long)
		}
	}
}
