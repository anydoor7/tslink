package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
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
			joined := make(chan bool, 1)
			if state == "cancelled" {
				t.Setenv("TSLINK_TEST_MANAGER_WAIT", "yes")
				go func() {
					deadline := time.Now().Add(3 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(marker); err == nil {
							cancel()
							joined <- true
							return
						}
						time.Sleep(time.Millisecond)
					}
					cancel()
					joined <- false
				}()
			}
			started := time.Now()
			_, err := runBoundedManagerCommandContext(ctx, exe, 5*time.Second, "-test.run=^TestManagerCallerContextHelper$")
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
				if !<-joined {
					t.Fatal("child did not reach the real process boundary")
				}
				if !errors.Is(err, context.Canceled) || time.Since(started) > 4*time.Second {
					t.Fatalf("caller cancellation did not end child before 5s manager timeout: %v elapsed=%s", err, time.Since(started))
				}
			}
		})
	}
}
