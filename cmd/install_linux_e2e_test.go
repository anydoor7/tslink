//go:build linux

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real-systemd e2e for the install settle window (the Round C-2 defect: install
// reported success while the freshly restarted unit was already in systemd's
// restart loop).
//
// Off by default. When enabled it drives the caller's REAL `systemctl --user`
// manager: it installs, upgrades, and removes the real tslink.service unit in
// $HOME, so it must only run inside a disposable Linux session.
// scripts/systemd-e2e.sh is the intended driver: it builds the binaries from
// the current tree, runs this test in an OrbStack VM, and supplies the mutation
// control (a build whose verify is blind MUST make this test fail).
//
// Gate and inputs:
//
//	TSLINK_SYSTEMD_E2E=1
//	TSLINK_SYSTEMD_E2E_GOOD_BIN  absolute path; a build whose `serve` stays up
//	TSLINK_SYSTEMD_E2E_BAD_BIN   absolute path; a build whose `serve` exits at once
//
// Every assertion about the unit is made by this test against systemd itself,
// never by trusting the product's own verify: that verify is the thing under test.

// systemdE2ESentinel is the one failure line scripts/systemd-e2e.sh keys on to
// tell "the e2e observed the defect" apart from "the e2e failed for another
// reason". Keep the text in sync with the script.
const systemdE2ESentinel = "E2E_SENTINEL: install claimed success while the unit is crash-looping"

const systemdE2EProbeService = "tslink-e2e-probe"

func TestSystemdInstallE2E(t *testing.T) {
	good, bad := systemdE2EBinaries(t)
	systemdE2ERequireUserManager(t)
	systemdE2ERefuseIfUnitActive(t)

	addedProbe := false
	t.Cleanup(func() { systemdE2EReset(t, good, addedProbe) })
	systemdE2EReset(t, good, false)
	addedProbe = systemdE2EEnsureRegistryHasService(t, good)

	t.Run("fresh install of a healthy serve settles and stays up", func(t *testing.T) {
		run := systemdE2ERun(t, good, "install", "--json")
		result, data := e2eDecodeEnvelope(t, run, "good install")
		if !result.OK || run.ExitCode != 0 || data["installed"] != true || data["started"] != true {
			t.Fatalf("good install: ok=%v exit=%d data=%v stderr=%q", result.OK, run.ExitCode, data, run.Stderr)
		}
		pid := systemdE2ERequireRunning(t, "after good install")
		time.Sleep(500 * time.Millisecond)
		if again := systemdE2ERequireRunning(t, "500ms after good install"); again != pid {
			t.Fatalf("MainPID drifted %d -> %d after a healthy install", pid, again)
		}
		systemdE2ERequireExecStart(t, good)
	})

	t.Run("upgrade to a crash-looping serve is rolled back to the previous unit", func(t *testing.T) {
		run := systemdE2ERun(t, bad, "install", "--json")
		message := systemdE2ERequireFailedInstall(t, run, "bad upgrade")
		if !strings.Contains(message, "restored and restarted") {
			t.Fatalf("bad upgrade: error = %q, want the previous unit restored and restarted", message)
		}
		systemdE2ERequireRunning(t, "after rollback")
		systemdE2ERequireExecStart(t, good)
	})

	t.Run("uninstall removes the unit", func(t *testing.T) {
		run := systemdE2ERun(t, good, "uninstall", "--json")
		result, _ := e2eDecodeEnvelope(t, run, "good uninstall")
		if !result.OK || run.ExitCode != 0 {
			t.Fatalf("good uninstall: ok=%v exit=%d stdout=%s stderr=%q", result.OK, run.ExitCode, run.Stdout, run.Stderr)
		}
		if props := systemdE2EShow(t, "LoadState", "ActiveState"); props["ActiveState"] != "inactive" {
			t.Fatalf("after uninstall: %v, want ActiveState=inactive", props)
		}
	})

	t.Run("fresh install of a crash-looping serve is reported as a failure", func(t *testing.T) {
		run := systemdE2ERun(t, bad, "install", "--json")
		message := systemdE2ERequireFailedInstall(t, run, "bad fresh install")
		if strings.Contains(message, "restored") {
			t.Fatalf("bad fresh install: error = %q mentions a restore, but no previous unit existed", message)
		}
		// Positive control for the fixture itself: systemd must actually be in
		// the loop the message describes.
		props := systemdE2EShow(t, "ActiveState", "SubState", "NRestarts")
		if props["SubState"] != "auto-restart" && props["ActiveState"] != "failed" {
			t.Fatalf("bad fresh install: unit is %v, want the crash loop systemd actually entered (auto-restart or failed)", props)
		}
	})
}

func systemdE2EBinaries(t *testing.T) (good, bad string) {
	t.Helper()
	if os.Getenv("TSLINK_SYSTEMD_E2E") != "1" {
		t.Skip("set TSLINK_SYSTEMD_E2E=1 (and run inside a disposable systemd user session) to enable")
	}
	good = os.Getenv("TSLINK_SYSTEMD_E2E_GOOD_BIN")
	bad = os.Getenv("TSLINK_SYSTEMD_E2E_BAD_BIN")
	for name, path := range map[string]string{
		"TSLINK_SYSTEMD_E2E_GOOD_BIN": good,
		"TSLINK_SYSTEMD_E2E_BAD_BIN":  bad,
	} {
		if !filepath.IsAbs(path) {
			t.Fatalf("%s must be an absolute path, got %q", name, path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return good, bad
}

// systemdE2ERequireUserManager fails closed: once the gate is set explicitly, a
// missing user manager is an error, not a skip, so a green run can never mean
// "nothing happened".
func systemdE2ERequireUserManager(t *testing.T) {
	t.Helper()
	out, err := exec.Command("systemctl", "--user", "show", "--property=Version").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "Version=") {
		t.Fatalf("no reachable systemd user manager (err=%v): %s", err, out)
	}
}

func systemdE2ERefuseIfUnitActive(t *testing.T) {
	t.Helper()
	props := systemdE2EShow(t, "LoadState", "ActiveState", "SubState")
	switch props["ActiveState"] {
	case "active", "activating", "reloading", "deactivating":
		t.Fatalf("refusing: %s is %s/%s on this host; this e2e installs and removes the real unit", systemdServiceName, props["ActiveState"], props["SubState"])
	}
}

func systemdE2EEnv() []string {
	return append(os.Environ(),
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
	)
}

func systemdE2ERun(t *testing.T, binary string, args ...string) e2eRun {
	t.Helper()
	return e2eRunBinary(t, binary, "", "", systemdE2EEnv(), args...)
}

// systemdE2EShow reads unit properties in key=value form. It deliberately does
// not use --value: systemctl prints values in its own internal order, not the
// requested one.
func systemdE2EShow(t *testing.T, properties ...string) map[string]string {
	t.Helper()
	args := []string{"--user", "show", systemdServiceName}
	for _, property := range properties {
		args = append(args, "--property="+property)
	}
	out, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("systemctl %v: %v\n%s", args, err, out)
	}
	props := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			props[key] = value
		}
	}
	return props
}

