//go:build linux

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func setupRepairManager(t *testing.T, gate func()) {
	t.Helper()
	isolateBootstrap(t)
	resetRootJSONFlag(t)
	stubFastSystemdSettle(t)
	oldSystem, oldLogin, oldConflict, oldArtifact := systemctlCombinedOutput, loginctlCombinedOutputFn, installDaemonConflictFn, installDaemonArtifactConflictFn
	t.Cleanup(func() {
		systemctlCombinedOutput = oldSystem
		loginctlCombinedOutputFn = oldLogin
		installDaemonConflictFn = oldConflict
		installDaemonArtifactConflictFn = oldArtifact
	})
	installDaemonConflictFn = func() error { return nil }
	installDaemonArtifactConflictFn = func() error { return nil }
	loginctlCombinedOutputFn = func(...string) ([]byte, error) { return []byte("yes"), nil }
	var running atomic.Bool
	isRunningFn = func(string) bool { return running.Load() }
	installDaemonFn = installDaemonLocked
	detectSupervisionFn = func(string, bool, int) Supervision {
		return Supervision{Manager: "systemd", Installed: true, Autostart: true, RestartOnExit: true}
	}
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		gate()
		switch args[1] {
		case "restart":
			running.Store(true)
		case "stop":
			running.Store(false)
		case "show":
			if running.Load() {
				return []byte("LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=4242\nNRestarts=0\n"), nil
			}
			return []byte("LoadState=not-found\nActiveState=inactive\nMainPID=0\n"), nil
		}
		return nil, nil
	}
}

func TestRepairFailedInstallRollbackCannotUndoPeer(t *testing.T) {
	setupRepairManager(t, func() {})
	path, _ := supervisorPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte("old unit bytes\n")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	original := systemctlCombinedOutput
	fail := true
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if fail && args[1] == "restart" {
			return nil, errors.New("injected restart failure")
		}
		if fail && args[1] == "stop" {
			close(entered)
			<-release
		}
		return original(args...)
	}
	first := make(chan error, 1)
	go func() { first <- repairOperation(context.Background(), "install") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("rollback never entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := repairOperation(ctx, "install")
	cancel()
	close(release)
	firstErr := repairAwait(t, first)
	if !errors.Is(err, context.DeadlineExceeded) || firstErr == nil {
		t.Fatalf("rollback transaction: peer=%v first=%v", err, firstErr)
	}
	data, _ := os.ReadFile(path)
	if string(data) != string(old) {
		t.Fatal("rollback lost previous bytes")
	}
	fail = false
	if err := repairOperation(context.Background(), "install"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	dir, _ := absoluteConfigDir()
	if err != nil || !supervisorConfigMatches(data, dir) {
		t.Fatalf("successful peer lost final definition: %v", err)
	}
}
