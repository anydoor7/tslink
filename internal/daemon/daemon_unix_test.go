//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func stubProcessLivenessError(t *testing.T) {
	t.Helper()
	orig := findProcess
	findProcess = func(int) (*os.Process, error) {
		return nil, errors.New("injected findProcess error")
	}
	t.Cleanup(func() { findProcess = orig })
}

func stubStopProcessLookupError(t *testing.T) {
	t.Helper()
	stubProcessLivenessError(t)
}

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
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false)
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

func TestDaemonizeAppliesRestrictiveChildUmaskAndRestoresParent(t *testing.T) {
	origStart := startCmd
	origUmask := setUmask
	t.Cleanup(func() {
		startCmd = origStart
		setUmask = origUmask
	})

	var masks []int
	setUmask = func(mask int) int {
		masks = append(masks, mask)
		if mask == 0o077 {
			return 0o022
		}
		return 0
	}
	startCmd = func(cmd *exec.Cmd) error {
		if len(masks) != 1 || masks[0] != 0o077 {
			t.Fatalf("startCmd observed masks = %#o, want child umask applied first", masks)
		}
		return errors.New("stop before exec")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want injected start error")
	}
	if len(masks) != 2 || masks[0] != 0o077 || masks[1] != 0o022 {
		t.Fatalf("umask calls = %#o, want [077 022]", masks)
	}
}
