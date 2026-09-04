package tailapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// CleanupTargetForOwnedService builds the only structure that can authorize a
// remote DELETE. These tests pin the two properties that decide whether the
// right device is removed: the NodeID proof must be an independent copy, and
// hostname/tags must survive as discovery-only fields.

func TestCleanupTargetForOwnedServiceCopiesNodeIDProofFromCaller(t *testing.T) {
	svc := registry.Service{Name: "web", Tags: []string{"tag:web"}}
	callerIDs := []string{"nodeid-fake-owned"}

	target := CleanupTargetForOwnedService(svc, callerIDs)

	// A caller that reuses its slice must not be able to retarget an already
	// built DELETE authorization.
	callerIDs[0] = "nodeid-fake-someone-elses-device"

	if len(target.NodeIDs) != 1 || target.NodeIDs[0] != "nodeid-fake-owned" {
		t.Fatalf("target.NodeIDs = %v, want the proof captured at construction time", target.NodeIDs)
	}
}

func TestCleanupTargetForOwnedServiceAppendToCallerSliceCannotWidenAuthorization(t *testing.T) {
	svc := registry.Service{Name: "web"}
	callerIDs := make([]string, 1, 4) // spare capacity: an aliasing bug would share the array
	callerIDs[0] = "nodeid-fake-owned"

	target := CleanupTargetForOwnedService(svc, callerIDs)
	callerIDs = append(callerIDs, "nodeid-fake-unowned")

	if len(target.NodeIDs) != 1 {
		t.Fatalf("target.NodeIDs = %v, want exactly one authorized node", target.NodeIDs)
	}
	if cap(target.NodeIDs) > 1 && &target.NodeIDs[:1][0] == &callerIDs[:1][0] {
		t.Fatal("target.NodeIDs aliases the caller slice; later appends could widen DELETE authorization")
	}
	_ = callerIDs
}

func TestCleanupTargetForOwnedServiceKeepsServiceHostnameAndTags(t *testing.T) {
	svc := registry.Service{Name: "docs", Tags: []string{"tag:docs", "tag:shared"}}

	target := CleanupTargetForOwnedService(svc, []string{"nodeid-fake-docs"})

	if target.Hostname != "docs" {
		t.Fatalf("target.Hostname = %q, want the service name so orphan discovery still matches", target.Hostname)
	}
	if got := strings.Join(target.Tags, ","); got != "tag:docs,tag:shared" {
		t.Fatalf("target.Tags = %q, want the service tags", got)
	}
	if err := validateCleanupTarget(target); err != nil {
		t.Fatalf("validateCleanupTarget() error = %v, want a valid target", err)
	}
}

func TestCleanupTargetForOwnedServiceWithoutProofAuthorizesNoDelete(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	deleteCalled := false
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return jsonResponse(http.StatusOK, `{"devices":[{"nodeId":"nodeid-fake-live","hostname":"web"}]}`), nil
		}
		deleteCalled = true
		return jsonResponse(http.StatusOK, `{}`), nil
	}))

	// An empty proof list is what a service with no recorded ownership yields.
	target := CleanupTargetForOwnedService(registry.Service{Name: "web"}, nil)
	if len(target.NodeIDs) != 0 {
		t.Fatalf("target.NodeIDs = %v, want no authorization without recorded proof", target.NodeIDs)
	}

	result, err := DeleteDevicesForService(context.Background(), target)
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}
	if deleteCalled {
		t.Fatal("hostname-only target issued a DELETE; ownership proof is not gating deletion")
	}
	if strings.Join(result.Protected, ",") != "web" {
		t.Fatalf("result.Protected = %v, want the hostname match reported as protected", result.Protected)
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("result.Deleted = %v, want none", result.Deleted)
	}
}

func TestCleanupTargetForOwnedServiceAuthorizesOnlyItsRecordedNodeIDs(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deletePaths []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet:
			return jsonResponse(http.StatusOK, `{"devices":[
				{"nodeId":"nodeid-fake-owned","hostname":"renamed-web"},
				{"nodeId":"nodeid-fake-unowned","hostname":"web"},
				{"nodeId":"nodeid-fake-suffix","hostname":"web-1"}
			]}`), nil
		case req.Method == http.MethodDelete:
			deletePaths = append(deletePaths, req.URL.Path)
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	svc := registry.Service{Name: "web", Tags: []string{"tag:web"}}
	target := CleanupTargetForOwnedService(svc, []string{"nodeid-fake-owned"})

	result, err := DeleteDevicesForService(context.Background(), target)
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}
	if got := strings.Join(deletePaths, ","); got != "/api/v2/device/nodeid-fake-owned" {
		t.Fatalf("DELETE paths = %q, want only the node ID carried by the built target", got)
	}
	if got := strings.Join(result.Deleted, ","); got != "renamed-web" {
		t.Fatalf("result.Deleted = %q, want the renamed device that carries the recorded proof", got)
	}
	if got := strings.Join(result.Protected, ","); got != "web,web-1" {
		t.Fatalf("result.Protected = %q, want hostname collisions protected", got)
	}
}

func TestCleanupTargetForOwnedServiceCarriesProofForAnOrphanWhoseHostnameNoLongerMatches(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deletePaths []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet:
			return jsonResponse(http.StatusOK, `{"devices":[{"nodeId":"nodeid-fake-orphan","hostname":"totally-different"}]}`), nil
		case req.Method == http.MethodDelete:
			deletePaths = append(deletePaths, req.URL.Path)
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	// The production repair case: the orphan's hostname drifted, so only the
	// recorded NodeID can ever reach it.
	target := CleanupTargetForOwnedService(registry.Service{Name: "web"}, []string{"nodeid-fake-orphan"})

	result, err := DeleteDevicesForService(context.Background(), target)
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}
	if got := strings.Join(deletePaths, ","); got != "/api/v2/device/nodeid-fake-orphan" {
		t.Fatalf("DELETE paths = %q, want the orphan reachable through its recorded node ID", got)
	}
	if got := strings.Join(result.ResolvedOwnershipIDs, ","); got != "nodeid-fake-orphan" {
		t.Fatalf("result.ResolvedOwnershipIDs = %q, want the consumed proof reported so the ledger row can be forgotten", got)
	}
}
