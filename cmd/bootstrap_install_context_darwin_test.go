//go:build darwin

package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func bootstrapInspectInstaller(t *testing.T, ctx context.Context, path string, existing bool) {
	t.Helper()
	if !existing {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = captureLaunchAgentPreviousState(ctx, path)
}

func TestBootstrapInspectionChecksEachQuery(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			isolateBootstrap(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return now })
			queries := 0
			bootstrapObserveQueries(t, func(context.Context) {
				queries++
				if state == "expired" {
					now = expiry
				}
				if state == "cancelled" {
					cancel()
				}
			})
			err := checkUnregisteredSupervisor(ctx)
			want := 1
			if state == "active-control" {
				want = 2
			}
			if queries != want || (err != nil) != (state != "active-control") {
				t.Fatalf("queries=%d want=%d error=%v", queries, want, err)
			}
		})
	}
}
