//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"
)

// IsProcessRunning answers "may I signal this PID as the TSLink daemon?". Every
// process referenced here is one this test started; nothing is matched by name
// and no pre-existing PID is probed.

func startForeignLiveProcess(t *testing.T) int {
	t.Helper()
	shell := "/bin/sh"
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("no %s available to start a non-TSLink process: %v", shell, err)
	}
	cmd := exec.Command(shell, "-c", "while :; do sleep 1; done")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start foreign process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestIsProcessRunningRejectsNonPositivePIDs(t *testing.T) {
	for _, pid := range []int{0, -1, -1000} {
		if IsProcessRunning(pid) {
			t.Fatalf("IsProcessRunning(%d) = true, want false for a PID that cannot exist", pid)
		}
	}
}

func TestIsProcessRunningReportsFalseAfterAChildThisTestStartedExits(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a short-lived child here: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if IsProcessRunning(pid) {
		t.Fatalf("IsProcessRunning(%d) = true for a child this test already reaped", pid)
	}
}

func TestIsProcessRunningRejectsALiveProcessThatIsNotTSLink(t *testing.T) {
	pid := startForeignLiveProcess(t)

	if IsProcessRunning(pid) {
		t.Fatalf("IsProcessRunning(%d) = true for a live non-TSLink process this test started; "+
			"the daemon would be willing to signal an unrelated process", pid)
	}
}

func TestIsProcessRunningAcceptsATSLinkServeChildThisTestStarted(t *testing.T) {
	dir := t.TempDir()
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))

	if !IsProcessRunning(cmd.Process.Pid) {
		t.Fatalf("IsProcessRunning(%d) = false for a live TSLink serve process", cmd.Process.Pid)
	}
}

func TestIsProcessRunningRejectsATSLinkProcessThatIsNotServing(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "bin", "tslink")
	copyTestExecutable(t, binPath)
	cmd := startCopiedHelperProcessWithArgs(t, binPath, "logs", "-f")

	if IsProcessRunning(cmd.Process.Pid) {
		t.Fatalf("IsProcessRunning(%d) = true for a TSLink process that is not the serve daemon", cmd.Process.Pid)
	}
}

func TestIsProcessRunningFailsSafeWhenLivenessCannotBeDetermined(t *testing.T) {
	// Unknown liveness must read as "running" so nothing starts a second daemon
	// or reclaims a PID it could not inspect. The PID belongs to a child this
	// test started and already reaped, so no live process is touched either way.
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a short-lived child here: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()

	stubProcessLivenessError(t)

	if !IsProcessRunning(pid) {
		t.Fatal("IsProcessRunning() = false when process lookup failed, want a fail-safe true")
	}
}

func TestIsProcessRunningFailsSafeWhenIdentityCannotBeInspected(t *testing.T) {
	pid := startForeignLiveProcess(t)

	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(int) (string, error) { return "", os.ErrPermission }

	if !IsProcessRunning(pid) {
		t.Fatal("IsProcessRunning() = false when the executable could not be inspected, want a fail-safe true")
	}
}

func TestIsProcessRunningFailsSafeWhenBuildMetadataIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))

	orig := readExecutableBuildInfo
	t.Cleanup(func() { readExecutableBuildInfo = orig })
	readExecutableBuildInfo = func(string) (*debug.BuildInfo, error) {
		return nil, errors.New("injected EIO")
	}

	if !IsProcessRunning(cmd.Process.Pid) {
		t.Fatal("IsProcessRunning() = false when build metadata was unreadable, want a fail-safe true")
	}
}

func TestIsProcessRunningRejectsAForeignProcessEvenWhenArgvLooksLikeServe(t *testing.T) {
	// argv is attacker-influenceable; the module identity of the executable is
	// what must decide. A non-TSLink process claiming "serve" stays rejected.
	shell := "/bin/sh"
	if _, err := os.Stat(shell); err != nil {
		t.Skipf("no %s available: %v", shell, err)
	}
	cmd := exec.Command(shell, "-c", "while :; do sleep 1; done")
	cmd.Args = []string{"tslink", "serve"}
	cmd.Path = shell
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start the disguised process here: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	if IsProcessRunning(cmd.Process.Pid) {
		t.Fatalf("IsProcessRunning(%d) = true for a non-TSLink process whose argv claims to be a serve daemon", cmd.Process.Pid)
	}
}
