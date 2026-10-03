//go:build linux

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/testenv"
)

// installLinuxUnitFixture puts a real unit file on disk, bound to the config
// directory under test, and points every manager query at a scripted answer.
func installLinuxUnitFixture(t *testing.T, properties string, loginctlOut string, loginctlErr error) string {
	t.Helper()
	home := t.TempDir()
	dir := testenv.SetHome(t, home)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	path, err := systemdServicePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	unit := "[Unit]\n[Service]\n" + systemdConfigEnvironment(absDir) + "\nExecStart=/usr/bin/tslink serve\n"
	if err := os.WriteFile(path, []byte(unit), 0600); err != nil {
		t.Fatal(err)
	}
	oldManager, oldLoginctl, oldUser := managerOutputFn, loginctlCombinedOutputFn, linuxUserNameFn
	t.Cleanup(func() {
		managerOutputFn, loginctlCombinedOutputFn, linuxUserNameFn = oldManager, oldLoginctl, oldUser
	})
	linuxUserNameFn = func() string { return "tester" }
	managerOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		if properties == "" {
			return []byte("Failed to connect to bus: No such file or directory\n"), errors.New("exit status 1")
		}
		return []byte(properties), nil
	}
	loginctlCombinedOutputFn = func(context.Context, ...string) ([]byte, error) { return []byte(loginctlOut), loginctlErr }
	return path
}

const linuxStoppedUnitProperties = "LoadState=loaded\nActiveState=inactive\nSubState=dead\nMainPID=0\nUnitFileState=enabled\nRestart=on-failure\nFragmentPath=%s\n"

// An enabled systemd *user* unit only comes back at boot when this user has
// lingering. Without it the unit waits for a login, which is a different
// answer to "will it still be there after a reboot" than autostart alone can
// give. The scope has to track lingering, and the guidance has to name the
// command that changes it.
func TestLinuxSupervisionReportsAutostartScopeFromLinger(t *testing.T) {
	for _, tc := range []struct {
		name      string
		out       string
		err       error
		wantScope string
		wantText  string
	}{
		{"linger_on", "yes\n", nil, autostartScopeBoot, "starts at boot"},
		{"linger_off", "no\n", nil, autostartScopeLogin, `loginctl enable-linger "$USER"`},
		{"linger_blank", "\n", nil, autostartScopeLogin, `loginctl enable-linger "$USER"`},
		{"linger_unreadable", "", errors.New("exit status 1"), autostartScopeUnknown, "could not be determined"},
		{"linger_unexpected", "maybe\n", nil, autostartScopeUnknown, "could not be determined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := installLinuxUnitFixture(t, "", tc.out, tc.err)
			managerOutputFn = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(strings.Replace(linuxStoppedUnitProperties, "%s", path, 1)), nil
			}
			// Record what detection actually asks loginctl to do. The
			// fixture's t.Cleanup restores the seam, so this wrapper lives
			// exactly as long as the subtest.
			var loginctlCalls []string
			scripted := loginctlCombinedOutputFn
			loginctlCombinedOutputFn = func(ctx context.Context, args ...string) ([]byte, error) {
				loginctlCalls = append(loginctlCalls, strings.Join(args, " "))
				return scripted(ctx, args...)
			}
			s := detectSupervision(context.Background(), "", false, 0)
			if s.Manager != "systemd" || !s.Autostart {
				t.Fatalf("fixture did not verify: %+v", s)
			}
			if s.AutostartScope != tc.wantScope {
				t.Fatalf("scope=%q want %q (detail=%q)", s.AutostartScope, tc.wantScope, s.Detail)
			}
			if !strings.Contains(s.Detail, tc.wantText) {
				t.Fatalf("detail %q missing %q", s.Detail, tc.wantText)
			}
			// Lingering is a per-user setting affecting every service this
			// user owns, so detection reads it and leaves it alone. Both
			// halves of that promise are asserted positively: wantText above
			// requires the detail to hand the user the command to run
			// themselves, and the call log below requires the only loginctl
			// invocation on this path to be the read-only property query.
			//
			// The assertion this replaced searched s.Detail for the phrase
			// "enabling lingering for you", which appears nowhere in the
			// production code -- `grep -rn` for it across non-test .go files
			// returns 0 hits -- so it was a tautology that could never fail,
			// and its silence was indistinguishable from a working guard.
			// Checking the call log instead catches the thing the phrase was
			// standing in for: an actual mutation of the user's setting.
			wantQuery := `show-user tester --property=Linger --value`
			if len(loginctlCalls) != 1 || loginctlCalls[0] != wantQuery {
				t.Fatalf("loginctl calls = %q, want exactly one read-only query %q", loginctlCalls, wantQuery)
			}
		})
	}
}

// A unit that is already installed, with the user manager out of reach, is a
// missing session or missing lingering. Reinstalling rewrites the same file
// and changes neither, so the recovery step has to say something else.
func TestLinuxSupervisionDistinguishesUnreachableManagerFromMissingUnit(t *testing.T) {
	t.Run("installed_but_manager_unreachable", func(t *testing.T) {
		path := installLinuxUnitFixture(t, "", "no\n", nil)
		s := detectSupervision(context.Background(), "", false, 0)
		if s.Manager != "none" || s.Autostart {
			t.Fatalf("unreachable manager must not claim supervision: %+v", s)
		}
		for _, want := range []string{path, "is installed", `loginctl enable-linger "$USER"`, "systemctl --user status"} {
			if !strings.Contains(s.Detail, want) {
				t.Fatalf("detail %q missing %q", s.Detail, want)
			}
		}
		if strings.Contains(s.Detail, "Run: tslink install") {
			t.Fatalf("detail sends an installed unit back to install: %q", s.Detail)
		}
	})

	t.Run("no_unit_installed", func(t *testing.T) {
		path := installLinuxUnitFixture(t, "", "no\n", nil)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		s := detectSupervision(context.Background(), "", false, 0)
		if s.Manager != "none" || !strings.Contains(s.Detail, "No systemd user unit is installed") || !strings.Contains(s.Detail, "Run: tslink install") {
			t.Fatalf("missing unit detail=%q manager=%q", s.Detail, s.Manager)
		}
	})

	t.Run("installed_but_not_loaded", func(t *testing.T) {
		installLinuxUnitFixture(t, "LoadState=not-found\nMainPID=0\n", "no\n", nil)
		s := detectSupervision(context.Background(), "", false, 0)
		if s.Manager != "none" || !strings.Contains(s.Detail, "systemctl --user daemon-reload") {
			t.Fatalf("unloaded detail=%q", s.Detail)
		}
	})
}
