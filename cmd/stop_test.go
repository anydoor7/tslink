package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/daemon"
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

func TestStopHelpDocumentsLinuxSystemdStopSemantics(t *testing.T) {
	stopCmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop command: %v", err)
	}

	help := stopCmd.Long
	for _, want := range []string{"Linux systemd", "Restart=on-failure", "leaves it stopped", "systemctl --user start tslink.service"} {
		if !strings.Contains(help, want) {
			t.Fatalf("stop help = %q, want Linux systemd behavior containing %q", help, want)
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

func TestStopOnlyDeletesPIDArtifactsForConclusiveExitedProcess(t *testing.T) {
	oldIsRunning, oldStop, oldRemove, oldAbsent := isRunningFn, stopDaemonFn, removePIDFn, isProcessAbsentFromPIDFileFn
	t.Cleanup(func() {
		isRunningFn, stopDaemonFn, removePIDFn = oldIsRunning, oldStop, oldRemove
		isProcessAbsentFromPIDFileFn = oldAbsent
	})
	isRunningFn = func(string) bool { return false }
	isProcessAbsentFromPIDFileFn = daemon.IsProcessAbsentFromPIDFile
	removePIDFn = daemon.RemovePID
	stopDaemonFn = func(string) error {
		t.Fatal("stopDaemonFn called after not-running result")
		return nil
	}

	cases := []struct {
		name          string
		prepare       func(*testing.T, string)
		wantPIDExists bool
	}{
		{name: "missing PID remains unknown", prepare: func(*testing.T, string) {}},
		{name: "unreadable PID is preserved", wantPIDExists: true, prepare: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "garbage PID is preserved", wantPIDExists: true, prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "negative PID is preserved", wantPIDExists: true, prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("-1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "zero PID is preserved", wantPIDExists: true, prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "live process PID is preserved", wantPIDExists: true, prepare: func(t *testing.T, path string) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "TSLINK_STOP_LIVENESS_HELPER=1")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			ready, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || strings.TrimSpace(ready) != stopLivenessHelperReady {
				t.Fatalf("stop liveness helper readiness = %q err=%v", ready, err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "exited process artifacts are removed", prepare: func(t *testing.T, path string) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			pid := cmd.Process.Pid
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "tslink.pid")
			identityPath := pidPath + ".identity"
			tc.prepare(t, pidPath)
			if err := os.WriteFile(identityPath, []byte("identity-evidence\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := stopService(pidPath, false, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			_, pidErr := os.Lstat(pidPath)
			if got := pidErr == nil; got != tc.wantPIDExists {
				t.Fatalf("PID artifact exists = %t, want %t (err=%v)", got, tc.wantPIDExists, pidErr)
			}
			_, identityErr := os.Stat(identityPath)
			wantIdentity := tc.name != "exited process artifacts are removed"
			if got := identityErr == nil; got != wantIdentity {
				t.Fatalf("identity artifact exists = %t, want %t (err=%v)", got, wantIdentity, identityErr)
			}
		})
	}
}
