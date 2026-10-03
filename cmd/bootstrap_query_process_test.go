package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// The platform adapter below preserves the production query executor's context
// behavior while substituting only the executable with a marked test child.
func TestBootstrapQueryProcessCancellation(t *testing.T) {
	for _, state := range []string{"active-control", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			bootstrapIsolateQueryProcess(t)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "query-started")
			t.Setenv("TSLINK_TEST_MANAGER_MARKER", marker)
			t.Setenv("TSLINK_TEST_MANAGER_WAIT", "")
			if state == "cancelled" {
				t.Setenv("TSLINK_TEST_MANAGER_WAIT", "yes")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}})
			installDaemonFn = func(context.Context, io.Writer) error { return errors.New("fixture ends before installation") }
			bootstrapSetQueryProcess(t, exe)
			cancelled := make(chan time.Time, 1)
			if state == "cancelled" {
				go func() {
					deadline := time.Now().Add(5 * time.Second)
					for time.Now().Before(deadline) {
						if _, err := os.Stat(marker); err == nil {
							when := time.Now()
							cancel()
							cancelled <- when
							return
						}
						time.Sleep(time.Millisecond)
					}
					cancel()
					cancelled <- time.Time{}
				}()
			}
			err = ensureDaemon(ctx, io.Discard, false)
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Fatalf("query child not started: %v %v", err, statErr)
			}
			if state == "cancelled" {
				when := <-cancelled
				if when.IsZero() {
					t.Fatal("no child-start marker before cancellation")
				}
				delay := time.Since(when)
				t.Logf("post-cancellation return delay=%s; error=%v", delay, err)
				if delay > time.Second {
					t.Fatalf("bootstrap manager query ignored caller cancellation for %s", delay)
				}
			}
		})
	}
}
