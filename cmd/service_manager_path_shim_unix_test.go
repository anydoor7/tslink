//go:build darwin || linux

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testenv"
)

// The seam guard in service_manager_guard_wiring_unix_test.go closes the exits
// that exist inside this test process. This file covers the other half: a test
// that runs a *compiled* binary hands the work to a process where no seam was
// ever replaced, and where the service manager it resolves by bare name is the
// real one.
//
// Every probe below names a job label / unit that does not exist. That is
// deliberate and load bearing: if the shim, the PATH and the guard all failed
// at once, the worst a real launchctl or systemctl could do with these argv is
// answer "not found". No assertion in this file depends on the operator's real
// com.tslink.daemon, and no probe names it.

const serviceManagerShimProbeEnv = "TSLINK_SERVICE_MANAGER_SHIM_PROBE"

// serviceManagerShimProbeManager is the manager this platform's production code
// resolves by bare name.
func serviceManagerShimProbeManager() string {
	if runtime.GOOS == "darwin" {
		return "launchctl"
	}
	return "systemctl"
}

// runServiceManagerProbe is the one place in this file that builds a real
// process exit to a service manager. Both call sites are written with the
// binary inline so that service_manager_exit_inventory_test.go's test-exec scan
// sees them and this file has to be reviewed on that list; resolving the name
// from a variable would have hidden the exit from the scanner that exists to
// find exactly this shape.
// A nil env inherits this process's environment, which sends the recording to
// the package-wide log; a non-nil env is how a probe keeps its own record.
func runServiceManagerProbe(env []string, args ...string) (stdout, stderr string, exitCode int) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("launchctl", args...)
	} else {
		cmd = exec.Command("systemctl", args...)
	}
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// serviceManagerShimWriteProbeArgs is a state-changing verb aimed at a target
// that does not exist.
func serviceManagerShimWriteProbeArgs(t *testing.T) []string {
	t.Helper()
	unique := fmt.Sprintf("com.tslink.pathshimprobe.%d.%d", os.Getpid(), time.Now().UnixNano())
	if runtime.GOOS == "darwin" {
		return []string{"bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), unique)}
	}
	return []string{"--user", "stop", unique + ".service"}
}

// serviceManagerShimReadProbeArgs is a read verb aimed at the same nonexistent
// target, used as the tolerated half of the teardown policy.
func serviceManagerShimReadProbeArgs(t *testing.T) []string {
	t.Helper()
	unique := fmt.Sprintf("com.tslink.pathshimprobe.%d.%d", os.Getpid(), time.Now().UnixNano())
	if runtime.GOOS == "darwin" {
		return []string{"print", fmt.Sprintf("gui/%d/%s", os.Getuid(), unique)}
	}
	return []string{"--user", "show", unique + ".service"}
}

func activeShim(t *testing.T) *testenv.ServiceManagerPathShim {
	t.Helper()
	guard := testenv.ActiveServiceManagerGuard()
	if guard == nil {
		t.Fatal("no service manager guard is active; TestMain is not wrapping m.Run with RunWithServiceManagerGuard")
	}
	shim := guard.ServiceManagerShim()
	if shim == nil || shim.Dir == "" {
		t.Fatal("the active guard planted no child-process PATH shim; every compiled-binary test in this package can reach the real OS service manager")
	}
	return shim
}

// TestServiceManagerPathShimIsFirstOnPath is the cheap half of the evidence.
// It proves the shim is installed without executing anything, so it still
// reports honestly on a machine where every other probe here is skipped.
func TestServiceManagerPathShimIsFirstOnPath(t *testing.T) {
	shim := activeShim(t)

	entries := filepath.SplitList(os.Getenv("PATH"))
	if len(entries) == 0 || entries[0] != shim.Dir {
		t.Fatalf("PATH[0] = %q, want the shim directory %q; a later entry does not win a lookup", firstOrEmpty(entries), shim.Dir)
	}
	resolved, err := exec.LookPath(serviceManagerShimProbeManager())
	if err != nil {
		t.Fatalf("LookPath(%s): %v", serviceManagerShimProbeManager(), err)
	}
	if got := filepath.Dir(resolved); got != shim.Dir {
		t.Fatalf("%s resolves to %q, want a file in the shim directory %q", serviceManagerShimProbeManager(), resolved, shim.Dir)
	}
}

func firstOrEmpty(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[0]
}

// TestServiceManagerPathShimInterceptsAStateChangingVerb runs the probe that
// matters: a write verb, resolved by bare name exactly the way
// cmd/install_darwin.go resolves it, from a process this package controls.
//
// The control group is the same helper with an argv that names no manager. A
// batch of interceptions proves nothing unless the recording file can also be
// empty, and this is the case that shows it can.
func TestServiceManagerPathShimInterceptsAStateChangingVerb(t *testing.T) {
	// Assert the shim is installed, then record into a log of this test's own.
	// Leaving a state-changing verb in the package-wide log would trip the
	// guard's teardown policy, which is a separate property with its own test.
	activeShim(t)
	probeLog := filepath.Join(t.TempDir(), "probe.log")
	env := append(environWithout(testenv.ServiceManagerShimLogEnv), testenv.ServiceManagerShimLogEnv+"="+probeLog)

	args := serviceManagerShimWriteProbeArgs(t)
	stdout, stderr, exitCode := runServiceManagerProbe(env, args...)

	if exitCode != testenv.ServiceManagerShimExitCode {
		t.Fatalf("%s %v exit = %d, want the shim's %d; stdout=%q stderr=%q",
			serviceManagerShimProbeManager(), args, exitCode, testenv.ServiceManagerShimExitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, testenv.ServiceManagerShimStderrPrefix) {
		t.Fatalf("stderr = %q, want the shim marker %q; a real binary produced this output",
			stderr, testenv.ServiceManagerShimStderrPrefix)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty; the shim executes nothing and prints nothing to stdout", stdout)
	}

	recordings := readShimCallsAllowingAbsence(t, probeLog)
	if len(recordings) != 1 {
		t.Fatalf("shim log holds %d entries, want exactly 1: %v", len(recordings), recordings)
	}
	recorded := recordings[0]
	if recorded.Manager != serviceManagerShimProbeManager() {
		t.Fatalf("recorded manager = %q, want %q", recorded.Manager, serviceManagerShimProbeManager())
	}
	if strings.Join(recorded.Args, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("recorded argv = %q, want %q; the parent cannot assert on what the child tried to do", recorded.Args, args)
	}
	if recorded.IsReadOnly() {
		t.Fatalf("recorded call %s classified read-only; the teardown policy would tolerate a state change", recorded)
	}
}

// TestServiceManagerShimLogStaysEmptyWithoutAServiceManagerCall is the control
// group for every log assertion in this file and in the guard's teardown.
func TestServiceManagerShimLogStaysEmptyWithoutAServiceManagerCall(t *testing.T) {
	dir := t.TempDir()
	logPath, planted, err := testenv.PlantServiceManagerShims(dir)
	if err != nil {
		t.Fatalf("plant shims: %v", err)
	}
	// An empty log means nothing unless fakes were there to write into it.
	if len(planted) == 0 {
		t.Fatal("no fake was planted, so an empty log is not evidence that no manager was called")
	}

	probe := exec.Command(compiledTSLinkBinary(t), "--version")
	probe.Env = append(os.Environ(),
		"TSLINK_CONFIG_DIR="+t.TempDir(),
		"TSLINK_DISABLE_KEYRING=1",
		testenv.ServiceManagerShimLogEnv+"="+logPath,
	)
	var combined bytes.Buffer
	probe.Stdout = &combined
	probe.Stderr = &combined
	if err := probe.Run(); err != nil {
		t.Fatalf("tslink --version: %v\n%s", err, combined.String())
	}

	calls, err := testenv.ReadServiceManagerShimCalls(logPath)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("a command that touches no service manager recorded %v; the log assertions elsewhere in this file are tautologies", calls)
	}
}

// TestCompiledBinaryResolvesTheServiceManagerThroughItsOwnPath is the
// end-to-end half: the shipped binary, spawned the way every compiled-binary
// test in this package spawns it, resolving the manager inside its own process.
//
// It is darwin-only on purpose. `tslink status` reads `launchctl print` on
// darwin; the linux equivalent has not been observed on this machine, and a
// test that silently skips on the platform it claims to cover is worse than one
// that says which platform it covers.
func TestCompiledBinaryResolvesTheServiceManagerThroughItsOwnPath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("verified for darwin's launchctl read path only; see the comment above")
	}
	binary := compiledTSLinkBinary(t)
	// status only asks launchctl about a LaunchAgent it can find, so an
	// inherited HOME makes the result depend on the host: it passes where TSLink
	// is installed and fails on a clean Mac or CI runner. Both children get a
	// scratch HOME holding a stand-in plist instead.
	home := launchAgentFixtureHome(t)

	// Redirect this child's recording away from the package-wide log so the
	// guard's teardown report stays a statement about the rest of the suite.
	guardedLog := filepath.Join(t.TempDir(), "guarded.log")
	guarded := exec.Command(binary, "status", "--json")
	guarded.Env = append(os.Environ(),
		"HOME="+home,
		"TSLINK_CONFIG_DIR="+t.TempDir(),
		"TSLINK_DISABLE_KEYRING=1",
		testenv.ServiceManagerShimLogEnv+"="+guardedLog,
	)
	var guardedOut bytes.Buffer
	guarded.Stdout = &guardedOut
	guarded.Stderr = &guardedOut
	_ = guarded.Run() // status' exit code is not the claim under test

	guardedCalls := readShimCallsAllowingAbsence(t, guardedLog)
	if len(guardedCalls) == 0 {
		t.Fatalf("tslink status made no service manager call through the shim; this test can no longer detect an escape\noutput: %s", guardedOut.String())
	}
	for _, call := range guardedCalls {
		if call.Manager != "launchctl" {
			t.Fatalf("recorded manager = %q, want launchctl: %s", call.Manager, call)
		}
	}

	// Negative: the same binary, the same invocation, with a decoy shim
	// directory ahead of the package one and no log override. Whichever
	// directory comes first on the child's PATH is the one that answers, which
	// is precisely why a child with no shim at all reaches /bin/launchctl.
	decoyDir := t.TempDir()
	decoyLog, _, err := testenv.PlantServiceManagerShims(decoyDir)
	if err != nil {
		t.Fatalf("plant decoy shims: %v", err)
	}
	decoy := exec.Command(binary, "status", "--json")
	decoy.Env = append(environWithout(testenv.ServiceManagerShimLogEnv),
		"HOME="+home,
		"TSLINK_CONFIG_DIR="+t.TempDir(),
		"TSLINK_DISABLE_KEYRING=1",
		"PATH="+decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var decoyOut bytes.Buffer
	decoy.Stdout = &decoyOut
	decoy.Stderr = &decoyOut
	_ = decoy.Run()

	decoyCalls := readShimCallsAllowingAbsence(t, decoyLog)
	if len(decoyCalls) == 0 {
		t.Fatalf("the decoy directory recorded nothing, so PATH order is not what decided the earlier interception\noutput: %s", decoyOut.String())
	}
	if len(readShimCallsAllowingAbsence(t, guardedLog)) != len(guardedCalls) {
		t.Fatal("the decoy run also wrote to the guarded log; the two runs are not distinguishable and the negative proves nothing")
	}
}