func systemdE2ERequireRunning(t *testing.T, context string) int {
	t.Helper()
	props := systemdE2EShow(t, "ActiveState", "SubState", "MainPID")
	pid, _ := strconv.Atoi(props["MainPID"])
	if props["ActiveState"] != "active" || props["SubState"] != "running" || pid <= 0 {
		t.Fatalf("%s: unit is %v, want active/running with MainPID>0", context, props)
	}
	return pid
}

func systemdE2ERequireExecStart(t *testing.T, binary string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatalf("resolve %q: %v", binary, err)
	}
	props := systemdE2EShow(t, "ExecStart")
	if !strings.Contains(props["ExecStart"], "path="+resolved+" ") {
		t.Fatalf("ExecStart = %q, want the unit to run %q", props["ExecStart"], resolved)
	}
}

// systemdE2ERequireFailedInstall asserts that install told the truth about a
// unit that never settled. An ok:true here is exactly the C-2 defect.
func systemdE2ERequireFailedInstall(t *testing.T, run e2eRun, context string) string {
	t.Helper()
	result, _ := e2eDecodeEnvelope(t, run, context)
	if result.OK {
		t.Fatalf("%s: %s\nstdout=%s", context, systemdE2ESentinel, run.Stdout)
	}
	if run.ExitCode == 0 {
		t.Fatalf("%s: ok:false with exit code 0\nstdout=%s", context, run.Stdout)
	}
	if result.Error == nil {
		t.Fatalf("%s: ok:false without an error object\nstdout=%s", context, run.Stdout)
	}
	message := result.Error.Message
	for _, want := range []string{"did not settle", "reset-failed"} {
		if !strings.Contains(message, want) {
			t.Fatalf("%s: error = %q, want it to contain %q", context, message, want)
		}
	}
	return message
}

// systemdE2EEnsureRegistryHasService makes sure serve has something to run.
// Returns true when it added the probe service (registry-only; no credentials
// are present, so nothing reaches a tailnet).
func systemdE2EEnsureRegistryHasService(t *testing.T, good string) bool {
	t.Helper()
	run := systemdE2ERun(t, good, "list", "--json")
	result, data := e2eDecodeEnvelope(t, run, "list")
	if !result.OK {
		t.Fatalf("list: %s\nstderr=%q", run.Stdout, run.Stderr)
	}
	if services, _ := data["services"].([]any); len(services) > 0 {
		return false
	}
	run = systemdE2ERun(t, good, "add", systemdE2EProbeService, "--proxy", "localhost:3000", "--json")
	if result, _ := e2eDecodeEnvelope(t, run, "add probe service"); !result.OK {
		t.Fatalf("add probe service: %s\nstderr=%q", run.Stdout, run.Stderr)
	}
	return true
}

// systemdE2EReset returns the host to "no tslink unit" regardless of the state
// a previous (possibly aborted) run left behind, then checks that it did.
func systemdE2EReset(t *testing.T, good string, removeProbe bool) {
	t.Helper()
	systemctl := func(args ...string) {
		_ = exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
	}
	systemctl("stop", systemdServiceName)
	systemctl("disable", systemdServiceName)
	if path, err := systemdServicePath(); err == nil {
		_ = os.Remove(path)
	}
	systemctl("daemon-reload")
	systemctl("reset-failed", systemdServiceName)
	if removeProbe {
		remove := exec.Command(good, "remove", systemdE2EProbeService, "--json")
		remove.Env = systemdE2EEnv()
		_ = remove.Run()
	}
	props := systemdE2EShow(t, "LoadState", "ActiveState")
	if props["ActiveState"] == "active" || props["ActiveState"] == "activating" {
		t.Errorf("reset: %s is still %v", systemdServiceName, props)
	}
}
