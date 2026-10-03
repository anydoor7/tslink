package cmd

import (
	"fmt"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"io"
	"time"
)

func activeGuestViews(reg *registry.Registry, now time.Time) []registry.GuestView {
	out := []registry.GuestView{}
	for _, g := range reg.Guests {
		v := g.View(now)
		if !v.Expired && !v.Revoked {
			out = append(out, v)
		}
	}
	return out
}
func formatGuestViews(out io.Writer, views []registry.GuestView) {
	for _, v := range views {
		fmt.Fprintf(out, "Guest %s: %s (%s), expires %s\n", v.ID, v.App, v.Label, v.ExpiresAt.Format(time.RFC3339))
	}
}
func diagnoseGuests(result *DoctorResult, reg *registry.Registry, now time.Time) {
	result.GuestLinks = activeGuestViews(reg, now)
	for _, v := range result.GuestLinks {
		if v.ExpiresAt.Sub(now) <= 24*time.Hour {
			result.addFinding(inspect.WarningCodeGuestExpiry, v.App, "guest", "Guest link "+v.ID+" expires within 24 hours.", nil)
		}
	}
	snapshot, e := tsruntime.Load(result.Paths.RuntimeSnapshot)
	fp, fpErr := tsruntime.RegistryFingerprint(reg, nil)
	expected := tsruntime.ExpectedRuntime{CurrentRegistryFingerprint: fp, DaemonPID: result.Daemon.PID}
	if lower, err := pidFileModTimeFn(result.Paths.PID); err == nil {
		expected.DaemonStartedAtLowerBound = lower
	}
	current := e == nil && fpErr == nil && result.Daemon.Running && tsruntime.Classify(snapshot, e, expected).Exact
	for _, svc := range reg.Services {
		if !svc.GuestGate {
			continue
		}
		available := false
		if current {
			for _, s := range snapshot.Services {
				if s.Name == svc.Name && s.FunnelActive && s.FunnelState == tsruntime.FunnelStateActive && s.RuntimeState == tsruntime.ServiceRuntimeRunning {
					available = true
				}
			}
		}
		if !available {
			result.addFinding(inspect.WarningCodeGuestFunnel, svc.Name, "guest", "Guest gate configured; Funnel availability is unverified or unavailable.", nil)
		}
	}
}
