package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/tailscale/hujson"
	tailscale "tailscale.com/client/tailscale/v2"
)

type findExactDeviceNodeIDContractFunc func(context.Context, string) (string, int, error)
type deleteDevicesContractFunc func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error)
type cleanupDevicesContractFunc func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error)
type ensureTagsContractFunc func(context.Context, []string) error
type deleteTagContractFunc func(context.Context, string) error
type ensureFunnelAttrContractFunc func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error)
type adoptOwnedNodeContractFunc func(string, string, string, time.Time, bool) error
type previewAdoptOwnedNodeContractFunc func(string, string, string, time.Time, bool) (tsruntime.OwnershipLedger, error)

var (
	_ findExactDeviceNodeIDContractFunc = tailapi.FindExactDeviceNodeID
	_ deleteDevicesContractFunc         = tailapi.DeleteDevicesForService
	_ cleanupDevicesContractFunc        = tailapi.CleanupStaleNodesResult
	_ ensureTagsContractFunc            = tailapi.EnsureTags
	_ deleteTagContractFunc             = tailapi.DeleteTag
	_ ensureFunnelAttrContractFunc      = tailapi.EnsureFunnelAttr
	_ adoptOwnedNodeContractFunc        = tsruntime.AdoptOwnedNode
	_ previewAdoptOwnedNodeContractFunc = tsruntime.PreviewAdoptOwnedNode
)

func useContractTailnet(t *testing.T, fake *testenv.StatefulTailnet) {
	t.Helper()
	t.Setenv(tailapi.APIBaseURLEnv, fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
}

type exactLookupContractResult struct {
	nodeID  string
	matches int
	err     error
}

func TestContractCleanupFindExactDeviceNodeIDFnMatchesReal(t *testing.T) {
	tests := []struct {
		name       string
		devices    []tailscale.Device
		fakeNodeID string
		want       exactLookupContractResult
	}{
		{
			name: "one exact tagged match returns its node ID",
			devices: []tailscale.Device{
				{NodeID: "node-contract-one", Hostname: "legacy", Tags: []string{"tag:tslink-contract"}},
			},
			fakeNodeID: "node-contract-one",
			want:       exactLookupContractResult{nodeID: "node-contract-one", matches: 1},
		},
		{
			name: "two exact tagged matches never select one node ID",
			devices: []tailscale.Device{
				{NodeID: "node-contract-first", Hostname: "legacy", Tags: []string{"tag:tslink-contract"}},
				{NodeID: "node-contract-second", Hostname: "legacy", Tags: []string{"tag:tslink-contract"}},
			},
			fakeNodeID: "node-contract-first",
			want:       exactLookupContractResult{matches: 2},
		},
		{
			name: "no exact match returns an empty shape",
			devices: []tailscale.Device{
				{NodeID: "node-contract-other", Hostname: "legacy-1", Tags: []string{"tag:tslink-contract"}},
			},
			fakeNodeID: "node-contract-other",
			want:       exactLookupContractResult{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			remote := testenv.NewStatefulTailnet(t)
			remote.SetDevices(tc.devices)
			useContractTailnet(t, remote)
			fake := cleanupExactLookupFake{nodeID: tc.fakeNodeID, matches: tc.want.matches, err: tc.want.err}
			var fakeFn findExactDeviceNodeIDContractFunc = fake.Find

			fakeNodeID, fakeMatches, fakeErr := fakeFn(context.Background(), "legacy")
			realNodeID, realMatches, realErr := tailapi.FindExactDeviceNodeID(context.Background(), "legacy")
			if fakeNodeID != realNodeID || fakeMatches != realMatches || errorText(fakeErr) != errorText(realErr) {
				t.Fatalf("fake=(%q,%d,%v) real=(%q,%d,%v)", fakeNodeID, fakeMatches, fakeErr, realNodeID, realMatches, realErr)
			}
		})
	}
}

func TestContractDeleteDevicesFnMatchesRealExactOwnershipAuthorization(t *testing.T) {
	remote := testenv.NewStatefulTailnet(t)
	useContractTailnet(t, remote)
	remote.SetDevices([]tailscale.Device{
		{NodeID: "node-contract-owned", Hostname: "svc"},
		{NodeID: "node-contract-unowned", Hostname: "svc-1"},
	})
	target := tailapi.CleanupTarget{Hostname: "svc", NodeIDs: []string{"node-contract-owned"}}
	want := tailapi.CleanupResult{
		Matched:              []string{"svc", "svc-1"},
		Deleted:              []string{"svc"},
		Protected:            []string{"svc-1"},
		Skipped:              true,
		SkipReason:           "matched tailnet devices require exact TSLink ownership proof before deletion",
		ResolvedOwnershipIDs: []string{"node-contract-owned"},
	}
	fake := deleteDevicesContractFake{target: target, result: want}
	var fakeFn deleteDevicesContractFunc = fake.Delete

	fakeResult, fakeErr := fakeFn(context.Background(), target)
	realResult, realErr := tailapi.DeleteDevicesForService(context.Background(), target)
	if !reflect.DeepEqual(fakeResult, realResult) || errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake=(%+v,%v) real=(%+v,%v)", fakeResult, fakeErr, realResult, realErr)
	}
	remaining := remote.Devices()
	if len(remaining) != 1 || remaining[0].NodeID != "node-contract-unowned" {
		t.Fatalf("remaining devices = %+v, want only unowned hostname match", remaining)
	}
}

