//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

// mcpSignalChildEnv re-enters this test file as a child process running the
// `tslink mcp` command body. Only a child may receive the signal: if the
// command installs no handler, the default disposition kills whoever gets it.
const (
	mcpSignalChildEnv = "TSLINK_TEST_MCP_SIGNAL_CHILD"
	mcpSignalDirEnv   = "TSLINK_TEST_MCP_SIGNAL_DIR"
)

const mcpSignalShareCall = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000"}}}`

// runMCPSignalChild is the child side. It reports through its exit status and
// stderr because a t.Fatal here would only reach the child's own output.
func runMCPSignalChild(mode, dir string) {
	var actions mcpActions
	switch mode {
	case "rollback":
		// The real share path, with only the daemon and URL seams stubbed: the
		// share is registered, then waits for a URL that never comes.
		shareIsRunningFn = func(string) bool { return true }
		shareResolveEndpointOnceFn = func(_ context.Context, _, _, _, name string) (serviceURLResolution, error) {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		sharePollableStatusFn = func(_ context.Context, _, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }
		actions = defaultMCPActions(sharePaths{
			Registry:    filepath.Join(dir, "registry.json"),
			Ownership:   filepath.Join(dir, "node-ownership.json"),
			PID:         filepath.Join(dir, "tslink.pid"),
			Snapshot:    filepath.Join(dir, "runtime.json"),
			AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
		}, io.Discard)
	case "stuck-grace", "stuck-second-signal":
		// A handler that ignores cancellation altogether.
		mcpCancelGrace = 300 * time.Millisecond
		if mode == "stuck-second-signal" {
			mcpCancelGrace = time.Minute
		}
		actions = fakeMCPActions()
		actions.share = func(context.Context, shareRequest) (ShareResult, error) {
			_ = os.WriteFile(filepath.Join(dir, "started"), nil, 0o600)
			select {}
		}
	default:
		os.Exit(2)
	}
	err := runMCPCommand(context.Background(), os.Stdin, io.Discard, actions)
	fmt.Fprintf(os.Stderr, "child-result: %v\n", err)
	if err == nil {
		os.Exit(3)
	}
	os.Exit(0)
}

type mcpSignalChild struct {
	cmd    *exec.Cmd
	dir    string
	stdin  io.WriteCloser
	stderr *bytes.Buffer
	exited chan error
}

func startMCPSignalChild(t *testing.T, testName, mode string) *mcpSignalChild {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	// TMPDIR keeps whatever the child's TestMain creates inside this test's
	// temp dir, since the child leaves through os.Exit.
	cmd.Env = append(os.Environ(), mcpSignalChildEnv+"="+mode, mcpSignalDirEnv+"="+dir, "TMPDIR="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	child := &mcpSignalChild{cmd: cmd, dir: dir, stdin: stdin, stderr: &bytes.Buffer{}, exited: make(chan error, 1)}
	cmd.Stderr = child.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { child.exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = stdin.Close()
	})
	// stdin stays open: an MCP host that is still connected.
	if _, err := io.WriteString(stdin, initializedMCPInput(mcpSignalShareCall)); err != nil {
		t.Fatal(err)
	}
	return child
}

func (c *mcpSignalChild) waitFor(t *testing.T, what string, ready func() bool) {
	t.Helper()
	// Real child process; the deadline is a hang guard that keeps stderr in
	// the failure message.
	deadline := time.Now().Add(testwait.Budget(t))
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("child never reached %s; stderr: %s", what, c.stderr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *mcpSignalChild) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := c.cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

// wait returns the child's exit. The exit status and stderr are the
// assertions; the hang guard only stops a child that never exits (a guard of
// the product's own 5s grace used to race that grace).
func (c *mcpSignalChild) wait(t *testing.T) syscall.WaitStatus {
	t.Helper()
	limit := testwait.Budget(t)
	select {
	case err := <-c.exited:
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		return c.cmd.ProcessState.Sys().(syscall.WaitStatus)
	case <-time.After(limit):
		t.Fatalf("child still running %v after the signal; stderr: %s", limit, c.stderr)
		return 0
	}
}

func mcpSignalChildMode(t *testing.T) {
	if mode := os.Getenv(mcpSignalChildEnv); mode != "" {
		runMCPSignalChild(mode, os.Getenv(mcpSignalDirEnv))
		t.Fatal("unreachable: the child exits")
	}
}

// TestMCPSignalCancelsTheSessionAndRollsBackAShare is the case the signal
// handling exists for: a share registered by this session and still waiting
// for its URL is rolled back by executeShare's deferred cleanup, which only
// runs if SIGTERM becomes cancellation instead of killing the process.
func TestMCPSignalCancelsTheSessionAndRollsBackAShare(t *testing.T) {
	mcpSignalChildMode(t)
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			child := startMCPSignalChild(t, "TestMCPSignalCancelsTheSessionAndRollsBackAShare", "rollback")
			regPath := filepath.Join(child.dir, "registry.json")
			child.waitFor(t, "a registered share", func() bool {
				reg, err := registry.Load(regPath)
				return err == nil && len(reg.Services) == 1
			})
			child.signal(t, sig)
			status := child.wait(t)
			t.Logf("child stderr: %s", child.stderr)
			if status.Signaled() {
				t.Fatalf("%v killed tslink mcp instead of cancelling it; the share it registered was left behind", status.Signal())
			}
			if status.ExitStatus() != 0 || !strings.Contains(child.stderr.String(), "child-result: mcp stdio:") || !strings.Contains(child.stderr.String(), "signal") {
				t.Fatalf("exit status %d, stderr %q; want the session to end with the signal as its error", status.ExitStatus(), child.stderr)
			}
			reg, err := registry.Load(regPath)
			if err != nil || len(reg.Services) != 0 {
				t.Fatalf("share was not rolled back after %v: %+v err=%v", sig, reg, err)
			}
		})
	}
}

