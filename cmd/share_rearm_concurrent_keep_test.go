package cmd

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// A second share can rely on a renewal before the renewing call fails.
// Its successful result must settle the renewal, just as it settles creation.
func TestFailedRearmKeepsRenewalUsedByConcurrentShare(t *testing.T) {
	paths, req, _ := expiredFunnelShareFixture(t)
	restoreShareSeams(t)
	shareIsRunningFn = func(string) bool { return false }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		// The second caller reuses the just-renewed registry entry.
		spec, err := inferShareTarget(req.Target, req.Ephemeral)
		if err != nil {
			t.Fatal(err)
		}
		spec, err = applyShareExposure(spec, req)
		if err != nil {
			t.Fatal(err)
		}
		kept, err := registerShareWithOutcome(paths.Registry, spec, "")
		if err != nil || kept.Created || kept.FunnelRearmed || !kept.Service.Funnel {
			t.Fatalf("second caller did not reuse the renewal: %+v, %v", kept, err)
		}
		return shareDaemonStart{}, errors.New("synthetic first caller failure")
	}
	if _, err := executeShare(context.Background(), paths, req, 0, io.Discard); err == nil {
		t.Fatal("first caller failure was lost")
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 1 || !reg.Services[0].Funnel {
		t.Fatalf("failed first caller revoked the second caller's renewal: %+v, %v", reg, err)
	}
}
