//go:build !windows

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/testwait"
)

// These two tests pin the boundary that separates "registry.json does not exist
// yet" from "registry.json exists and is something registryCheck cannot use".
//
// registryCheck reclassifies the first state as a normal first run so that
// `registry check` and `list` do not answer differently on a fresh install. The
// hazard is in how that question is answered. Answering it by reading the file a
// second time after the first read failed reintroduces the very defect it was
// meant to remove, because os.ReadFile cannot distinguish an absent path from a
// present-but-unreadable one, and because a second open is a second open:
//
//   - a dangling symlink makes both reads return ENOENT, so the second read
//     reports "missing" and the command reports a healthy empty registry, while
//     `list` — which lstats through atomicfile.ConvergePrivateFile — reports an
//     unsafe state file for the identical path;
//   - a FIFO hands its single writer to the first read, so the second open waits
//     for a writer that never arrives and the command never returns.
//
// Both are checked against the shipped binary rather than in-process: the defect
// is observable as a divergence between two CLI commands and as a process that
// does not exit, and neither is a property of a Go function.

// Every run below is bounded by a testwait hang guard. The FIFO failure mode
// is not "slow", it is "never returns", so the assertion has to be a deadline
// and not a duration comparison; a process that blocks on a FIFO open blocks
// forever and will exhaust any bound.

type registryProbeRun struct {
	Stdout   string
	Stderr   string
	ExitCode int
	TimedOut bool
	Elapsed  time.Duration
	Deadline time.Duration
}

func runRegistryFilesystemProbe(t *testing.T, binary, configDir string, args ...string) registryProbeRun {
	t.Helper()
	deadline := testwait.Budget(t)
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(),
		"TSLINK_CONFIG_DIR="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
		testDaemonParentLifetimeEnv+"=1",
	)
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	run := registryProbeRun{Stdout: stdout.String(), Stderr: stderr.String(), Elapsed: time.Since(start), Deadline: deadline}
	if ctx.Err() != nil {
		run.TimedOut = true
		return run
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run tslink %v: %v", args, err)
		}
		run.ExitCode = exitErr.ExitCode()
	}
	return run
}

func soleRegistryProbeEnvelope(t *testing.T, label string, run registryProbeRun) output.Result {
	t.Helper()
	if run.TimedOut {
		t.Fatalf("tslink %s did not return within the %s hang guard", label, run.Deadline)
	}
	if run.Stderr != "" {
		t.Fatalf("tslink %s --json wrote %d bytes to stderr: %q", label, len(run.Stderr), run.Stderr)
	}
	results := parseCompiledJSONLines(t, run.Stdout)
	if len(results) != 1 {
		t.Fatalf("tslink %s --json produced %d envelopes, want 1\nstdout=%s", label, len(results), run.Stdout)
	}
	return results[0]
}

func registryProbeErrorCode(result output.Result) string {
	if result.Error == nil {
		return ""
	}
	return result.Error.Code
}

// assertRegistryCheckAndListAgree requires the two commands to hand an agent the
// same classification of one filesystem state, and requires that shared
// classification to be a failure.
//
// Both halves are load bearing. Agreement alone would be satisfied by both
// commands wrongly calling a broken path a healthy first run, so the failure
// requirement is what stops the fix from being "make list lie too". Agreement is
// what stops the double read from coming back, because the double read moves
// only `registry check`.
//
// Messages are deliberately not compared. The two commands arrive at the state
// through different code and describe it in different words ("no such file or
// directory" versus "unsafe state file ...: symlink"); an agent branches on ok,
// the process exit status and error.code, and those are what diverged.
func assertRegistryCheckAndListAgree(t *testing.T, state string, check, list registryProbeRun) {
	t.Helper()
	checkEnvelope := soleRegistryProbeEnvelope(t, "registry check", check)
	listEnvelope := soleRegistryProbeEnvelope(t, "list", list)

	if checkEnvelope.OK != listEnvelope.OK {
		t.Fatalf("on %s, registry check reported ok=%v but list reported ok=%v; one filesystem state must not be two classifications\n"+
			"  registry check: exit=%d %s\n  list:           exit=%d %s",
			state, checkEnvelope.OK, listEnvelope.OK,
			check.ExitCode, strings.TrimSpace(check.Stdout),
			list.ExitCode, strings.TrimSpace(list.Stdout))
	}
	if checkEnvelope.OK {
		t.Fatalf("on %s, both commands reported ok=true; %s is not the absent-registry first run and must not be reported as one\n"+
			"  registry check: exit=%d %s",
			state, state, check.ExitCode, strings.TrimSpace(check.Stdout))
	}
	if check.ExitCode != list.ExitCode {
		t.Fatalf("on %s, registry check exited %d but list exited %d", state, check.ExitCode, list.ExitCode)
	}
	if got, want := registryProbeErrorCode(checkEnvelope), registryProbeErrorCode(listEnvelope); got != want {
		t.Fatalf("on %s, registry check reported error.code=%q but list reported error.code=%q", state, got, want)
	}
}

