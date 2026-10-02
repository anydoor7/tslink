//go:build !windows

package daemon

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCopiedHelperSurvivesRuntimePreemptionSignal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink")
	copyTestExecutable(t, path)
	cmd := exec.Command(path, "serve")
	cmd.Env = append(os.Environ(), "TSLINK_DAEMON_TEST_MODE=success", "TSLINK_HELPER_READY=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("copied helper did not exit")
		}
		_ = stdout.Close()
	})
	ready := make(chan error, 1)
	go func() {
		marker := make([]byte, 6)
		_, err := io.ReadFull(stdout, marker)
		if err == nil && string(marker) != "ready\n" {
			err = io.ErrUnexpectedEOF
		}
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copied helper did not become ready")
	}
	if err := cmd.Process.Signal(syscall.SIGURG); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done <- err // Preserve the single joined result for cleanup.
		t.Fatalf("runtime preemption signal stopped the copied helper: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}
