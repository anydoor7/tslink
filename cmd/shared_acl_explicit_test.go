package cmd

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

// Automatic reconciliation preserves the shared grant. An operator can still
// deliberately revoke it through the existing force + manage-acl command.
func TestTagsDeleteRemoteExplicitForceCanRevokeCanonicalFunnelGrant(t *testing.T) {
	setTagsMocks(t)
	mockDefaults()
	mockRegistryWithServices(nil)
	tagsDeleteTagFn = tailapi.DeleteTag
	fake := testenv.NewStatefulTailnet(t)
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	fake.SetDevices([]tailscale.Device{{ID: "foreign-id", NodeID: "foreign-node", Hostname: "foreign", Tags: []string{registry.FunnelTag}}})
	fake.SetPolicy(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:tsmain"]},"nodeAttrs":[{"target":["tag:tslink-funnel"],"attr":["funnel"]}]}`)
	var out bytes.Buffer
	if err := tagsDeleteRemoteRun(context.Background(), &out, registry.FunnelTag, true, true, false); err != nil {
		t.Fatal(err)
	}
	after, _ := fake.Policy()
	if strings.Contains(after, registry.FunnelTag) || len(fake.Devices()) != 1 {
		t.Fatalf("explicit global deletion did not remove only the shared grant: policy=%q devices=%+v", after, fake.Devices())
	}
	posts := 0
	for _, request := range fake.Requests() {
		if request.Method == http.MethodPost && request.Path == testenv.TailnetPolicyPath {
			posts++
		}
	}
	if posts != 1 || !strings.Contains(out.String(), "globally") {
		t.Fatalf("explicit force path did not report and perform one global write: posts=%d output=%q", posts, out.String())
	}
}
