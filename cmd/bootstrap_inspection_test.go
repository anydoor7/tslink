package cmd

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func TestBootstrapInspectionChecksCaller(t *testing.T) {
	for _, inspection := range []string{"unregistered", "existing-definition", "supervision", "installer-new", "installer-existing"} {
		for _, state := range []string{"active-control", "expired", "cancelled"} {
			t.Run(inspection+"/"+state, func(t *testing.T) {
				bootstrapIsolateQueryProcess(t)
				path := bootstrapWriteDefinition(t)
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(time.Hour)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time { return now })
				queries := 0
				bootstrapObserveQueries(t, func(queryCtx context.Context) {
					queries++
					session, ok := mcpscope.FromContext(queryCtx)
					if !ok || session.Who != "owner" {
						t.Error("manager query lost caller session")
					}
				})
				if state == "expired" {
					now = expiry
				}
				if state == "cancelled" {
					cancel()
				}
				switch inspection {
				case "unregistered":
					_ = checkUnregisteredSupervisor(ctx)
				case "existing-definition":
					_ = checkBootstrapScope(ctx, path)
				case "supervision":
					_ = detectSupervisionContext(ctx, "", false, 0)
				default:
					bootstrapInspectInstaller(t, ctx, path, inspection == "installer-existing")
				}
				if state == "active-control" && queries == 0 {
					t.Fatal("active inspection did not query manager")
				}
				if state != "active-control" && queries != 0 {
					t.Fatalf("inactive inspection started %d queries", queries)
				}
			})
		}
	}
}

func TestBootstrapSupervisionCallerContext(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-install", true: "after-install"}[installed], func(t *testing.T) {
			bootstrapIsolateQueryProcess(t)
			running := !installed
			isRunningFn = func(string) bool { return running }
			installDaemonFn = func(context.Context, io.Writer) error { running = true; return nil }
			ctx := mcpscope.WithSession(context.Background(), mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}})
			checks := 0
			detectSupervisionFn = func(checkCtx context.Context, _ string, _ bool, _ int) Supervision {
				checks++
				session, ok := mcpscope.FromContext(checkCtx)
				if !ok || session.Who != "owner" {
					t.Error("supervision check lost caller session")
				}
				return Supervision{Manager: "systemd", Installed: true, Autostart: true, RestartOnExit: true}
			}
			bootstrapObserveQueries(t, func(context.Context) {})
			if err := ensureDaemon(ctx, io.Discard, false); err != nil {
				t.Fatal(err)
			}
			if checks == 0 {
				t.Fatal("supervision was not inspected")
			}
		})
	}
}