func TestContractServeCleanupFnMatchesRealPartialDeleteShape(t *testing.T) {
	remote := testenv.NewStatefulTailnet(t)
	useContractTailnet(t, remote)
	remote.SetDevices([]tailscale.Device{
		{NodeID: "node-contract-first", Hostname: "first"},
		{NodeID: "node-contract-second", Hostname: "second"},
	})
	remote.FailNext(http.MethodDelete, testenv.TailnetDevicePath("node-contract-second"), http.StatusForbidden)
	targets := []tailapi.CleanupTarget{
		{Hostname: "first", NodeIDs: []string{"node-contract-first"}},
		{Hostname: "second", NodeIDs: []string{"node-contract-second"}},
	}
	wantResult := tailapi.CleanupResult{
		Matched:              []string{"first", "second"},
		Deleted:              []string{"first"},
		ResolvedOwnershipIDs: []string{"node-contract-first"},
	}
	wantErr := errors.New("delete TSLink-owned device failed")
	fake := cleanupDevicesContractFake{targets: targets, result: wantResult, err: wantErr}
	var fakeFn cleanupDevicesContractFunc = fake.Cleanup

	fakeResult, fakeErr := fakeFn(context.Background(), targets)
	realResult, realErr := tailapi.CleanupStaleNodesResult(context.Background(), targets)
	if !reflect.DeepEqual(fakeResult, realResult) || errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake=(%+v,%v) real=(%+v,%v)", fakeResult, fakeErr, realResult, realErr)
	}
	remaining := remote.Devices()
	if len(remaining) != 1 || remaining[0].NodeID != "node-contract-second" {
		t.Fatalf("remaining devices = %+v, want failed second delete retained", remaining)
	}
}

type ensureTagsContractFake struct {
	tagOwners map[string][]string
}

func (f *ensureTagsContractFake) Ensure(_ context.Context, tags []string) error {
	for _, tag := range tags {
		if _, exists := f.tagOwners[tag]; !exists {
			f.tagOwners[tag] = []string{"autogroup:admin"}
		}
	}
	return nil
}

func TestContractServeEnsureTagsFnMatchesRealPolicyMutation(t *testing.T) {
	remote := testenv.NewStatefulTailnet(t)
	useContractTailnet(t, remote)
	remote.SetPolicy(`{"tagOwners":{"tag:keep":["tag:owner"]}}`)
	tags := []string{"tag:keep", "tag:contract", "tag:contract"}
	fake := &ensureTagsContractFake{tagOwners: map[string][]string{"tag:keep": {"tag:owner"}}}
	var fakeFn ensureTagsContractFunc = fake.Ensure

	fakeErr := fakeFn(context.Background(), tags)
	realErr := tailapi.EnsureTags(context.Background(), tags)
	if errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake error = %v, real error = %v", fakeErr, realErr)
	}
	realOwners := policyTagOwners(t, remote)
	if !reflect.DeepEqual(fake.tagOwners, realOwners) {
		t.Fatalf("fake tagOwners = %+v, real tagOwners = %+v", fake.tagOwners, realOwners)
	}
}

type deleteTagContractFake struct {
	tagOwners map[string][]string
}

func (f *deleteTagContractFake) Delete(_ context.Context, tag string) error {
	if _, exists := f.tagOwners[tag]; !exists {
		return fmt.Errorf("tag %q not found in tailnet ACL", tag)
	}
	delete(f.tagOwners, tag)
	return nil
}

func TestContractTagsDeleteTagFnMatchesRealPolicyDeletion(t *testing.T) {
	remote := testenv.NewStatefulTailnet(t)
	useContractTailnet(t, remote)
	remote.SetPolicy(`{"tagOwners":{"tag:delete":["autogroup:admin"],"tag:keep":["tag:owner"]}}`)
	fake := &deleteTagContractFake{tagOwners: map[string][]string{
		"tag:delete": {"autogroup:admin"},
		"tag:keep":   {"tag:owner"},
	}}
	var fakeFn deleteTagContractFunc = fake.Delete

	fakeErr := fakeFn(context.Background(), "tag:delete")
	realErr := tailapi.DeleteTag(context.Background(), "tag:delete")
	if errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake error = %v, real error = %v", fakeErr, realErr)
	}
	realOwners := policyTagOwners(t, remote)
	if !reflect.DeepEqual(fake.tagOwners, realOwners) {
		t.Fatalf("fake tagOwners = %+v, real tagOwners = %+v", fake.tagOwners, realOwners)
	}
}

type ensureFunnelAttrContractFake struct {
	want    tailapi.FunnelPolicyRequest
	written bool
}