// TestCompiledRegistryCheckAndListAgreeOnADanglingRegistrySymlink is the
// symlink half. A registry.json that is a symlink to a nonexistent target is
// present and broken, not absent, and neither command may call it a first run.
func TestCompiledRegistryCheckAndListAgreeOnADanglingRegistrySymlink(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	configDir := t.TempDir()
	registryPath := filepath.Join(configDir, "registry.json")
	if err := os.Symlink(filepath.Join(configDir, "no-such-target.json"), registryPath); err != nil {
		t.Fatalf("create dangling registry.json symlink: %v", err)
	}

	check := runRegistryFilesystemProbe(t, binary, configDir, "registry", "check", registryPath, "--json")
	list := runRegistryFilesystemProbe(t, binary, configDir, "list", "--json")
	assertRegistryCheckAndListAgree(t, "a dangling registry.json symlink", check, list)

	// A read-only validator must not have followed, replaced or materialised
	// the link.
	info, err := os.Lstat(registryPath)
	if err != nil {
		t.Fatalf("lstat registry.json after the probes: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("registry.json is no longer a symlink after the probes (mode=%v)", info.Mode())
	}
	if _, err := os.Stat(registryPath); !os.IsNotExist(err) {
		t.Fatalf("a read-only command created the symlink target (stat err = %v)", err)
	}
}

// TestCompiledRegistryCheckDoesNotBlockOnAFifoRegistry is the liveness half.
//
// The FIFO carries exactly one writer, which is what a single-read
// implementation consumes. An implementation that reads a second time to explain
// the first read's failure parks in open(2) waiting for a second writer that
// this test never supplies, and is caught by the deadline rather than by a
// wrong answer.
func TestCompiledRegistryCheckDoesNotBlockOnAFifoRegistry(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	configDir := t.TempDir()
	registryPath := filepath.Join(configDir, "registry.json")
	if err := syscall.Mkfifo(registryPath, 0o600); err != nil {
		t.Fatalf("create FIFO registry.json: %v", err)
	}

	// One writer: its open(2) blocks until a reader opens, then it sends blank
	// content and closes. Blank rather than valid JSON so that the command still
	// takes the failure path where the classification happens.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		f, err := os.OpenFile(registryPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = f.Write([]byte(" "))
		_ = f.Close()
	}()
	t.Cleanup(func() {
		// Release a writer still parked in open(2) so that a failing run cannot
		// leave a goroutine on the FIFO for the rest of the package run.
		if f, err := os.OpenFile(registryPath, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		select {
		case <-writerDone:
		case <-time.After(testwait.Budget(t)):
			t.Log("the FIFO writer was still parked in open(2): the command under test never read the FIFO")
		}
	})

	check := runRegistryFilesystemProbe(t, binary, configDir, "registry", "check", registryPath, "--json")
	if check.TimedOut {
		t.Fatalf("tslink registry check did not return within the %s hang guard on a FIFO registry.json; "+
			"it consumed the single writer on its first read and is waiting for a second one",
			check.Deadline)
	}
	// A command that never opened the FIFO leaves the writer parked forever.
	testwait.Recv(t, writerDone, "FIFO writer released by registry check reading the FIFO")

	// `list` reaches the same state through atomicfile.ConvergePrivateFile,
	// which lstats and refuses a non-regular file without opening it, so it
	// needs no writer of its own.
	list := runRegistryFilesystemProbe(t, binary, configDir, "list", "--json")
	assertRegistryCheckAndListAgree(t, "a FIFO registry.json", check, list)

	t.Logf("registry check returned in %s (hang guard %s)", check.Elapsed.Round(time.Millisecond), check.Deadline)
}
