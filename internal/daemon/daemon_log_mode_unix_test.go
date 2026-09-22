//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func stubStartCmdFailure(t *testing.T) {
	t.Helper()
	orig := startCmd
	// Stop before exec: Daemonize opens both log files before it starts
	// anything, so the injected failure leaves the files on disk to inspect
	// without ever launching a second daemon on this machine.
	startCmd = func(*exec.Cmd) error { return errors.New("stop before exec") }
	t.Cleanup(func() { startCmd = orig })
}

// TestDaemonizeCreatesOwnerOnlyLogs pins the mode of a log the daemon creates.
// These files carry the access log, invite recipient addresses and the tsnet
// authorization URL, so group and world must have no bits at all -- asserted
// both as an exact mode and as "nothing outside 0600", so a future widening to
// 0640 fails the second check even if someone updates the first.
func TestDaemonizeCreatesOwnerOnlyLogs(t *testing.T) {
	stubStartCmdFailure(t)

	dir := t.TempDir()
	outLog := filepath.Join(dir, "tslink.out.log")
	errLog := filepath.Join(dir, "tslink.err.log")
	if _, err := Daemonize(outLog, errLog, "", false, false, false); err == nil {
		t.Fatal("Daemonize() error = nil, want the injected start error")
	}

	for _, path := range []string{outLog, errLog} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v; Daemonize must create its logs before starting", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", filepath.Base(path), got)
		}
		if extra := info.Mode().Perm() &^ 0o600; extra != 0 {
			t.Fatalf("%s is reachable beyond its owner: extra bits %v", filepath.Base(path), extra)
		}
	}
}

// TestDaemonizeNarrowsPreexistingWideLogs is the upgrade case, and the one
// O_CREATE cannot cover: its mode argument applies only when it creates the
// file, so a log left 0644 by an earlier build stays 0644 forever unless
// something narrows it explicitly.
func TestDaemonizeNarrowsPreexistingWideLogs(t *testing.T) {
	stubStartCmdFailure(t)

	dir := t.TempDir()
	outLog := filepath.Join(dir, "tslink.out.log")
	errLog := filepath.Join(dir, "tslink.err.log")
	for _, path := range []string{outLog, errLog} {
		if err := os.WriteFile(path, []byte("older build wrote this\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("Chmod(%q) error = %v", path, err)
		}
	}

	if _, err := Daemonize(outLog, errLog, "", false, false, false); err == nil {
		t.Fatal("Daemonize() error = nil, want the injected start error")
	}

	for _, path := range []string{outLog, errLog} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("pre-existing %s mode = %v after Daemonize, want 0600", filepath.Base(path), got)
		}
		// The narrowing must not cost the content: an upgrade that truncated
		// the log would be a worse bug than the mode it fixes.
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "older build wrote this\n" {
			t.Fatalf("pre-existing %s content = %q err=%v, want the log preserved", filepath.Base(path), body, err)
		}
	}
}

// TestDaemonizeSurvivesAFailedLogChmod pins the non-fatal half. A daemon that
// refuses to start because it could not tighten a log file leaves the operator
// with no service and no log to diagnose it from.
func TestDaemonizeSurvivesAFailedLogChmod(t *testing.T) {
	stubStartCmdFailure(t)
	origChmod := chmodFile
	calls := 0
	chmodFile = func(*os.File, os.FileMode) error {
		calls++
		return errors.New("injected chmod failure")
	}
	t.Cleanup(func() { chmodFile = origChmod })

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "out.log"), filepath.Join(dir, "err.log"), "", false, false, false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want the injected start error")
	}
	if err.Error() == "injected chmod failure" {
		t.Fatalf("Daemonize() error = %v, want the chmod failure to be non-fatal", err)
	}
	if calls != 2 {
		t.Fatalf("chmod attempts = %d, want one per log file", calls)
	}
}
