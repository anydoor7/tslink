package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
)

// TestRemoveDaemonRunningPredicateDefaultImplementation tests the seam's own
// body rather than a stub of it.
//
// Every other test in this package replaces removeDaemonRunningFn, so the
// default composition -- resolve the PID path, ask daemon.IsRunning, and answer
// true when the path itself cannot be resolved -- had no coverage at all. A
// body rewritten to `return false` passed the whole suite while changing the
// production meaning to "delete the node state even though a daemon is running",
// which is the unsafe direction.
//
// The first two cases run the real daemon.IsRunning against a real PID file in
// a real config directory; only the unresolvable-path case needs a stub,
// because config.Dir() answers for every environment.
func TestRemoveDaemonRunningPredicateDefaultImplementation(t *testing.T) {
	t.Run("no pid file means no daemon", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv(config.ConfigDirEnv, configDir)
		if _, err := os.Stat(filepath.Join(configDir, "tslink.pid")); !os.IsNotExist(err) {
			t.Fatalf("fixture is wrong: a PID file already exists (%v)", err)
		}
		if removeDaemonRunningFn() {
			t.Fatal("removeDaemonRunningFn() = true with no PID file, want false")
		}
	})

	t.Run("a live pid file means a daemon", func(t *testing.T) {
		// A PID file written by this test process is rejected: the daemon
		// identity check requires the process argv to name `serve`, and a go
		// test binary's does not. So this uses the package's existing daemon
		// identity fixture -- a purpose-built binary that writes a real PID
		// file and then blocks on stdin. It opens no listener, contacts no
		// control plane, and lives entirely inside a temporary config dir.
		configDir := t.TempDir()
		t.Setenv(config.ConfigDirEnv, configDir)

		fixture := exec.Command(compiledDaemonIdentityFixture(t), "serve")
		fixture.Env = append(os.Environ(), config.ConfigDirEnv+"="+configDir, "TSLINK_DISABLE_KEYRING=1")
		fixtureInput, err := fixture.StdinPipe()
		if err != nil {
			t.Fatalf("create daemon fixture input: %v", err)
		}
		fixtureOutput, err := fixture.StdoutPipe()
		if err != nil {
			t.Fatalf("create daemon fixture output: %v", err)
		}
		var fixtureStderr bytes.Buffer
		fixture.Stderr = &fixtureStderr
		if err := fixture.Start(); err != nil {
			t.Fatalf("start daemon identity fixture: %v", err)
		}
		t.Cleanup(func() {
			_ = fixtureInput.Close()
			_ = fixture.Wait()
		})
		ready, readyErr := bufio.NewReader(fixtureOutput).ReadString('\n')
		if readyErr != nil || ready != "ready\n" {
			t.Fatalf("daemon identity fixture ready=%q err=%v stderr=%q", ready, readyErr, fixtureStderr.String())
		}

		pidPath, err := config.PIDPath()
		if err != nil {
			t.Fatalf("config.PIDPath() error = %v", err)
		}
		if _, err := os.Stat(pidPath); err != nil {
			t.Fatalf("Stat(%q) error = %v, want the fixture's PID file", pidPath, err)
		}
		if !removeDaemonRunningFn() {
			t.Fatal("removeDaemonRunningFn() = false while a daemon identity holds the PID file, want true")
		}
	})

	t.Run("an unresolvable pid path is treated as running", func(t *testing.T) {
		old := removeDaemonPIDPathFn
		removeDaemonPIDPathFn = func() (string, error) { return "", errors.New("injected config dir failure") }
		t.Cleanup(func() { removeDaemonPIDPathFn = old })

		// Pessimistic on purpose: a wrong "not running" deletes state underneath
		// a live node, a wrong "running" only leaves a directory behind.
		if !removeDaemonRunningFn() {
			t.Fatal("removeDaemonRunningFn() = false when the PID path is unresolvable, want true")
		}
	})
}
