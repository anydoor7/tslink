//go:build !windows

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestCompiledDaemonDoesNotOutliveTestLauncher(t *testing.T) {
	configDir := t.TempDir()
	pidPath := filepath.Join(configDir, "tslink.pid")
	launcher := exec.Command(compiledTSLinkBinary(t), "serve", "--daemon", "--json", "--no-browser")
	launcher.Env = append(os.Environ(),
		config.ConfigDirEnv+"="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		testDaemonParentLifetimeEnv+"=1",
	)
	var stdout, stderr bytes.Buffer
	launcher.Stdout = &stdout
	launcher.Stderr = &stderr
	if err := launcher.Start(); err != nil {
		t.Fatalf("start compiled daemon launcher: %v", err)
	}

	launcherDone := make(chan error, 1)
	go func() { launcherDone <- launcher.Wait() }()
	launcherWaited := false

	var daemonPID int
	var daemonProcess *os.Process
	t.Cleanup(func() {
		if daemonProcess != nil {
			_ = daemonProcess.Signal(syscall.SIGCONT)
		}
		if launcher.Process != nil {
			_ = launcher.Process.Kill()
		}
		if !launcherWaited {
			select {
			case <-launcherDone:
			case <-time.After(testwait.Budget(t)):
			}
		}
		if daemonPID > 0 && daemon.IsProcessRunning(daemonPID) {
			if err := daemon.StopDaemon(pidPath); err != nil {
				_ = daemonProcess.Kill()
			}
		}
	})

	// Poll every millisecond so the daemon is frozen as soon as it publishes
	// its PID; only the deadline is a hang guard.
	deadline := time.Now().Add(testwait.Budget(t))
	for time.Now().Before(deadline) {
		pid, err := daemon.ReadPID(pidPath)
		if err == nil && daemon.IsProcessRunning(pid) {
			daemonPID = pid
			break
		}
		time.Sleep(time.Millisecond)
	}
	if daemonPID == 0 {
		t.Fatalf("detached daemon never became observable: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if daemonPID == 2469 {
		t.Fatal("refusing to signal a real installed daemon PID 2469")
	}

	var err error
	daemonProcess, err = os.FindProcess(daemonPID)
	if err != nil {
		t.Fatalf("find detached daemon %d: %v", daemonPID, err)
	}
	// Freeze a still-live detached child immediately after it publishes its PID.
	// If the launcher is still running this widens the cancellation window; if
	// it already exited, the same process-existence assertion applies directly.
	if err := daemonProcess.Signal(syscall.SIGSTOP); err != nil {
		if !daemon.IsProcessRunning(daemonPID) {
			return
		}
		t.Fatalf("stop detached daemon %d: %v", daemonPID, err)
	}
	launcherExited := false
	select {
	case <-launcherDone:
		launcherExited = true
		launcherWaited = true
	default:
	}

	if !launcherExited {
		// The launcher can exit and be reaped between the select above and
		// this Kill; that is the already-exited case, not a failure.
		if err := launcher.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("kill daemon launcher: %v", err)
		}
		testwait.Recv(t, launcherDone, "daemon launcher exited after kill")
		launcherWaited = true
	}
	if err := daemonProcess.Signal(syscall.SIGCONT); err != nil {
		if !daemon.IsProcessRunning(daemonPID) {
			return
		}
		t.Fatalf("resume detached daemon %d: %v", daemonPID, err)
	}

	testwait.Until(t, fmt.Sprintf("detached daemon %d exited after its launcher", daemonPID), func() bool { return !daemon.IsProcessRunning(daemonPID) })
}
