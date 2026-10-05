//go:build windows

package daemon

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/anydoor7/tslink/internal/testwait"
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
	done := make(chan error, 1)
	exited := make(chan struct{})
	startWait := sync.OnceFunc(func() {
		go func() { done <- cmd.Wait(); close(exited) }()
	})
	killAndJoin := func() {
		_ = cmd.Process.Kill()
		startWait()
		// Runs from cleanup, so it reports without t.Fatal.
		select {
		case <-exited:
		case <-time.After(testwait.Budget(t)):
			t.Error("shutdown helper did not exit after kill within the hang guard")
		}
	}
	t.Cleanup(func() {
		killAndJoin()
	})
	// The helper emits readiness after ShutdownContext creates the event.
	// Startup scheduling is not part of StopDaemon's graceful-stop contract.
	ready := make([]byte, len("ready\n"))
	read := make(chan error, 1)
	go func() { _, err := io.ReadFull(stdout, ready); read <- err }()
	select {
	case err := <-read:
		if err != nil || string(ready) != "ready\n" {
			t.Fatalf("helper shutdown event was not ready: event=%q err=%v", ready, err)
		}
	case <-time.After(testwait.Budget(t)):
		killAndJoin()
		testwait.Recv(t, read, "shutdown helper IPC reader exited after child kill")
		t.Fatal("helper shutdown event was not ready within the hang guard; killed and joined child")
	}
	startWait()

	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := StopDaemon(pidPath); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}

	if waitErr := testwait.Recv(t, done, "stopped helper process exited"); waitErr != nil {
		t.Fatalf("graceful helper did not exit successfully: %v", waitErr)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("PID file still exists after successful stop: %v", err)
	}
}
