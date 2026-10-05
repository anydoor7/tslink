//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestBoundedManagerCommandUsesRealSubprocessAndFiniteDeadline(t *testing.T) {
	out, err := runBoundedManagerCommand(context.Background(), "/bin/sh", time.Second, "-c", "printf ready")
	if err != nil || string(out) != "ready" {
		t.Fatalf("successful helper output = %q, err=%v", out, err)
	}
	out, err = runBoundedManagerCommand(context.Background(), "/bin/sh", time.Second, "-c", "printf rejected >&2; exit 7")
	if err == nil || string(out) != "rejected" {
		t.Fatalf("failed helper output = %q, err=%v", out, err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := runBoundedManagerCommand(context.Background(), "/bin/sleep", 40*time.Millisecond, "30")
		done <- err
	}()
	err = testwait.Recv(t, done, "manager command returned at its deadline")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked helper error = %v, want deadline exceeded", err)
	}
	if managerCommandTimeout("--user", "show", "tslink.service") != managerQueryTimeout ||
		managerCommandTimeout("bootout", "gui/501/com.tslink.daemon") != managerMutationTimeout {
		t.Fatal("query/mutation command budgets were misclassified")
	}
}

func TestBoundedManagerTimeoutReleasesSupervisorTransaction(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	err := withSupervisorTransaction(context.Background(), func() error {
		_, err := runBoundedManagerCommand(context.Background(), "/bin/sleep", 40*time.Millisecond, "30")
		return err
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("blocked helper transaction error = %v", err)
	}
	entered := false
	// Hang guard only: a lock still held would make this wait expire.
	ctx, cancel := context.WithTimeout(context.Background(), testwait.Budget(t))
	defer cancel()
	if err := withSupervisorTransaction(ctx, func() error { entered = true; return nil }); err != nil || !entered {
		t.Fatalf("next transaction could not acquire released lock: entered=%v err=%v", entered, err)
	}
}
