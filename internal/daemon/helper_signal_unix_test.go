//go:build !windows

package daemon

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCopiedHelperIgnoresNonTerminationSignals(t *testing.T) {
	cmd := startCopiedHelperProcess(t, filepath.Join(t.TempDir(), "tslink"))
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if err := cmd.Process.Signal(syscall.SIGURG); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		_, inspectErr := defaultProcessExecutable(cmd.Process.Pid)
		t.Fatalf("helper exited after SIGURG: wait=%v executable=%v", err, inspectErr)
	case <-time.After(100 * time.Millisecond):
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper termination: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Fatal("helper did not terminate after SIGTERM")
	}
}
