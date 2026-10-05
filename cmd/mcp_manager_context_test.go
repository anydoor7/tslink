package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestManagerCallerContextHelper(t *testing.T) {
	marker := os.Getenv("TSLINK_TEST_MANAGER_MARKER")
	if marker == "" {
		t.Skip("subprocess helper")
	}
	if err := os.WriteFile(marker, []byte("started"), 0600); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TSLINK_TEST_MANAGER_WAIT") == "yes" {
		time.Sleep(time.Hour)
	}
}

// A real child process proves cancellation is inherited after execution starts;
// an already-expired policy must never start that process at all.
func TestReview2ManagerCallerContext(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			t.Setenv("TSLINK_TEST_MANAGER_MARKER", marker)
			t.Setenv("TSLINK_TEST_MANAGER_WAIT", "")
			current := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := current.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return current })
			if state == "expired" {
				current = expiry
			}
			if state == "cancelled" {
				t.Setenv("TSLINK_TEST_MANAGER_WAIT", "yes")
			}
			// The manager deadline is set past any hang guard, so in the cancelled
			// state only the caller's cancellation can end the hour-long child.
			// Startup speed of the child process is not part of the property.
			beyondHangGuard := 2 * testwait.MaxBudget
			done := make(chan error, 1)
			go func() {
				_, err := runBoundedManagerCommandContext(ctx, exe, beyondHangGuard, "-test.run=^TestManagerCallerContextHelper$")
				done <- err
			}()
			if state == "cancelled" {
				testwait.Until(t, "child reached the real process boundary", func() bool { _, err := os.Stat(marker); return err == nil })
				cancel()
			}
			err := testwait.Recv(t, done, "bounded manager command returned")
			_, statErr := os.Stat(marker)
			switch state {
			case "active-control":
				if err != nil || statErr != nil {
					t.Fatalf("positive control: %v %v", err, statErr)
				}
			case "expired":
				if err == nil || !os.IsNotExist(statErr) {
					t.Fatalf("expired policy started child: %v %v", err, statErr)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("caller cancellation did not end the child: %v", err)
				}
			}
		})
	}
}
