//go:build !windows

package daemon

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonizeConfiguresUnixChildSessionAndRootDir(t *testing.T) {
	origStart := startCmd
	t.Cleanup(func() { startCmd = origStart })

	var gotDir string
	var gotSetsid bool
	startCmd = func(cmd *exec.Cmd) error {
		gotDir = cmd.Dir
		gotSetsid = cmd.SysProcAttr != nil && cmd.SysProcAttr.Setsid
		return errors.New("stop before exec")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "")
	if err == nil {
		t.Fatal("Daemonize() error = nil, want injected start error")
	}
	if gotDir != "/" {
		t.Fatalf("daemon child Dir = %q, want /", gotDir)
	}
	if !gotSetsid {
		t.Fatal("daemon child SysProcAttr.Setsid = false, want true")
	}
}
