package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// expireShareFunnel moves a registered share's Funnel deadline into the past,
// and optionally lets the daemon's reconcile downgrade it to tailnet-only the
// way the serve ticker does in production.
func expireShareFunnel(t *testing.T, regPath, name string, downgrade bool) time.Time {
	t.Helper()
	past := time.Now().Add(-time.Minute).UTC()
	if _, err := registry.MutateService(regPath, name, func(svc registry.Service) (registry.Service, error) {
		svc.FunnelExpiresAt = &past
		return svc, nil
	}); err != nil {
		t.Fatal(err)
	}
	if downgrade {
		expired, err := registry.DowngradeExpiredFunnels(regPath, time.Now(), false)
		if err != nil || len(expired) != 1 {
			t.Fatalf("downgrade = %+v err=%v", expired, err)
		}
	}
	return past
}

// TestShareReuseRearmsAnExpiredFunnelDeadline covers re-running an identical
// Funnel share after its deadline passed. The existing deadline is expired,
// so the reuse check must not accept it as is (the "not expired" half of the
// deadline predicate), and the retry re-arms it with the requested TTL, as
// `add` re-arms an expired preserved deadline, instead of failing with a
// conflict whose only remedy is remove.
func TestShareReuseRearmsAnExpiredFunnelDeadline(t *testing.T) {
	for _, tc := range []struct {
		name         string
		secondTTL    string
		secondSet    bool
		downgrade    bool
		wantRearm    bool
		wantDuration time.Duration
	}{
		{"identical request", "1h", true, false, true, time.Hour},
		{"identical request after the daemon downgraded it", "1h", true, true, true, time.Hour},
		{"default TTL request", "", false, false, true, registry.DefaultFunnelTTL},
		{"longer TTL request", "72h", true, false, true, 72 * time.Hour},
		{"never is a different posture", "never", true, false, false, 0},
		{"never after downgrade is a different posture", "never", true, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.json")
			first := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true})
			original, created, err := registerShare(path, first, "")
			if err != nil || !created {
				t.Fatalf("initial: %+v created=%v err=%v", original, created, err)
			}
			past := expireShareFunnel(t, path, original.Name, tc.downgrade)
			before, err := registry.Load(path)
			if err != nil {
				t.Fatal(err)
			}

			second := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: tc.secondTTL, FunnelTTLSet: tc.secondSet})
			outcome, err := registerShareWithOutcome(path, second, "")
			stored, loadErr := registry.Load(path)
			if loadErr != nil || len(stored.Services) != 1 {
				t.Fatalf("registry = %+v err=%v", stored, loadErr)
			}
			if !tc.wantRearm {
				if err == nil || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "funnel deadline") {
					t.Fatalf("want a funnel deadline conflict, got err=%v outcome=%+v", err, outcome)
				}
				if !sameDeadline(stored.Services[0].FunnelExpiresAt, before.Services[0].FunnelExpiresAt) || stored.Services[0].Funnel != before.Services[0].Funnel {
					t.Fatalf("a refused share changed the registry: %+v", stored.Services[0])
				}
				return
			}
			if err != nil || outcome.Created || !outcome.FunnelRearmed || outcome.Service.Name != original.Name {
				t.Fatalf("want the expired share re-armed and reused, got err=%v created=%v rearmed=%v name=%q", err, outcome.Created, outcome.FunnelRearmed, outcome.Service.Name)
			}
			got := stored.Services[0]
			if !got.Funnel || got.FunnelExpiresAt == nil || !got.FunnelExpiresAt.Equal(*second.Service.FunnelExpiresAt) || got.FunnelExpiresAt.Equal(past) {
				t.Fatalf("stored service = %+v, want Funnel on with the requested deadline %v", got, second.Service.FunnelExpiresAt)
			}
			if remaining := time.Until(*got.FunnelExpiresAt); remaining < tc.wantDuration-time.Minute || remaining > tc.wantDuration {
				t.Fatalf("re-armed deadline is %v away, want about %v", remaining, tc.wantDuration)
			}
			if !outcome.Service.FunnelExpiresAt.Equal(*got.FunnelExpiresAt) || !outcome.Service.Funnel {
				t.Fatalf("returned service %+v does not match the stored one %+v", outcome.Service, got)
			}
		})
	}

	// Only the Funnel deadline is re-armed: any other difference in posture is
	// still a conflict and leaves the expired entry alone.
	path := filepath.Join(t.TempDir(), "registry.json")
	first := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true})
	original, _, err := registerShare(path, first, "")
	if err != nil {
		t.Fatal(err)
	}
	expireShareFunnel(t, path, original.Name, false)
	private := shareIntentForRegression(t, shareRequest{Allow: []string{"person@example.com"}})
	if _, err := registerShareWithOutcome(path, private, ""); err == nil || output.ExitCode(err) != output.ExitConflict {
		t.Fatalf("different posture on an expired share: err=%v, want a conflict", err)
	}
	if stored, err := registry.Load(path); err != nil || len(stored.Services) != 1 || !stored.Services[0].FunnelExpiresAt.Before(time.Now()) {
		t.Fatalf("a refused share changed the registry: %+v err=%v", stored, err)
	}
}

func TestMCPShareReportsARearmedFunnel(t *testing.T) {
	actions, regPath := shareMCPWireActions(t)
	const args = `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`
	first := callMCPShare(t, actions, args)
	if _, ok := first["funnel_rearmed"]; ok {
		t.Fatalf("a newly created share reported funnel_rearmed: %v", first)
	}
	expireShareFunnel(t, regPath, "port-3000", true)
	again := callMCPShare(t, actions, args)
	if again["name"] != "port-3000" || again["funnel_rearmed"] != true {
		t.Fatalf("retry after expiry = %v, want the same share with funnel_rearmed true", again)
	}
	raw, _ := again["funnel_expires_at"].(string)
	deadline, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || time.Until(deadline) < 59*time.Minute {
		t.Fatalf("funnel_expires_at = %v (%v), want the re-armed 1h deadline", again["funnel_expires_at"], err)
	}
}
