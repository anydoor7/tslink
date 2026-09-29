package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// TestTagsDeleteRemoteFunnelTagRefusesWhileLocalFunnelIsActive is R5 probe P2
// kept as a regression test (R4-7/R5-2). The Funnel tag is derived from
// svc.Funnel and never persisted in svc.Tags, so the in-use scan did not see
// it: `tags delete-remote tag:tslink-funnel --force --manage-acl` deleted the
// tailnet-wide grant while this host still served an acknowledged Funnel.
// The expired and private cases are the control group: a Funnel past its
// deadline is tailnet-only (registry.EffectiveServiceAt) and does not hold
// the tag, and an ordinary registry does not either.
func TestTagsDeleteRemoteFunnelTagRefusesWhileLocalFunnelIsActive(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name       string
		service    registry.Service
		isJSON     bool
		wantRefuse bool
	}{
		{name: "acknowledged Funnel", service: registry.Service{Name: "public", Type: "proxy", Target: "localhost:3000", Funnel: true, PublicAck: true, Tags: []string{"tag:tsmain"}}, wantRefuse: true},
		{name: "acknowledged Funnel json", service: registry.Service{Name: "public", Type: "proxy", Target: "localhost:3000", Funnel: true, PublicAck: true, Tags: []string{"tag:tsmain"}}, isJSON: true, wantRefuse: true},
		{name: "Funnel before its deadline", service: registry.Service{Name: "public", Type: "proxy", Target: "localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &future}, wantRefuse: true},
		{name: "Funnel past its deadline", service: registry.Service{Name: "public", Type: "proxy", Target: "localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &past}},
		{name: "private service", service: registry.Service{Name: "private", Type: "proxy", Target: "localhost:3000", Tags: []string{"tag:tsmain"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setTagsMocks(t)
			mockDefaults()
			mockRegistryWithServices([]registry.Service{tc.service})
			deleted := ""
			tagsDeleteTagFn = func(_ context.Context, tag string) error { deleted = tag; return nil }
			var buf bytes.Buffer
			err := tagsDeleteRemoteRun(context.Background(), &buf, registry.FunnelTag, true, true, tc.isJSON)
			if !tc.wantRefuse {
				if err != nil || deleted != registry.FunnelTag {
					t.Fatalf("err=%v deleted=%q, want the explicit deletion to proceed", err, deleted)
				}
				return
			}
			want := `cannot delete "` + registry.FunnelTag + `" — in use by services: ` + tc.service.Name
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err=%v deleted=%q out=%q, want the in-use conflict %q even with --force", err, deleted, buf.String(), want)
			}
			if deleted != "" {
				t.Fatalf("deleted %q from the tailnet ACL while a local Funnel still uses it", deleted)
			}
		})
	}
}
