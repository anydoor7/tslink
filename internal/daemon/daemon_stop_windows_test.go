//go:build windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestStopDaemonWindowsReportsSuccessAfterTermination(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stubProcessExecutableForPID(t, cmd.Process.Pid)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		default:
		}
	})

	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	started := time.Now()
	if err := StopDaemon(pidPath); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= windowsStopTimeout {
		t.Fatalf("StopDaemon() took %s, want less than %s", elapsed, windowsStopTimeout)
	}

	select {
	case waitErr := <-done:
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			t.Fatalf("Wait() error = %v", waitErr)
		}
	case <-time.After(windowsStopTimeout):
		t.Fatal("terminated helper process did not exit")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists after successful stop: %v", err)
	}
}
