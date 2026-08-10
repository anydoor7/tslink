//go:build darwin || linux

package cmd

import (
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
)

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
