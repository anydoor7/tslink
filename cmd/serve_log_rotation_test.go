package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/logrotate"
)

// stubStderrLogRotation replaces every seam this file touches and restores them
// from t.Cleanup, so a failing test cannot leave the package's daemon path
// pointing at a recorder.
func stubStderrLogRotation(t *testing.T, logDir string, rotate func(*os.File, string, int64) (logrotate.Result, error)) {
	t.Helper()
	oldRotate, oldFile, oldInterval, oldMax, oldLogDir, oldRepeat :=
		stderrLogRotateFn, stderrLogFileFn, stderrLogRotateInterval, stderrLogMaxBytes, serveLogDirFn,
		stderrLogDegradedRepeatInterval
	t.Cleanup(func() {
		stderrLogRotateFn = oldRotate
		stderrLogFileFn = oldFile
		stderrLogRotateInterval = oldInterval
		stderrLogMaxBytes = oldMax
		serveLogDirFn = oldLogDir
		stderrLogDegradedRepeatInterval = oldRepeat
	})
	serveLogDirFn = func() (string, error) { return logDir, nil }
	stderrLogRotateInterval = 5 * time.Millisecond
	if rotate != nil {
		stderrLogRotateFn = rotate
	}
}

type rotationCall struct {
	file     *os.File
	target   string
	maxBytes int64
}

