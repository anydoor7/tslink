package tailapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

func useStatefulTailnet(t *testing.T, fake *testenv.StatefulTailnet) {
	t.Helper()
	saveAPIClientState(t)
	t.Setenv(APIBaseURLEnv, fake.URL())
	t.Setenv(apiKeyEnv, "test-placeholder")
	warnAPIBaseURLRedirectFn = func(string) {}
	oldACLClient := aclClientFn
	aclClientFn = newTailscaleClient
	t.Cleanup(func() { aclClientFn = oldACLClient })
}

func TestRealTailAPIUsesStatefulTailnetAcrossEveryCurrentRESTSurface(t *testing.T) {
	setup(t)
	fake := testenv.NewStatefulTailnet(t)
	useStatefulTailnet(t, fake)
	fake.SetDevices([]tailscale.Device{
		{ID: "legacy-owned", NodeID: "node-owned", Hostname: "owned", Tags: []string{"tag:tsmain"}},
		{ID: "legacy-keep", NodeID: "node-keep", Hostname: "keep", Tags: []string{"tag:other"}},
	})
	fake.SetPolicy(`{
		"tagOwners": {"tag:tsmain": ["autogroup:admin"]},
		"nodeAttrs": []
	}`)
	fake.SetTailnetSettings(tailscale.TailnetSettings{HTTPSEnabled: true})

	nodeID, matches, err := FindExactDeviceNodeID(context.Background(), "owned")
	if err != nil || nodeID != "node-owned" || matches != 1 {
		t.Fatalf("FindExactDeviceNodeID() = nodeID:%q matches:%d err:%v", nodeID, matches, err)
	}
	result, err := DeleteDevicesForService(context.Background(), CleanupTarget{
		Hostname: "owned",
		Tags:     []string{"tag:tsmain"},
		NodeIDs:  []string{"node-owned"},
	})
	if err != nil || strings.Join(result.Deleted, ",") != "owned" || strings.Join(result.ResolvedOwnershipIDs, ",") != "node-owned" {
		t.Fatalf("DeleteDevicesForService() = %+v, err=%v", result, err)
	}
	devices := fake.Devices()
	if len(devices) != 1 || devices[0].NodeID != "node-keep" {
		t.Fatalf("stateful devices after real cleanup = %+v", devices)
	}

	tags, err := ReadTags(context.Background())
	if err != nil || strings.Join(tags, ",") != "tag:tsmain" {
		t.Fatalf("ReadTags() = %v, err=%v", tags, err)
	}
	if err := EnsureTags(context.Background(), []string{"tag:extra"}); err != nil {
		t.Fatalf("EnsureTags() error = %v", err)
	}
	policyAfterTags, etagAfterTags := fake.Policy()
	if !strings.Contains(policyAfterTags, "tag:extra") || etagAfterTags == "" {
		t.Fatalf("policy after EnsureTags = %q etag=%q", policyAfterTags, etagAfterTags)
	}

	mutation, err := EnsureFunnelAttr(context.Background(), FunnelPolicyRequest{
		Tags:   []string{"tag:tsmain"},
		Target: registry.FunnelTag,
		Owners: []string{"tag:tsmain"},
	})
	if err != nil || !mutation.Changed || mutation.WriteOutcome != PolicyWriteChanged {
		t.Fatalf("EnsureFunnelAttr() = %+v, err=%v", mutation, err)
	}
	policyAfterFunnel, etagAfterFunnel := fake.Policy()
	if !strings.Contains(policyAfterFunnel, registry.FunnelTag) || !strings.Contains(policyAfterFunnel, FunnelNodeAttr) || etagAfterFunnel == etagAfterTags {
		t.Fatalf("policy after Funnel mutation = %q etag=%q", policyAfterFunnel, etagAfterFunnel)
	}

	for _, request := range fake.Requests() {
		if !request.BasicAuth {
			t.Fatalf("real tailapi request lacked environment-derived basic auth metadata: %+v", request)
		}
	}
}

func TestRealTailAPIPreservesForbiddenConflictTimeoutAndPartialFailureShapes(t *testing.T) {
	setup(t)
	fake := testenv.NewStatefulTailnet(t)
	useStatefulTailnet(t, fake)
	fake.SetPolicy(`{"tagOwners":{}}`)

	fake.FailNext(http.MethodGet, testenv.TailnetPolicyPath, http.StatusForbidden)
	if _, err := ReadTags(context.Background()); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("ReadTags() forbidden error = %v, want HTTP 403", err)
	}

	fake.FailNext(http.MethodPost, testenv.TailnetPolicyPath, http.StatusPreconditionFailed)
	if err := EnsureTags(context.Background(), []string{"tag:conflict"}); !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("EnsureTags() conflict error = %v, want ErrPolicyConflict", err)
	}
	fake.DelayNext(http.MethodGet, testenv.TailnetDevicesPath, 200*time.Millisecond)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, _, err := FindExactDeviceNodeID(timeoutCtx, "timed-out"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("FindExactDeviceNodeID() timeout error = %v, want deadline exceeded", err)
	}

	fake.SetDevices([]tailscale.Device{
		{NodeID: "node-first", Hostname: "first"},
		{NodeID: "node-second", Hostname: "second"},
	})
	fake.FailNext(http.MethodDelete, testenv.TailnetDevicePath("node-second"), http.StatusForbidden)
	result, err := CleanupStaleNodesResult(context.Background(), []CleanupTarget{
		{Hostname: "first", NodeIDs: []string{"node-first"}},
		{Hostname: "second", NodeIDs: []string{"node-second"}},
	})
	if err == nil || strings.Join(result.Deleted, ",") != "first" {
		t.Fatalf("partial cleanup = %+v, err=%v, want first delete then failure", result, err)
	}
	remaining := fake.Devices()
	if len(remaining) != 1 || remaining[0].NodeID != "node-second" {
		t.Fatalf("partial cleanup state = %+v, want failed second device retained", remaining)
	}
}
