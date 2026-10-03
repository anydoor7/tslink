//go:build windows

package daemon

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func stubProcessLivenessError(t *testing.T) {
	t.Helper()
	orig := openProcessForLiveness
	openProcessForLiveness = func(uint32, bool, uint32) (windows.Handle, error) {
		return 0, errors.New("injected OpenProcess error")
	}
	t.Cleanup(func() { openProcessForLiveness = orig })
}

func stubStopProcessLookupError(t *testing.T) {
	t.Helper()
	orig := findProcessForStop
	findProcessForStop = func(int) (*os.Process, error) {
		return nil, errors.New("injected FindProcess error")
	}
	t.Cleanup(func() { findProcessForStop = orig })
}

func TestStopDaemonWindowsReportsSuccessAfterGracefulShutdown(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1", "TSLINK_HELPER_READY=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stubProcessExecutableForPID(t, cmd.Process.Pid)
	var exited chan struct{}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		if exited == nil {
			_ = cmd.Wait()
		} else {
			<-exited
		}
	})
	// The helper emits readiness after ShutdownContext creates the event.
	// Startup scheduling is not part of StopDaemon's graceful-stop contract.
	ready := make([]byte, len("ready\n"))
	if _, err := io.ReadFull(stdout, ready); err != nil || string(ready) != "ready\n" {
		t.Fatalf("helper shutdown event was not ready: event=%q err=%v", ready, err)
	}
	done := make(chan error, 1)
	exited = make(chan struct{})
	go func() { done <- cmd.Wait(); close(exited) }()

	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := StopDaemon(pidPath); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}

	select {
	case waitErr := <-done:
		if waitErr != nil {
			t.Fatalf("graceful helper did not exit successfully: %v", waitErr)
		}
	case <-time.After(windowsStopTimeout):
		t.Fatal("terminated helper process did not exit")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists after successful stop: %v", err)
	}
}