// TestStartStderrLogRotationChecksTheConfiguredLogOnATimer pins the three
// things the wiring decides: which file, which descriptor, and that it keeps
// checking rather than only checking once.
func TestStartStderrLogRotationChecksTheConfiguredLogOnATimer(t *testing.T) {
	logDir := t.TempDir()
	var mu sync.Mutex
	var calls []rotationCall
	observed := make(chan struct{}, 8)

	stubStderrLogRotation(t, logDir, func(f *os.File, target string, maxBytes int64) (logrotate.Result, error) {
		mu.Lock()
		calls = append(calls, rotationCall{file: f, target: target, maxBytes: maxBytes})
		mu.Unlock()
		select {
		case observed <- struct{}{}:
		default:
		}
		return logrotate.Result{Reason: "under the size cap"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := startStderrLogRotation(ctx)

	for i := 0; i < 2; i++ {
		select {
		case <-observed:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d rotation checks ran; the timer is not running", i)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rotation goroutine did not stop with its context")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("recorded %d checks, want at least 2", len(calls))
	}
	want := filepath.Join(logDir, "tslink.err.log")
	for i, call := range calls {
		if call.target != want {
			t.Fatalf("check %d targeted %q, want %q", i, call.target, want)
		}
		if call.file != os.Stderr {
			t.Fatalf("check %d was handed %v, want this process's stderr", i, call.file)
		}
		if call.maxBytes != logrotate.DefaultMaxBytes {
			t.Fatalf("check %d cap = %d, want %d", i, call.maxBytes, logrotate.DefaultMaxBytes)
		}
	}
}

// TestStartStderrLogRotationRunsOnceBeforeTheFirstTick covers the daemon that
// starts against an already-oversized log. Waiting out a full interval first
// would mean a restart loop never rotates at all.
func TestStartStderrLogRotationRunsOnceBeforeTheFirstTick(t *testing.T) {
	logDir := t.TempDir()
	first := make(chan time.Duration, 1)
	start := time.Now()

	stubStderrLogRotation(t, logDir, func(*os.File, string, int64) (logrotate.Result, error) {
		select {
		case first <- time.Since(start):
		default:
		}
		return logrotate.Result{Reason: "under the size cap"}, nil
	})
	stderrLogRotateInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startStderrLogRotation(ctx)

	select {
	case elapsed := <-first:
		if elapsed > 30*time.Second {
			t.Fatalf("the first check took %s; it waited for the ticker", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no check ran before the one-hour ticker; an oversized log would stay oversized")
	}
	cancel()
	<-done
}

// TestStartStderrLogRotationStopsWhenTheLogDirIsUnresolvable keeps a broken
// config from turning into a goroutine nobody closes.
func TestStartStderrLogRotationStopsWhenTheLogDirIsUnresolvable(t *testing.T) {
	stubStderrLogRotation(t, t.TempDir(), func(*os.File, string, int64) (logrotate.Result, error) {
		t.Fatal("rotation ran despite an unresolvable log directory")
		return logrotate.Result{}, nil
	})
	serveLogDirFn = func() (string, error) { return "", os.ErrPermission }

	select {
	case <-startStderrLogRotation(context.Background()):
	case <-time.After(5 * time.Second):
		t.Fatal("startStderrLogRotation neither ran nor returned")
	}
}

// TestStartStderrLogRotationWritesNothingWhenStderrIsNotTheConfiguredLog is the
// side-effect guard, and it runs the real rotate function rather than a stub.
//
// A green test suite says nothing about whether the code under test wrote to
// disk. This one asserts it: the daemon path now performs a disk-writing check
// on a timer, every existing test that reaches runForegroundWithOptions runs it,
// and this process's stderr is whatever `go test` handed it -- a pipe, a
// terminal, or an operator's redirect. None of those may be touched, and the
// log directory must stay empty.
func TestStartStderrLogRotationWritesNothingWhenStderrIsNotTheConfiguredLog(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logDir := t.TempDir()
		stubStderrLogRotation(t, logDir, nil) // real logrotate.RotateStderrLog

		ctx, cancel := context.WithCancel(context.Background())
		done := startStderrLogRotation(ctx)
		time.Sleep(50 * time.Millisecond) // ten virtual ticks
		synctest.Wait()
		cancel()
		<-done

		entries, err := os.ReadDir(logDir)
		if err != nil {
			t.Fatalf("read the isolated log dir: %v", err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			t.Fatalf("the rotation timer created %v in the isolated log dir; against a real config dir those writes land in the operator's logs", names)
		}
	})
}

// TestStartStderrLogRotationRotatesTheConfiguredLog is the one end-to-end case,
// with a descriptor this test owns standing in for the one launchd supplies.
func TestStartStderrLogRotationRotatesTheConfiguredLog(t *testing.T) {
	logDir := t.TempDir()
	target := filepath.Join(logDir, "tslink.err.log")
	if err := os.WriteFile(target, make([]byte, 4096), 0o600); err != nil {
		t.Fatalf("seed the log: %v", err)
	}
	handle, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open the log O_APPEND: %v", err)
	}
	defer handle.Close()

	stubStderrLogRotation(t, logDir, nil) // real logrotate.RotateStderrLog
	stderrLogFileFn = func() *os.File { return handle }
	stderrLogMaxBytes = 1024

	ctx, cancel := context.WithCancel(context.Background())
	done := startStderrLogRotation(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, err := os.Stat(target); err == nil && info.Size() == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("the configured log was never truncated")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	archived, err := os.Stat(target + logrotate.ArchiveSuffix)
	if err != nil {
		t.Fatalf("stat the archive: %v", err)
	}
	if archived.Size() != 4096 {
		t.Fatalf("archive size = %d, want the 4096 bytes that were rotated away", archived.Size())
	}
}

// TestRunForegroundBoundsTheDaemonStderrLog is the wiring pin. Every other test
// in this file calls startStderrLogRotation directly, so all of them stay green
// if the call disappears from the daemon path -- which is the only place it has
// any effect.
func TestRunForegroundBoundsTheDaemonStderrLog(t *testing.T) {
	logDir := t.TempDir()
	checked := make(chan string, 4)
	stubStderrLogRotation(t, logDir, func(_ *os.File, target string, _ int64) (logrotate.Result, error) {
		select {
		case checked <- target:
		default:
		}
		return logrotate.Result{Reason: "under the size cap"}, nil
	})
	// Only the startup check may fire, so a pass cannot come from the ticker
	// happening to run during an unrelated wait.
	stderrLogRotateInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runForegroundWithInjectedContext(t, ctx, context.Canceled); err != nil {
		t.Fatalf("runForeground() error = %v, want a clean shutdown", err)
	}

	select {
	case target := <-checked:
		if want := filepath.Join(logDir, "tslink.err.log"); target != want {
			t.Fatalf("the daemon bounded %q, want %q", target, want)
		}
	default:
		t.Fatal("the daemon path never bounded its stderr log; startStderrLogRotation is not wired into runForegroundWithOptions")
	}
}

// captureSlog redirects the default slog logger into a buffer for the duration
// of the test and returns a reader for what was written.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var buf bytes.Buffer
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, buf: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
}

// lockedWriter exists because the rotation goroutine writes while the test
// reads; without it the race detector fails the whole suite rather than this
// assertion.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// runRotationUntil starts the rotation goroutine, waits for calls of the stub,
// then stops it and returns everything the daemon logged.
func runRotationUntil(t *testing.T, calls int, result logrotate.Result, rotateErr error) string {
	t.Helper()
	logDir := t.TempDir()
	observed := make(chan struct{}, calls*4)
	stubStderrLogRotation(t, logDir, func(*os.File, string, int64) (logrotate.Result, error) {
		select {
		case observed <- struct{}{}:
		default:
		}
		return result, rotateErr
	})
	read := captureSlog(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := startStderrLogRotation(ctx)
	for i := 0; i < calls; i++ {
		select {
		case <-observed:
		case <-time.After(5 * time.Second):
			cancel()
			<-done
			t.Fatalf("only %d rotation checks ran", i)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the rotation goroutine did not stop with its context")
	}
	return read()
}

// TestStartStderrLogRotationKeepsRestatingADegradedDecision is the negative
// case for a degraded rotation. The log-once-per-change policy is right for the
// steady state and wrong for the one state that matters: the operator deleted
// the log, rotation refused, and after a single line the daemon's own log looks
// exactly like a healthy daemon under the size cap for as long as the process
// lives.
//
// The control is in the same test and is the load-bearing half of the pair: an
// ordinary under-cap skip must still be said once and never again, or this fix
// has simply traded a silent failure for the per-tick noise the file exists to
// avoid.
func TestStartStderrLogRotationKeepsRestatingADegradedDecision(t *testing.T) {
	const degradedReason = "the configured log file no longer exists; it is still being written to"

	t.Run("degraded is restated", func(t *testing.T) {
		stderrLogDegradedRepeatInterval = 0
		logged := runRotationUntil(t, 4, logrotate.Result{Degraded: true, Reason: degradedReason}, nil)
		if n := strings.Count(logged, degradedReason); n < 3 {
			t.Fatalf("the decision was stated %d times across 4 checks; a degraded rotation must stay audible:\n%s", n, logged)
		}
		if !strings.Contains(logged, "level=WARN") {
			t.Fatalf("a degradation was logged below WARN:\n%s", logged)
		}
	})

	t.Run("a healthy skip is stated once", func(t *testing.T) {
		stderrLogDegradedRepeatInterval = 0
		logged := runRotationUntil(t, 4, logrotate.Result{Reason: "under the size cap"}, nil)
		if n := strings.Count(logged, "under the size cap"); n != 1 {
			t.Fatalf("an unchanged healthy decision was stated %d times, want exactly 1:\n%s", n, logged)
		}
	})
}

// TestStartStderrLogRotationDeduplicatesRepeatedRotationErrors is the negative
// case for a repeated rotation error. The error branch used to log every
// tick with no deduplication at all, so a log directory turned read-only
// produced a Warn every five minutes
// -- this function feeding the growth it exists to bound, which is the exact
// shape its own comment names as the thing to avoid.
func TestStartStderrLogRotationDeduplicatesRepeatedRotationErrors(t *testing.T) {
	stderrLogDegradedRepeatInterval = time.Hour
	logged := runRotationUntil(t, 4, logrotate.Result{}, errors.New("truncate the log through its own descriptor: read-only file system"))

	n := strings.Count(logged, "stderr log rotation failed")
	if n != 1 {
		t.Fatalf("a persistent error was logged %d times across 4 checks, want exactly 1 inside the repeat window:\n%s", n, logged)
	}
	// The one line it does print has to carry the fact that it will not repeat
	// soon, or a reader takes the single line for a transient blip.
	if !strings.Contains(logged, "repeat_after") {
		t.Fatalf("the deduplicated error does not say when it will be restated:\n%s", logged)
	}
}
