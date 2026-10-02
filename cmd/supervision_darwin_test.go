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
			managerOutputFn = func(ctx context.Context, _ string, args ...string) ([]byte, error) {
				queries++
				if tc.readError {
					return nil, errors.New("manager unavailable")
				}
				if args[0] == "print-disabled" {
					return []byte("disabled services = {\n}\n"), nil
				}
				return launchctlFixture(t, tc.state, tc.managerPID), nil
			}
			s := detectSupervision(context.Background(), filepath.Join(dir, "tslink.pid"), tc.running, 4242)
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
		managerOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(tc.output), nil }
		if got := launchdAutostartEnabled(context.Background(), "user/fixture"); got != tc.want {
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
	managerOutputFn = func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "print-disabled" {
			return []byte("disabled services = {\n \"com.tslink.daemon\" => disabled\n}\n"), nil
		}
		return []byte("state = running\npid = 4242\nproperties = keepalive | runatload\n"), nil
	}
	s := detectSupervision(context.Background(), filepath.Join(dir, "tslink.pid"), true, 4242)
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
	managerOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("state = running\npid = 41564\n"), nil
	}
	if err := checkUnregisteredSupervisor(context.Background()); err == nil {
		t.Fatal("accepted a loaded job with no plist")
	}
	if err := checkSupervisorProcessScope(context.Background()); err == nil {
		t.Fatal("accepted a supervisor PID from another config")
	}
}

// TestBootstrapLaunchdInstallSettles pins the settle contract:
// waitStableDaemon accepts only after two consecutive samples agree AND the
// settle window has elapsed, and rejects a job whose state or PID moves inside
// that window.
//
// The settle window is scenario-specific on purpose, because the two halves of
// the contract want opposite things from it. The production invariant is
// "samples >= 2 && time.Since(firstGood) >= settle" (cmd/daemon_setup.go), so:
//
//   - stable needs a window short enough to elapse, and can only be promised
//     two samples. Asserting three was a wall-clock assumption, not the
//     contract: under load a 1ms poll sleeps far longer, the 6ms window is
//     already spent by the second sample, and the call returns correctly with
//     calls=2. That is what turned this red in a full -race package run while
//     it passed in isolation.
//   - dies and changes_pid need a window long enough that it cannot elapse
//     before their divergence lands, or the wait succeeds first and the
//     scenario silently stops testing rejection at all. With a 6ms window this
//     half was flaky in the direction that looks like a pass.
func TestBootstrapLaunchdInstallSettles(t *testing.T) {
	oldOutput, oldSettle := launchctlCombinedOutput, launchAgentSettleWindow
	t.Cleanup(func() { launchctlCombinedOutput, launchAgentSettleWindow = oldOutput, oldSettle })
	for _, tc := range []struct {
		scenario  string
		settle    time.Duration
		timeout   time.Duration
		wantErr   string
		wantCalls int
	}{
		// Short window: the wait may return as soon as the second sample agrees.
		{scenario: "stable", settle: time.Millisecond, timeout: 5 * time.Second, wantErr: "", wantCalls: 2},
		// Window far longer than the three samples take, so divergence always
		// lands first no matter how slow this machine is. The expected error is
		// matched by reason, not merely by being non-nil: a timeout also
		// produces an error, so "wantErr != nil" would stay green with the
		// divergence check deleted outright -- it would just take the full
		// timeout to say so.
		{scenario: "dies", settle: time.Minute, timeout: 5 * time.Second, wantErr: "PID/state changed", wantCalls: 3},
		{scenario: "changes_pid", settle: time.Minute, timeout: 5 * time.Second, wantErr: "PID/state changed", wantCalls: 3},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			launchAgentSettleWindow = tc.settle
			calls := 0
			launchctlCombinedOutput = func(context.Context, ...string) ([]byte, error) {
				calls++
				if calls > 2 {
					if tc.scenario == "dies" {
						return []byte("state = waiting\n"), nil
					}
					if tc.scenario == "changes_pid" {
						return []byte("state = running\npid = 43\n"), nil
					}
				}
				return []byte("state = running\npid = 42\n"), nil
			}
			_, err := waitForLaunchAgentRunning(context.Background(), "user/fixture/com.tslink.daemon", tc.timeout, time.Millisecond)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("scenario=%s err=%v, want success (calls=%d)", tc.scenario, err, calls)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("scenario=%s succeeded, want rejection naming %q (calls=%d)", tc.scenario, tc.wantErr, calls)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("scenario=%s err=%v, want it to name %q (calls=%d)", tc.scenario, err, tc.wantErr, calls)
			}
			if calls < tc.wantCalls {
				t.Fatalf("scenario=%s made %d launchctl calls, want at least %d", tc.scenario, calls, tc.wantCalls)
			}
		})
	}
}

func TestBootstrapInstallerUsesExistingConflictGuard(t *testing.T) {
	isolateBootstrap(t)
	oldGuard := installDaemonConflictFn
	t.Cleanup(func() { installDaemonConflictFn = oldGuard })
	guardCalls := 0
	installDaemonConflictFn = func(context.Context) error { guardCalls++; return errors.New("existing conflict guard marker") }
	var out bytes.Buffer
	err := installDaemonLocked(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), "existing conflict guard marker") || guardCalls != 1 {
		t.Fatalf("err=%v guard=%d", err, guardCalls)
	}
}