// launchAgentFixtureHome returns a scratch HOME whose LaunchAgents directory
// holds a minimal com.tslink.daemon plist. The plist points at a binary that
// does not exist; it only has to make status look the agent up.
func launchAgentFixtureHome(t *testing.T) string {
	t.Helper()
	// The same label as plistLabel, which only exists in darwin builds; this
	// file also compiles on Linux.
	const label = "com.tslink.daemon"
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(agents, 0o700); err != nil {
		t.Fatalf("create LaunchAgents fixture: %v", err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>/nonexistent/tslink</string><string>serve</string></array></dict></plist>
`
	if err := os.WriteFile(filepath.Join(agents, label+".plist"), []byte(plist), 0o600); err != nil {
		t.Fatalf("write LaunchAgent fixture: %v", err)
	}
	return home
}

func readShimCallsAllowingAbsence(t *testing.T, logPath string) []testenv.ServiceManagerShimCall {
	t.Helper()
	calls, err := testenv.ReadServiceManagerShimCalls(logPath)
	if err != nil {
		t.Fatalf("read shim log %s: %v", logPath, err)
	}
	return calls
}

func environWithout(name string) []string {
	prefix := name + "="
	filtered := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// TestServiceManagerShimProbeHelper is the body that runs inside a child test
// binary, mirroring TestServiceManagerGuardTripwireHelper. The teardown policy
// under test is fatal to a whole package, so it has to be observed from outside.
func TestServiceManagerShimProbeHelper(t *testing.T) {
	switch os.Getenv(serviceManagerShimProbeEnv) {
	case "":
		t.Skip("helper process only; driven by TestServiceManagerShimTeardownFailsThePackageOnAStateChangingCall")
	case "control":
		// Same binary, same TestMain, same shim, no service manager call.
	case "read":
		if _, stderr, code := runServiceManagerProbe(nil, serviceManagerShimReadProbeArgs(t)...); code != testenv.ServiceManagerShimExitCode {
			t.Fatalf("read probe exit = %d, want %d (stderr=%q)", code, testenv.ServiceManagerShimExitCode, stderr)
		}
	case "write":
		if _, stderr, code := runServiceManagerProbe(nil, serviceManagerShimWriteProbeArgs(t)...); code != testenv.ServiceManagerShimExitCode {
			t.Fatalf("write probe exit = %d, want %d (stderr=%q)", code, testenv.ServiceManagerShimExitCode, stderr)
		}
	default:
		t.Fatalf("unknown shim probe mode %q", os.Getenv(serviceManagerShimProbeEnv))
	}
}

func runShimProbeChild(t *testing.T, mode string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	child := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestServiceManagerShimProbeHelper$", "-test.v", "-test.count=1")
	child.Env = append(environWithout(testenv.ServiceManagerShimLogEnv), serviceManagerShimProbeEnv+"="+mode)
	var combined bytes.Buffer
	child.Stdout = &combined
	child.Stderr = &combined

	err := child.Run()
	if ctx.Err() != nil {
		t.Fatalf("shim probe child %q timed out:\n%s", mode, combined.String())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, combined.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), combined.String()
	default:
		t.Fatalf("shim probe child %q: %v\n%s", mode, err, combined.String())
		return -1, ""
	}
}

// TestServiceManagerShimTeardownFailsThePackageOnAStateChangingCall pins the
// detector half of the shim: intercepting a call is not the same as telling
// anyone it happened.
//
// Three children through one harness. The control never calls a manager and
// must exit 0, otherwise the two below prove nothing. The read child makes a
// read-only call and must also exit 0, which is what stops the policy from
// being "anything that touches the log fails", a rule the 74 legitimate
// `launchctl print` calls this suite already makes would break immediately. The
// write child makes a state-changing call and must fail its package.
func TestServiceManagerShimTeardownFailsThePackageOnAStateChangingCall(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child test binaries")
	}

	controlCode, controlLog := runShimProbeChild(t, "control")
	if controlCode != 0 {
		t.Fatalf("control child exit = %d, want 0; the harness is broken and the cases below prove nothing:\n%s", controlCode, controlLog)
	}
	if !strings.Contains(controlLog, "--- PASS: TestServiceManagerShimProbeHelper") {
		t.Fatalf("control child did not run the helper:\n%s", controlLog)
	}

	readCode, readLog := runShimProbeChild(t, "read")
	if !strings.Contains(readLog, "--- PASS: TestServiceManagerShimProbeHelper") {
		t.Fatalf("read child's helper did not pass:\n%s", readLog)
	}
	if readCode != 0 {
		t.Fatalf("read child exit = %d, want 0; a read-only call must stay tolerated:\n%s", readCode, readLog)
	}
	if !strings.Contains(readLog, "read-only, tolerated") {
		t.Fatalf("read child did not report the intercepted call at all:\n%s", readLog)
	}

	writeCode, writeLog := runShimProbeChild(t, "write")
	if !strings.Contains(writeLog, "--- PASS: TestServiceManagerShimProbeHelper") {
		t.Fatalf("write child's helper did not pass, so the interception itself is unproven:\n%s", writeLog)
	}
	if writeCode == 0 {
		t.Fatalf("write child exit = 0; a state-changing call through the shim did not fail its package:\n%s", writeLog)
	}
	if !strings.Contains(writeLog, "NOT read-only") {
		t.Fatalf("write child did not classify the call as state-changing:\n%s", writeLog)
	}
}
