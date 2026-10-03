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

// TestLinuxUninstallMissingUnitFileStillLoadedIsNotReportedAbsent covers a unit
// file deleted by hand while systemd still runs or enables tslink.service.
// "not installed" with exit 0 was false there: the service kept running and
// the default.target.wants link stayed behind. The last two cases are the
// control group: a unit systemd never had, and a user manager that cannot be
// reached, both still report "not installed" as before.
func TestLinuxUninstallMissingUnitFileStillLoadedIsNotReportedAbsent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		showOut     string
		showErr     error
		enabledLink bool
		wantErr     []string
	}{
		{name: "loaded and running", showOut: "LoadState=loaded\nActiveState=active\nMainPID=2021\n",
			wantErr: []string{`ActiveState="active" MainPID="2021"`, "systemctl --user stop tslink.service"}},
		{name: "not-found but still running", showOut: "LoadState=not-found\nActiveState=active\nMainPID=2261\n",
			wantErr: []string{`ActiveState="active" MainPID="2261"`, "systemctl --user stop tslink.service"}},
		{name: "deactivating", showOut: "LoadState=loaded\nActiveState=deactivating\nMainPID=0\n",
			wantErr: []string{`ActiveState="deactivating" MainPID="0"`, "systemctl --user stop tslink.service"}},
		{name: "enabled link left behind", showOut: "LoadState=not-found\nActiveState=inactive\nMainPID=0\n", enabledLink: true,
			wantErr: []string{"default.target.wants/tslink.service", "systemctl --user daemon-reload"}},
		{name: "running and enabled", showOut: "LoadState=loaded\nActiveState=active\nMainPID=2021\n", enabledLink: true,
			wantErr: []string{"systemctl --user stop tslink.service", "default.target.wants/tslink.service", "systemctl --user daemon-reload"}},
		{name: "never installed", showOut: "LoadState=not-found\nActiveState=inactive\nMainPID=0\n"},
		{name: "user manager unreachable", showErr: errors.New("exit status 1")},
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
			unitDir := filepath.Join(home, ".config", "systemd", "user")
			link := filepath.Join(unitDir, "default.target.wants", systemdServiceName)
			if tc.enabledLink {
				if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
					t.Fatal(err)
				}
				// Dangling, exactly as 'systemctl --user enable' leaves it
				// once the unit file it points at is deleted.
				if err := os.Symlink(filepath.Join(unitDir, systemdServiceName), link); err != nil {
					t.Fatal(err)
				}
			}
			var mutations []string
			systemctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[1] == "show" {
					if tc.showErr != nil {
						return []byte("Failed to connect to user scope bus"), tc.showErr
					}
					return []byte(tc.showOut), nil
				}
				mutations = append(mutations, args[1])
				if tc.showErr != nil {
					return []byte("Failed to connect to user scope bus"), tc.showErr
				}
				return nil, nil
			}
			var out, errOut bytes.Buffer
			uninstallCmd.SetOut(&out)
			uninstallCmd.SetErr(&errOut)
			err := uninstallCmd.RunE(uninstallCmd, nil)

			if len(tc.wantErr) == 0 {
				if err != nil || !strings.Contains(out.String(), "systemd user service not installed") {
					t.Fatalf("err=%v stdout=%q, want the unchanged not-installed success", err, out.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("uninstall succeeded with stdout=%q while systemd still runs or enables the unit", out.String())
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("uninstall error = %v, want it to name %q", err, want)
				}
			}
			if strings.Contains(out.String(), "not installed") {
				t.Fatalf("stdout = %q, claimed not installed", out.String())
			}
			if len(mutations) != 0 {
				t.Fatalf("uninstall changed systemd state %v before reporting; the report must leave the unit as found", mutations)
			}
			if tc.enabledLink {
				if _, statErr := os.Lstat(link); statErr != nil {
					t.Fatalf("enablement link removed without being asked: %v", statErr)
				}
			}
		})
	}
}
