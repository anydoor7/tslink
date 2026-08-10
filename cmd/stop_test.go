package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
)

func TestStopHelpDocumentsWindowsForcedTermination(t *testing.T) {
	stopCmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop command: %v", err)
	}

	help := stopCmd.Long
	if strings.Contains(help, "Windows). The daemon shuts down all") {
		t.Fatalf("stop help still claims graceful shutdown on Windows: %s", help)
	}
	if !strings.Contains(help, "Windows") || !strings.Contains(help, "not graceful") {
		t.Fatalf("stop help = %q, want explicit Windows non-graceful caveat", help)
	}
}

func TestDetectInstallDaemonConflictNamesPIDAndResolution(t *testing.T) {
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	t.Cleanup(func() {
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
	})

	pidPathFn = func() (string, error) { return "/tmp/tslink-test.pid", nil }
	isRunningFn = func(path string) bool { return path == "/tmp/tslink-test.pid" }
	readPIDFn = func(path string) (int, error) { return 1676, nil }

	err := detectInstallDaemonConflict("run 'tslink stop' and retry 'tslink install'")
	if output.ExitCode(err) != output.ExitConflict {
		t.Fatalf("ExitCode = %d, want %d: %v", output.ExitCode(err), output.ExitConflict, err)
	}
	for _, want := range []string{"pid 1676", "tslink stop", "tslink install", "exit 4", "conflict"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("conflict error = %q, want %q", err, want)
		}
	}
}

func TestStopHelpDocumentsMacOSLaunchAgentRestart(t *testing.T) {
	stopCmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop command: %v", err)
	}

	help := stopCmd.Long
	for _, want := range []string{"LaunchAgent", "KeepAlive", "will restart", "ThrottleInterval=30", "tslink uninstall"} {
		if !strings.Contains(help, want) {
			t.Fatalf("stop help = %q, want macOS autostart restart caveat containing %q", help, want)
		}
	}
}

func TestStopNotRunningDoesNotDeletePIDIdentityEvidence(t *testing.T) {
	oldIsRunning, oldStop, oldRemove, oldAbsent := isRunningFn, stopDaemonFn, removePIDFn, isProcessAbsentFromPIDFileFn
	t.Cleanup(func() {
		isRunningFn, stopDaemonFn, removePIDFn = oldIsRunning, oldStop, oldRemove
		isProcessAbsentFromPIDFileFn = oldAbsent
	})

	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	identityPath := pidPath + ".identity"
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(pid) error = %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("live-daemon-evidence\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
	}

	isRunningFn = func(string) bool { return false }
	isProcessAbsentFromPIDFileFn = func(string) bool { return false }
	stopDaemonFn = func(string) error {
		t.Fatal("stopDaemonFn called after not-running result")
		return nil
	}
	removeCalls := 0
	removePIDFn = func(string) { removeCalls++ }
	var out bytes.Buffer
	if err := stopService(pidPath, false, &out); err != nil {
		t.Fatalf("stopService() error = %v", err)
	}
	if removeCalls != 0 {
		t.Fatalf("removePIDFn calls = %d, want 0 after an inconclusive not-running result", removeCalls)
	}
	for _, path := range []string{pidPath, identityPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live-daemon evidence %q was not preserved: %v", path, err)
		}
	}
	if !strings.Contains(out.String(), "not running") {
		t.Fatalf("stop output = %q, want not-running message", out.String())
	}
}
