//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/spf13/cobra"
)

func repairCommand(ctx context.Context) *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(ctx)
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.Flags().Bool("force", false, "")
	c.Flags().Bool("no-auto-provision", false, "")
	return c
}
func repairOperation(ctx context.Context, op string) error {
	switch op {
	case "install":
		return installCmd.RunE(repairCommand(ctx), nil)
	case "uninstall":
		return uninstallCmd.RunE(repairCommand(ctx), nil)
	default:
		return ensureDaemon(ctx, io.Discard, false)
	}
}
func repairAwait(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("transaction deadlocked")
		return nil
	}
}

// A real command pauses inside its manager phase, after definition mutation.
// Peers must cancel without entering capture/manager/removal, then succeed after
// the first transaction finishes. This uses the actual non-reentrant file lock.
func TestRepairSupervisorTransactions(t *testing.T) {
	for _, pair := range [][2]string{{"install", "install"}, {"install", "uninstall"}, {"auto", "install"}, {"uninstall", "install"}} {
		t.Run(pair[0]+"_"+pair[1], func(t *testing.T) {
			var armed atomic.Bool
			var calls atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			setupRepairManager(t, func() {
				calls.Add(1)
				if armed.Load() {
					once.Do(func() { close(entered); <-release })
				}
			})
			if pair[0] == "uninstall" {
				if err := repairOperation(context.Background(), "install"); err != nil {
					t.Fatal(err)
				}
			}
			armed.Store(true)
			first := make(chan error, 1)
			firstCtx, firstCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer firstCancel()
			go func() { first <- repairOperation(firstCtx, pair[0]) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first did not enter manager")
			}
			path, _ := supervisorPath()
			before, _ := os.ReadFile(path)
			count := calls.Load()
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			second := make(chan error, 1)
			go func() { second <- repairOperation(ctx, pair[1]) }()
			var err error
			timedOut := false
			select {
			case err = <-second:
			case <-time.After(5 * time.Second):
				timedOut = true
			}
			cancel()
			after, _ := os.ReadFile(path)
			pausedCalls := calls.Load()
			// Release before fatal assertions so neither goroutine nor fixture escapes.
			close(release)
			if timedOut {
				_ = repairAwait(t, second)
			}
			firstErr := repairAwait(t, first)
			if timedOut {
				t.Fatal("waiting peer ignored cancellation; released owner and drained both commands")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("peer bypassed transaction lock: %v", err)
			}
			if pausedCalls != count {
				t.Fatalf("waiting peer entered manager: %d -> %d", count, pausedCalls)
			}
			if string(before) != string(after) {
				t.Fatal("waiting peer changed supervisor definition")
			}
			// The first command resumes after release; compare the paused count via the
			// peer's canceled completion, before any further transaction starts.
			if firstErr != nil {
				t.Fatalf("first command failed: %v (initial manager calls %d)", firstErr, count)
			}
			if err := repairOperation(context.Background(), pair[1]); err != nil {
				t.Fatalf("positive control after unlock: %v", err)
			}
			_, statErr := os.Stat(path)
			if pair[1] == "uninstall" {
				if !os.IsNotExist(statErr) {
					t.Fatalf("definition remains: %v", statErr)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				dir, _ := absoluteConfigDir()
				if !supervisorConfigMatches(data, dir) {
					t.Fatal("final definition lost config binding")
				}
			}
		})
	}
}

func TestRepairBootstrapRechecksLiveDaemonInsideLock(t *testing.T) {
	isolateBootstrap(t)
	path, _ := supervisorPath()
	var running atomic.Bool
	isRunningFn = func(string) bool { return running.Load() }
	entered := make(chan struct{})
	release := make(chan struct{})
	owner := make(chan error, 1)
	go func() {
		owner <- daemon.WithPIDLock(path+".bootstrap", func() error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	blocked := make(chan error, 1)
	go func() { blocked <- ensureDaemon(ctx, io.Discard, false) }()
	// A canceled waiter must not install after the lock becomes free.
	canceledReturned := false
	select {
	case err := <-blocked:
		canceledReturned = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("waiter: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("waiter ignored cancellation")
	}
	cancel()
	// Start a fresh waiter while the slot is still held. Its lock-internal
	// observation must see the manual daemon that appears before acquisition.
	waiting := make(chan error, 1)
	go func() { waiting <- ensureDaemon(context.Background(), io.Discard, false) }()
	running.Store(true)
	close(release)
	if err := repairAwait(t, owner); err != nil {
		t.Fatal(err)
	}
	if !canceledReturned {
		_ = repairAwait(t, blocked)
	}
	if err := repairAwait(t, waiting); err == nil {
		t.Fatal("manual daemon left by prior transaction was accepted")
	}
}

// The legacy .bootstrap lock is an on-disk per-user protocol shared by all
// command entry points, including requests selecting another config directory.
func TestRepairDirectCommandsUseTransactionLock(t *testing.T) {
	for _, op := range []string{"install", "uninstall"} {
		t.Run(op, func(t *testing.T) {
			setupRepairManager(t, func() {})
			if err := repairOperation(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			path, _ := supervisorPath()
			entered, release := make(chan struct{}), make(chan struct{})
			owner := make(chan error, 1)
			go func() {
				owner <- daemon.WithPIDLock(path+".bootstrap", func() error { close(entered); <-release; return nil })
			}()
			<-entered
			t.Setenv("TSLINK_CONFIG_DIR", t.TempDir())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			err := repairOperation(ctx, op)
			cancel()
			close(release)
			if ownerErr := repairAwait(t, owner); ownerErr != nil {
				t.Fatal(ownerErr)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("per-user lock bypassed by %s with another config: %v", op, err)
			}
			if err := repairOperation(context.Background(), op); err != nil {
				t.Fatalf("positive control: %v", err)
			}
		})
	}
}
