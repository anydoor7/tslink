//go:build !windows

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeSupervisedLog(t *testing.T, logDir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(logDir, name)
	if err := os.WriteFile(path, []byte("a line the supervisor already wrote\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%q) error = %v", path, err)
	}
	return path
}

// runStderrLogRotationOnce starts the rotation wiring, lets it perform its
// startup work, and stops it. The rotate seam is stubbed to a no-op so only the
// startup narrowing is under test.
func runStderrLogRotationOnce(t *testing.T, logDir string) {
	t.Helper()
	stubStderrLogRotation(t, logDir, nil)
	stderrLogFileFn = func() *os.File { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := startStderrLogRotation(ctx)
	cancel()
	<-done
}

// TestStartStderrLogRotationNarrowsSupervisorCreatedLogs is the launchd and
// systemd case: the supervisor opened the files, so nothing in internal/daemon
// ever touched their mode, and they arrive 0644. Both sources are asserted,
// because the plist names StandardOutPath as well as StandardErrorPath and a
// fix that narrowed only the one this file is named after would leave the other
// world-readable.
func TestStartStderrLogRotationNarrowsSupervisorCreatedLogs(t *testing.T) {
	logDir := t.TempDir()
	errLog := writeSupervisedLog(t, logDir, "tslink.err.log", 0o644)
	outLog := writeSupervisedLog(t, logDir, "tslink.out.log", 0o644)

	runStderrLogRotationOnce(t, logDir)

	for _, path := range []string{errLog, outLog} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %v after daemon startup, want 0600", filepath.Base(path), got)
		}
		if extra := info.Mode().Perm() &^ 0o600; extra != 0 {
			t.Fatalf("%s is reachable beyond its owner: extra bits %v", filepath.Base(path), extra)
		}
		// Narrowing must not cost the content the operator is about to read.
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "a line the supervisor already wrote\n" {
			t.Fatalf("%s content = %q err=%v, want the existing log preserved", filepath.Base(path), body, err)
		}
	}
}

// TestStartStderrLogRotationLeavesNarrowerLogsAlone is the control for the
// direction of the change: the startup step narrows, it does not set. Without
// this row, "chmod every log to 0600" would pass the test above identically.
func TestStartStderrLogRotationLeavesNarrowerLogsAlone(t *testing.T) {
	logDir := t.TempDir()
	readOnly := writeSupervisedLog(t, logDir, "tslink.err.log", 0o400)

	runStderrLogRotationOnce(t, logDir)

	info, err := os.Stat(readOnly)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o400 {
		t.Fatalf("mode = %v, want the already-narrower 0400 left alone", got)
	}
}

// TestStartStderrLogRotationSurvivesAFailedLogChmod pins the non-fatal half. A
// daemon that aborted here would leave the operator with no service and no log
// to diagnose it from -- the file this step is about.
func TestStartStderrLogRotationSurvivesAFailedLogChmod(t *testing.T) {
	logDir := t.TempDir()
	errLog := writeSupervisedLog(t, logDir, "tslink.err.log", 0o644)

	oldChmod := chmodLogFileFn
	attempts := 0
	chmodLogFileFn = func(string, os.FileMode) error {
		attempts++
		return errors.New("injected chmod failure")
	}
	t.Cleanup(func() { chmodLogFileFn = oldChmod })

	runStderrLogRotationOnce(t, logDir)

	if attempts == 0 {
		t.Fatal("chmod was never attempted; the narrowing did not run at all")
	}
	info, err := os.Stat(errLog)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("mode = %v, want the file untouched after the injected failure", got)
	}
}

// TestNarrowSupervisedLogModesIgnoresAbsentAndNonRegularPaths covers the two
// ordinary states that must not produce a chmod or a failure: a log the
// supervisor has not created yet, and a path that is not a file at all.
func TestNarrowSupervisedLogModesIgnoresAbsentAndNonRegularPaths(t *testing.T) {
	logDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(logDir, "tslink.out.log"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	oldChmod := chmodLogFileFn
	var chmodded []string
	chmodLogFileFn = func(path string, mode os.FileMode) error {
		chmodded = append(chmodded, path)
		return os.Chmod(path, mode)
	}
	t.Cleanup(func() { chmodLogFileFn = oldChmod })

	narrowSupervisedLogModes(logDir)

	if len(chmodded) != 0 {
		t.Fatalf("chmod called for %v, want no call for an absent file or a directory", chmodded)
	}
	info, err := os.Stat(filepath.Join(logDir, "tslink.out.log"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("directory mode = %v, want it untouched", got)
	}

	// Control: a real 0644 log in the same directory is narrowed, so the
	// assertions above are about those two states and not about the function
	// having done nothing at all.
	errLog := writeSupervisedLog(t, logDir, "tslink.err.log", 0o644)
	narrowSupervisedLogModes(logDir)
	info, err = os.Stat(errLog)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("control log mode = %v, want 0600", got)
	}
}

// TestStartStderrLogRotationNarrowsRotatedArchives covers the file nothing
// else can reach. An archive is written once and never reopened, so a .1 left
// world-readable by a build that predated the 0600 cap stays that way for as
// long as it exists -- and it holds more history than the live log beside it,
// now including the account behind every request.
func TestStartStderrLogRotationNarrowsRotatedArchives(t *testing.T) {
	logDir := t.TempDir()
	errArchive := writeSupervisedLog(t, logDir, "tslink.err.log.1", 0o644)
	outArchive := writeSupervisedLog(t, logDir, "tslink.out.log.1", 0o644)

	runStderrLogRotationOnce(t, logDir)

	for _, path := range []string{errArchive, outArchive} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %v after daemon startup, want 0600", filepath.Base(path), got)
		}
		body, err := os.ReadFile(path)
		if err != nil || string(body) != "a line the supervisor already wrote\n" {
			t.Fatalf("%s content = %q err=%v, want the archived log preserved", filepath.Base(path), body, err)
		}
	}
}

// TestNarrowSupervisedLogModesTouchesOnlyTheDaemonsOwnLogs is the control for
// the archive row above: widening the set of paths this function chmods is a
// step toward chmodding whatever else shares the directory, and every file
// here is one the operator put there.
func TestNarrowSupervisedLogModesTouchesOnlyTheDaemonsOwnLogs(t *testing.T) {
	logDir := t.TempDir()
	bystanders := []string{
		"tslink.err.log.2",
		"tslink.err.log.bak",
		"notes.txt",
		"tslink.log",
	}
	for _, name := range bystanders {
		writeSupervisedLog(t, logDir, name, 0o644)
	}

	oldChmod := chmodLogFileFn
	var chmodded []string
	chmodLogFileFn = func(path string, mode os.FileMode) error {
		chmodded = append(chmodded, filepath.Base(path))
		return os.Chmod(path, mode)
	}
	t.Cleanup(func() { chmodLogFileFn = oldChmod })

	narrowSupervisedLogModes(logDir)

	if len(chmodded) != 0 {
		t.Fatalf("chmod called for %v, want only the daemon's own log files touched", chmodded)
	}

	// Control: the two names this function does own, in the same directory,
	// are narrowed -- so the assertion above is about the selection and not
	// about the function having skipped the directory entirely.
	writeSupervisedLog(t, logDir, "tslink.err.log", 0o644)
	writeSupervisedLog(t, logDir, "tslink.err.log.1", 0o644)
	narrowSupervisedLogModes(logDir)
	if len(chmodded) != 2 {
		t.Fatalf("chmod called for %v, want the live log and its archive", chmodded)
	}
}
