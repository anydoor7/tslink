package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/testwait"
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
			queries := bootstrapSetQueryProcess(t, exe)
			done := make(chan error, 1)
			go func() { done <- ensureDaemon(ctx, io.Discard, false) }()
			if state == "cancelled" {
				testwait.Until(t, "bootstrap manager query child started", func() bool { _, err := os.Stat(marker); return err == nil })
				cancel()
			}
			err = testwait.Recv(t, done, "ensureDaemon returned")
			if _, statErr := os.Stat(marker); statErr != nil {
				t.Fatalf("query child not started: %v %v", err, statErr)
			}
			if state == "cancelled" {
				// The child sleeps for an hour, so the query ends either by the
				// caller's cancellation (Canceled) or by the product's own
				// managerQueryTimeout (DeadlineExceeded). Only the first honors it.
				var queryErr error
				select {
				case queryErr = <-queries:
				default:
					t.Fatal("no manager query ran")
				}
				t.Logf("query error=%v; ensureDaemon error=%v", queryErr, err)
				if !errors.Is(queryErr, context.Canceled) {
					t.Fatalf("bootstrap manager query ignored caller cancellation: %v", queryErr)
				}
			}
		})
	}
}
