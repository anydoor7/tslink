//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"
)

// TestInspectProcessLivenessReportsExitedProcessWithOpenHandleAsAbsent pins the
// Windows rule that an exited process is absent even while its process object
// still exists. Any open handle keeps the object, and with it a successful
// OpenProcess, alive after the process has terminated; measured in the test
// guest, 72 of 300 children were still openable right after cmd.Wait returned,
// always with exit code 0. Reading that as alive made `tslink stop` keep the
// PID artifacts of a daemon that was gone. The test holds its own handle so the
// lingering object is guaranteed rather than a race.
func TestInspectProcessLivenessReportsExitedProcessWithOpenHandleAsAbsent(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	pid := cmd.Process.Pid
	exited := false
	t.Cleanup(func() {
		if !exited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	held, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess(helper) error = %v", err)
	}
	defer windows.CloseHandle(held)

	// Control: the same probe on the running helper must say alive.
	if got := inspectProcessLiveness(pid); got != processLivenessAlive {
		t.Fatalf("inspectProcessLiveness(running helper) = %d, want alive (%d)", got, processLivenessAlive)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	_ = cmd.Wait()
	exited = true

	// Premise: the held handle keeps the exited process openable, which is the
	// state the product used to misread.
	probe, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("premise: exited helper is no longer openable while a handle is held: %v", err)
	}
	_ = windows.CloseHandle(probe)

	if got := inspectProcessLiveness(pid); got != processLivenessAbsent {
		t.Fatalf("inspectProcessLiveness(exited helper, object still open) = %d, want absent (%d)", got, processLivenessAbsent)
	}
	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !IsProcessAbsentFromPIDFile(pidPath) {
		t.Fatal("IsProcessAbsentFromPIDFile(exited helper, object still open) = false, want true")
	}
}
