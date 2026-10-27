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
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// seedExistingEmptyInstallation makes configDir an existing, empty install
// instead of a first run.
//
// A config directory with no registry, no node state and no supervisor
// evidence is a first run, and a real `tslink serve` in one initializes the
// default home gateway and starts its tailnet node (registry.initializeGateway).
// Starting a node reads host facts through tailscale (on Linux that runs
// `loginctl list-sessions`) and reaches for the control plane. A test that
// launches the compiled serve daemon only to observe its process lifetime owns
// none of that, so it starts from an installation serve leaves alone: an
// existing empty registry is explicitly not a first run.
func seedExistingEmptyInstallation(t *testing.T, configDir string) {
	t.Helper()
	regPath := filepath.Join(configDir, "registry.json")
	if err := os.WriteFile(regPath, []byte(`{"schema_version":1,"services":[]}`), 0o600); err != nil {
		t.Fatalf("seed existing empty registry: %v", err)
	}
}

func TestCompiledDaemonDoesNotOutliveTestLauncher(t *testing.T) {
	configDir := t.TempDir()
	// This test owns the daemon's process lifetime, not the default gateway: a
	// first-run config directory would make the daemon start the home node and
	// query the host's login sessions before it notices its launcher is gone.
	seedExistingEmptyInstallation(t, configDir)
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

	// The seeded installation is what keeps this test off the gateway start
	// path; a daemon that initialized the default gateway anyway would have
	// started the home node, so the isolation is checked, not assumed.
	reg, loadErr := registry.Load(filepath.Join(configDir, "registry.json"))
	if loadErr != nil {
		t.Fatalf("load registry after the daemon exited: %v", loadErr)
	}
	if reg.Portal != nil {
		t.Fatalf("the daemon initialized the default gateway in an existing installation: portal=%+v", reg.Portal)
	}
}
