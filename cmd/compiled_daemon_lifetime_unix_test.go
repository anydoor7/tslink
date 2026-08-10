//go:build !windows

package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
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

	var daemonPID int
	var daemonProcess *os.Process
	t.Cleanup(func() {
		if daemonProcess != nil {
			_ = daemonProcess.Signal(syscall.SIGCONT)
		}
		if launcher.Process != nil {
			_ = launcher.Process.Kill()
		}
		select {
		case <-launcherDone:
		case <-time.After(2 * time.Second):
		}
		if daemonPID > 0 && daemon.IsProcessRunning(daemonPID) {
			if err := daemon.StopDaemon(pidPath); err != nil {
				_ = daemonProcess.Kill()
			}
		}
	})

	deadline := time.Now().Add(10 * time.Second)
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
		t.Fatal("refusing to signal production daemon PID 2469")
	}

	var err error
	daemonProcess, err = os.FindProcess(daemonPID)
	if err != nil {
		t.Fatalf("find detached daemon %d: %v", daemonPID, err)
	}
	// Freeze the detached child immediately after it publishes its PID. This
	// widens the cancellation window deterministically: the launcher cannot
	// observe a ready/auth handoff before the test terminates it.
	if err := daemonProcess.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("stop detached daemon %d: %v", daemonPID, err)
	}
	select {
	case err := <-launcherDone:
		t.Fatalf("launcher exited before cancellation window was established: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	default:
	}

	if err := launcher.Process.Kill(); err != nil {
		t.Fatalf("kill daemon launcher: %v", err)
	}
	select {
	case <-launcherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon launcher did not exit after kill")
	}
	if err := daemonProcess.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume detached daemon %d: %v", daemonPID, err)
	}

	exitDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(exitDeadline) {
		if !daemon.IsProcessRunning(daemonPID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("detached daemon %d survived launcher exit", daemonPID)
}
