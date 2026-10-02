//go:build linux

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
	_, _ = captureSystemdPreviousState(ctx, path)
}

func TestBootstrapLingerChecksCaller(t *testing.T) {
	for _, state := range []string{"active-control", "expired", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			isolateBootstrap(t)
			oldQuery, oldUser := loginctlCombinedOutputFn, linuxUserNameFn
			t.Cleanup(func() { loginctlCombinedOutputFn, linuxUserNameFn = oldQuery, oldUser })
			linuxUserNameFn = func() string { return "fixture" }
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return now })
			queries := 0
			loginctlCombinedOutputFn = func(queryCtx context.Context, _ ...string) ([]byte, error) {
				queries++
				if _, ok := mcpscope.FromContext(queryCtx); !ok {
					t.Error("linger query lost caller session")
				}
				return []byte("yes\n"), nil
			}
			if state == "expired" {
				now = expiry
			}
			if state == "cancelled" {
				cancel()
			}
			got := linuxLingerState(ctx)
			if state == "active-control" {
				if queries != 1 || got != lingerEnabled {
					t.Fatalf("active linger queries=%d state=%v", queries, got)
				}
			} else if queries != 0 || got != lingerUnknown {
				t.Fatalf("inactive linger queries=%d state=%v", queries, got)
			}
		})
	}
}