func (f *ensureFunnelAttrContractFake) Ensure(_ context.Context, request tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
	if !reflect.DeepEqual(request, f.want) {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteNotAttempted}, fmt.Errorf("request = %+v, want %+v", request, f.want)
	}
	if f.written {
		return tailapi.PolicyMutationResult{WriteOutcome: tailapi.PolicyWriteUnchanged}, nil
	}
	f.written = true
	return tailapi.PolicyMutationResult{Changed: true, WriteOutcome: tailapi.PolicyWriteChanged}, nil
}

func TestContractServeEnsureFunnelAttrFnMatchesRealWriteOutcomes(t *testing.T) {
	remote := testenv.NewStatefulTailnet(t)
	useContractTailnet(t, remote)
	remote.SetPolicy(`{"tagOwners":{},"nodeAttrs":[]}`)
	remote.SetTailnetSettings(tailscale.TailnetSettings{HTTPSEnabled: true})
	request := tailapi.FunnelPolicyRequest{
		Tags:   []string{"tag:tslink-app", registry.FunnelTag},
		Target: registry.FunnelTag,
		Owners: []string{"tag:tslink-app"},
	}
	fake := &ensureFunnelAttrContractFake{want: request}
	var fakeFn ensureFunnelAttrContractFunc = fake.Ensure

	for call := 1; call <= 2; call++ {
		fakeResult, fakeErr := fakeFn(context.Background(), request)
		realResult, realErr := tailapi.EnsureFunnelAttr(context.Background(), request)
		if !reflect.DeepEqual(fakeResult, realResult) || errorText(fakeErr) != errorText(realErr) {
			t.Fatalf("call %d fake=(%+v,%v) real=(%+v,%v)", call, fakeResult, fakeErr, realResult, realErr)
		}
	}
	policy, _ := remote.Policy()
	standard, err := hujson.Standardize([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		TagOwners map[string][]string `json:"tagOwners"`
		NodeAttrs []struct {
			Target []string `json:"target"`
			Attr   []string `json:"attr"`
		} `json:"nodeAttrs"`
	}
	if err := json.Unmarshal(standard, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.TagOwners["tag:tslink-app"], []string{"autogroup:admin"}) ||
		!reflect.DeepEqual(decoded.TagOwners[registry.FunnelTag], []string{"tag:tslink-app"}) {
		t.Fatalf("tagOwners = %+v, want ordinary and Funnel ownership", decoded.TagOwners)
	}
	if len(decoded.NodeAttrs) != 1 || !reflect.DeepEqual(decoded.NodeAttrs[0].Target, []string{registry.FunnelTag}) ||
		!reflect.DeepEqual(decoded.NodeAttrs[0].Attr, []string{tailapi.FunnelNodeAttr}) {
		t.Fatalf("nodeAttrs = %+v, want exact Funnel grant", decoded.NodeAttrs)
	}
}

func TestContractCleanupAdoptOwnedNodeFnMatchesRealLedgerWrite(t *testing.T) {
	path := t.TempDir() + "/node-ownership.json"
	seedTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := tsruntime.RecordOwnedNode(path, "seed", "node-contract-seed", seedTime); err != nil {
		t.Fatal(err)
	}
	initial, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	fake := &adoptOwnedNodeContractFake{ledger: initial}
	var fakeFn adoptOwnedNodeContractFunc = fake.Adopt
	now := time.Date(2030, 9, 1, 12, 0, 0, 123, time.FixedZone("contract", 3600))

	fakeErr := fakeFn(path, "legacy", "node-contract-adopted", now, true)
	realErr := tsruntime.AdoptOwnedNode(path, "legacy", "node-contract-adopted", now, true)
	if errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake error = %v, real error = %v", fakeErr, realErr)
	}
	realLedger, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.ledger, realLedger) {
		t.Fatalf("fake ledger = %+v, real ledger = %+v", fake.ledger, realLedger)
	}
}

func TestContractCleanupPreviewAdoptOwnedNodeFnMatchesRealWithoutWriting(t *testing.T) {
	path := t.TempDir() + "/node-ownership.json"
	seedTime := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := tsruntime.RecordOwnedNode(path, "seed", "node-contract-seed", seedTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	fake := previewAdoptOwnedNodeContractFake{ledger: initial}
	var fakeFn previewAdoptOwnedNodeContractFunc = fake.Preview
	now := time.Date(2030, 9, 1, 12, 0, 0, 123, time.FixedZone("contract", 3600))

	fakeLedger, fakeErr := fakeFn(path, "legacy", "node-contract-preview", now, true)
	realLedger, realErr := tsruntime.PreviewAdoptOwnedNode(path, "legacy", "node-contract-preview", now, true)
	if !reflect.DeepEqual(fakeLedger, realLedger) || errorText(fakeErr) != errorText(realErr) {
		t.Fatalf("fake=(%+v,%v) real=(%+v,%v)", fakeLedger, fakeErr, realLedger, realErr)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("preview seam changed ledger bytes:\nbefore=%s\nafter=%s", before, after)
	}
}

func policyTagOwners(t *testing.T, remote *testenv.StatefulTailnet) map[string][]string {
	t.Helper()
	policy, _ := remote.Policy()
	standard, err := hujson.Standardize([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		TagOwners map[string][]string `json:"tagOwners"`
	}
	if err := json.Unmarshal(standard, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.TagOwners
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
