//go:build darwin || linux

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/daemon"
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

// Doctor now sends this case to 'tslink install', so install has to accept it.
// A stale PID file naming somebody else's process is not a reason to refuse:
// installing writes our unit and starts our daemon without touching it.
func TestBootstrapInstallAcceptsForeignPIDAndRefusesUnverifiableOne(t *testing.T) {
	for _, tc := range []struct {
		name     string
		contents string
		conflict bool
	}{
		{"foreign_live_process", fmt.Sprint(os.Getpid()), false},
		{"unreadable_pid", "unreadable PID", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateBootstrap(t)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			pidPath := filepath.Join(dir, "tslink.pid")
			oldPIDPath := pidPathFn
			t.Cleanup(func() { pidPathFn = oldPIDPath })
			pidPathFn = func() (string, error) { return pidPath, nil }
			isRunningFn = func(string) bool { return false }
			readPIDFn = daemon.ReadPID
			if err := os.WriteFile(pidPath, []byte(tc.contents), 0600); err != nil {
				t.Fatal(err)
			}
			err := detectInstallDaemonConflict("run 'tslink stop' and retry 'tslink install'")
			if tc.conflict {
				if output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "process identity is unverified") {
					t.Fatalf("want unverified-identity conflict, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("install refused over a PID file owned by an unrelated program: %v", err)
			}
		})
	}
}