// TestMCPSignalHardExitsWhenAHandlerIgnoresCancellation covers the fallbacks:
// after the first signal a stuck handler gets a short grace, and a second
// signal terminates the process at once.
func TestMCPSignalHardExitsWhenAHandlerIgnoresCancellation(t *testing.T) {
	mcpSignalChildMode(t)
	started := func(child *mcpSignalChild) func() bool {
		return func() bool {
			_, err := os.Stat(filepath.Join(child.dir, "started"))
			return err == nil
		}
	}

	t.Run("grace after the first signal", func(t *testing.T) {
		child := startMCPSignalChild(t, "TestMCPSignalHardExitsWhenAHandlerIgnoresCancellation", "stuck-grace")
		child.waitFor(t, "the stuck handler", started(child))
		child.signal(t, syscall.SIGTERM)
		status := child.wait(t)
		if status.Signaled() || status.ExitStatus() != 0 || !strings.Contains(child.stderr.String(), "child-result: mcp stdio:") {
			t.Fatalf("signaled=%v exit=%d stderr=%q; want the session to give up on the handler and return", status.Signaled(), status.ExitStatus(), child.stderr)
		}
	})

	t.Run("second signal", func(t *testing.T) {
		child := startMCPSignalChild(t, "TestMCPSignalHardExitsWhenAHandlerIgnoresCancellation", "stuck-second-signal")
		child.waitFor(t, "the stuck handler", started(child))
		child.signal(t, syscall.SIGTERM)
		select {
		case <-child.exited:
			t.Fatalf("the first SIGTERM ended tslink mcp outright; stderr: %s", child.stderr)
		case <-time.After(300 * time.Millisecond):
		}
		child.signal(t, syscall.SIGTERM)
		status := child.wait(t)
		if !status.Signaled() || status.Signal() != syscall.SIGTERM {
			t.Fatalf("signaled=%v exit=%d; want the second SIGTERM to terminate the process", status.Signaled(), status.ExitStatus())
		}
	})
}
