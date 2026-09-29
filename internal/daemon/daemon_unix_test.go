//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func stubProcessLivenessError(t *testing.T) {
	t.Helper()
	orig := findProcess
	findProcess = func(int) (*os.Process, error) {
		return nil, errors.New("injected findProcess error")
	}
	t.Cleanup(func() { findProcess = orig })
}

func stubStopProcessLookupError(t *testing.T) {
	t.Helper()
	stubProcessLivenessError(t)
}

func TestDaemonizeConfiguresUnixChildSessionAndRootDir(t *testing.T) {
	origStart := startCmd
	t.Cleanup(func() { startCmd = origStart })

	var gotDir string
	var gotSetsid bool
	startCmd = func(cmd *exec.Cmd) error {
		gotDir = cmd.Dir
		gotSetsid = cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
		return errors.New("stop before exec")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false, false, false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want injected start error")
	}
	if gotDir != "/" {
		t.Fatalf("daemon child Dir = %q, want /", gotDir)
	}
	if !gotSetsid {
		t.Fatal("daemon child SysProcAttr.Setsid = false, want true")
	}
}

func TestDaemonizeAppliesRestrictiveChildUmaskAndRestoresParent(t *testing.T) {
	origStart := startCmd
	origUmask := setUmask
	t.Cleanup(func() {
		startCmd = origStart
		setUmask = origUmask
	})

	var masks []int
	setUmask = func(mask int) int {
		masks = append(masks, mask)
		if mask == 0o077 {
			return 0o022
		}
		return 0
	}
	startCmd = func(cmd *exec.Cmd) error {
		if len(masks) != 1 || masks[0] != 0o077 {
			t.Fatalf("startCmd observed masks = %#o, want child umask applied first", masks)
		}
		return errors.New("stop before exec")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false, false, false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want injected start error")
	}
	if len(masks) != 2 || masks[0] != 0o077 || masks[1] != 0o022 {
		t.Fatalf("umask calls = %#o, want [077 022]", masks)
	}
}

// TestStopDaemon_SignalError: a PID file naming a live process that is not
// TSLink passes StopDaemon's signal-0 probe and must then be refused by the
// identity check, leaving the process untouched.
//
// The target is a process this test started. It used to be PID 1: as root
// (common in CI containers) signal 0 to init succeeds, so a single regression
// in the identity check would have sent SIGTERM to init. With a child of its
// own, the test can also prove the refusal left the process running, which it
// could never check for init.
func TestStopDaemon_SignalError(t *testing.T) {
	pid := startForeignLiveProcess(t)
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want signal error")
	}
	if !strings.Contains(err.Error(), "refusing to stop process") {
		t.Fatalf("StopDaemon() error = %v, want identity refusal error", err)
	}
	var status syscall.WaitStatus
	if reaped, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); err != nil || reaped != 0 {
		t.Fatalf("PID %d is not a still-running child of this test after the refused stop: wait4 = %d, %v (status %v)", pid, reaped, err, status)
	}
}
