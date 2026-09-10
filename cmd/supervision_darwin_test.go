//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootstrapLaunchdOwnershipMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		running    bool
		state      string
		managerPID int
		readError  bool
		want       string
	}{
		{"owned", true, "running", 4242, false, "launchd"},
		{"manual", true, "running", 9999, false, "manual"},
		{"unknown", true, "running", 4242, true, "manual"},
		{"stopped_managed", false, "waiting", 0, false, "launchd"},
		{"none", false, "waiting", 0, true, "none"},
		{"unverified_live_manager", false, "running", 9999, false, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateBootstrap(t)
			path, _ := supervisorPath()
			var plist bytes.Buffer
			if err := plistTemplate.Execute(&plist, plistData{Label: plistLabel, Executable: "/tmp/tslink", ConfigDir: dir, ThrottleInterval: 30}); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, plist.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			queries := 0
			managerOutputFn = func(_ string, args ...string) ([]byte, error) {
				queries++
				if tc.readError {
					return nil, errors.New("manager unavailable")
				}
				if args[0] == "print-disabled" {
					return []byte("disabled services = {\n}\n"), nil
				}
				return launchctlFixture(t, tc.state, tc.managerPID), nil
			}
			s := detectSupervision(filepath.Join(dir, "tslink.pid"), tc.running, 4242)
			if s.Manager != tc.want || queries == 0 {
				t.Fatalf("supervision=%+v queries=%d", s, queries)
			}
			// A LaunchAgent is a per-user job, so a verified autostart has to
			// say "login": the boolean on its own cannot answer whether the
			// daemon returns after a reboot nobody signs in to, which is the
			// only question that matters on a host with no interactive user.
			// Ownership that could not be verified has no scope to report.
			if tc.want == "launchd" && (!s.Autostart || !s.RestartOnExit || s.Path != path || s.AutostartScope != autostartScopeLogin) {
				t.Fatalf("missing restart evidence: %+v", s)
			}
			if tc.want != "launchd" && (s.Autostart || s.RestartOnExit || s.AutostartScope != "") {
				t.Fatalf("unknown ownership promised restart: %+v", s)
			}
		})
	}
}

func TestBootstrapLaunchdDisabledOverride(t *testing.T) {
	dir := isolateBootstrap(t)
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"disabled services = {\n}\n", true},
		{"disabled services = {\n \"com.tslink.daemon\" => enabled\n}\n", true},
		{"disabled services = {\n \"com.tslink.daemon\" => disabled\n}\n", false},
		{"unreadable format", false},
	} {
		managerOutputFn = func(string, ...string) ([]byte, error) { return []byte(tc.output), nil }
		if got := launchdAutostartEnabled("user/fixture"); got != tc.want {
			t.Fatalf("output=%q enabled=%t want=%t", tc.output, got, tc.want)
		}
	}
	path, _ := supervisorPath()
	var plist bytes.Buffer
	if err := plistTemplate.Execute(&plist, plistData{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, plist.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	managerOutputFn = func(_ string, args ...string) ([]byte, error) {
		if args[0] == "print-disabled" {
			return []byte("disabled services = {\n \"com.tslink.daemon\" => disabled\n}\n"), nil
		}
		return []byte("state = running\npid = 4242\nproperties = keepalive | runatload\n"), nil
	}
	s := detectSupervision(filepath.Join(dir, "tslink.pid"), true, 4242)
	// Autostart is disabled, so there is nothing to scope. An empty scope is
	// how the renderer knows to print no boot-versus-login answer at all,
	// rather than a login answer that would not happen.
	if s.Manager != "launchd" || s.Autostart || !s.RestartOnExit || s.AutostartScope != "" {
		t.Fatalf("disabled-but-running job misreported: %+v", s)
	}
}

func TestBootstrapLaunchdConfigAndSideEffects(t *testing.T) {
	dir := isolateBootstrap(t) + "/a&b<quoted>"
	var plist bytes.Buffer
	if err := plistTemplate.Execute(&plist, plistData{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if !supervisorConfigMatches(plist.Bytes(), dir) || supervisorConfigMatches(plist.Bytes(), dir+"-other") {
		t.Fatalf("config did not round-trip escaped XML: %s", &plist)
	}
	managerOutputFn = func(string, ...string) ([]byte, error) { return []byte("state = running\npid = 41564\n"), nil }
	if err := checkUnregisteredSupervisor(); err == nil {
		t.Fatal("accepted a loaded job with no plist")
	}
	if err := checkSupervisorProcessScope(); err == nil {
		t.Fatal("accepted a supervisor PID from another config")
	}
}

func TestBootstrapLaunchdInstallSettles(t *testing.T) {
	oldOutput, oldSettle := launchctlCombinedOutput, launchAgentSettleWindow
	t.Cleanup(func() { launchctlCombinedOutput, launchAgentSettleWindow = oldOutput, oldSettle })
	launchAgentSettleWindow = 6 * time.Millisecond
	for _, scenario := range []string{"stable", "dies", "changes_pid"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			launchctlCombinedOutput = func(...string) ([]byte, error) {
				calls++
				if calls > 2 {
					if scenario == "dies" {
						return []byte("state = waiting\n"), nil
					}
					if scenario == "changes_pid" {
						return []byte("state = running\npid = 43\n"), nil
					}
				}
				return []byte("state = running\npid = 42\n"), nil
			}
			_, err := waitForLaunchAgentRunning("user/fixture/com.tslink.daemon", 30*time.Millisecond, time.Millisecond)
			if (err == nil) != (scenario == "stable") || calls < 3 {
				t.Fatalf("scenario=%s err=%v calls=%d", scenario, err, calls)
			}
		})
	}
}

func TestBootstrapInstallerUsesExistingConflictGuard(t *testing.T) {
	isolateBootstrap(t)
	oldGuard := installDaemonConflictFn
	t.Cleanup(func() { installDaemonConflictFn = oldGuard })
	guardCalls := 0
	installDaemonConflictFn = func() error { guardCalls++; return errors.New("existing conflict guard marker") }
	var out bytes.Buffer
	err := installDaemon(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), "existing conflict guard marker") || guardCalls != 1 {
		t.Fatalf("err=%v guard=%d", err, guardCalls)
	}
}

func fmtPID(pid int) string {
	if pid == 4242 {
		return "4242"
	}
	if pid == 9999 {
		return "9999"
	}
	return "0"
}
