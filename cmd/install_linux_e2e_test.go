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

	"github.com/monody0007/tslink/internal/testenv"
)

// Real-systemd e2e for the install settle window (the defect where install
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
// Before touching anything the test refuses to run when the host looks like a
// real installation rather than a disposable one: a unit that is running, a
// unit file that points at a binary this e2e did not supply, or a stored
// Tailscale credential (the daemon starts from systemd's environment, so no
// test-side variable can keep a credentialed serve off the tailnet).
//
// Every assertion about the unit is made by this test against systemd itself,
// never by trusting the product's own verify: that verify is the thing under test.
//
// Known blind spot, by design of the product verify: the bad fixture dies at
// exec time. A serve that dies after the two settle samples but before the
// window closes is certified "settled" (the verify proves settling, not
// survival) and this e2e does not probe that band.

// systemdE2ESentinel is the one failure line scripts/systemd-e2e.sh keys on to
// tell "the e2e observed the defect" apart from "the e2e failed for another
// reason". The script checks at preflight that this text is still present here.
const systemdE2ESentinel = "E2E_SENTINEL: install claimed success while the unit is crash-looping"

func TestSystemdInstallE2E(t *testing.T) {
	good, bad := systemdE2EBinaries(t)
	systemdE2EAllowRealSystemctl(t)
	systemdE2ERequireUserManager(t)
	systemdE2ERefuseForeignUnit(t, good, bad)
	systemdE2ERefuseStoredCredential(t, good)

	t.Cleanup(func() { systemdE2EReset(t) })
	systemdE2EReset(t)

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
		if props := systemdE2EShow(t, "UnitFileState"); props["UnitFileState"] != "enabled" {
			t.Fatalf("after good install: UnitFileState = %q, want enabled", props["UnitFileState"])
		}
		systemdE2ERequireUnitFileMode(t, 0o600)
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
		props := systemdE2EShow(t, "LoadState", "ActiveState")
		if props["LoadState"] != "not-found" || props["ActiveState"] != "inactive" {
			t.Fatalf("after uninstall: %v, want LoadState=not-found ActiveState=inactive", props)
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

// systemdE2EAllowRealSystemctl takes the guard's child-process PATH shim out of
// PATH for this test.
//
// It is needed because that shim is not selective: testenv rewrites the test
// binary's PATH once, so both halves of this e2e -- the systemctl commands this
// file builds directly and the compiled tslink binaries it spawns -- resolve the
// manager to a fake that refuses everything with exit 97. Without this call the e2e does not
// fail loudly; it passes or fails against the shim while claiming in its own doc
// comment to drive real systemd, which is worse than not having the e2e.
//
// It runs after systemdE2EBinaries, so the gate decides first: a run without
// TSLINK_SYSTEMD_E2E=1 skips before the shim is ever weakened. The opt-in is
// recorded in the guard's teardown report, so a run that took it is
// distinguishable from one that did not.
func systemdE2EAllowRealSystemctl(t *testing.T) {
	t.Helper()
	restore, err := testenv.AllowRealServiceManagerInChildProcesses(
		"TSLINK_SYSTEMD_E2E drives a real systemctl --user inside a disposable session; " +
			"the planted fake would answer every call with exit 97")
	if err != nil {
		t.Fatalf("cannot reach the real systemd user manager: %v", err)
	}
	t.Cleanup(restore)
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

// systemdE2ERefuseForeignUnit refuses a running unit, and a unit file whose
// ExecStart points at a binary this e2e did not supply: that is a real
// installation, and the reset below would delete its unit file.
// Leftovers of an aborted e2e run point at the e2e's own binaries and pass.
func systemdE2ERefuseForeignUnit(t *testing.T, good, bad string) {
	t.Helper()
	props := systemdE2EShow(t, "LoadState", "ActiveState", "SubState", "ExecStart")
	switch props["ActiveState"] {
	case "active", "activating", "reloading", "deactivating":
		t.Fatalf("refusing: %s is %s/%s on this host; this e2e installs and removes the real unit", systemdServiceName, props["ActiveState"], props["SubState"])
	}
	if props["LoadState"] != "loaded" {
		return
	}
	execPath := systemdE2EExecStartPath(props["ExecStart"])
	execDir := filepath.Dir(execPath)
	if execPath == "" || (execDir != filepath.Dir(good) && execDir != filepath.Dir(bad)) {
		t.Fatalf("refusing: an existing %s runs %q, which this e2e did not install; stop, disable, and remove it yourself if that is intended", systemdServiceName, execPath)
	}
}

// systemdE2ERefuseStoredCredential refuses hosts that hold a Tailscale
// credential. The check runs with the bare host environment on purpose:
// TSLINK_DISABLE_KEYRING=1 would blind it, and the daemon systemd starts never
// sees that variable anyway.
func systemdE2ERefuseStoredCredential(t *testing.T, good string) {
	t.Helper()
	run := e2eRunBinary(t, good, "", "", os.Environ(), "status", "--json")
	_, data := e2eDecodeEnvelope(t, run, "status (credential check)")
	if data["credential_stored"] == true {
		t.Fatalf("refusing: a Tailscale credential is stored on this host; a healthy install would register a real node")
	}
}

// systemdE2EEnv keeps the CLI processes off the keyring. Deliberately no
// TSLINK_CONFIG_DIR: the unit file has no Environment= line, so the daemon
// always uses the real ~/.config/tslink; giving the CLI a private config dir
// would desync its pidfile view from MainPID and trip the ownership guard.
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

// systemdE2EExecStartPath extracts the executable from systemd's
// "{ path=/x ; argv[]=/x serve ; ... }" ExecStart rendering.
func systemdE2EExecStartPath(execStart string) string {
	_, rest, ok := strings.Cut(execStart, "path=")
	if !ok {
		return ""
	}
	path, _, _ := strings.Cut(rest, " ;")
	return strings.TrimSpace(path)
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
	if got := systemdE2EExecStartPath(props["ExecStart"]); got != resolved {
		t.Fatalf("ExecStart = %q (path %q), want the unit to run %q", props["ExecStart"], got, resolved)
	}
}

func systemdE2ERequireUnitFileMode(t *testing.T, want os.FileMode) {
	t.Helper()
	path, err := systemdServicePath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat unit file: %v", err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("unit file %s mode = %04o, want %04o", path, got, want)
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

// systemdE2EReset returns the host to "no tslink unit" regardless of the state
// a previous (possibly aborted) run left behind, then checks that it did. It
// only ever runs after systemdE2ERefuseForeignUnit has passed, and it says
// which file it removed.
func systemdE2EReset(t *testing.T) {
	t.Helper()
	systemctl := func(args ...string) {
		_ = exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
	}
	systemctl("stop", systemdServiceName)
	systemctl("disable", systemdServiceName)
	if path, err := systemdServicePath(); err == nil {
		if err := os.Remove(path); err == nil {
			t.Logf("reset: removed %s", path)
		}
	}
	systemctl("daemon-reload")
	systemctl("reset-failed", systemdServiceName)
	props := systemdE2EShow(t, "LoadState", "ActiveState")
	if props["ActiveState"] == "active" || props["ActiveState"] == "activating" {
		t.Errorf("reset: %s is still %v", systemdServiceName, props)
	}
}
