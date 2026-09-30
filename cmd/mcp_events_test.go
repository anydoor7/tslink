package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

const mcpEventAuthURLSentinel = "https://login.tailscale.com/a/EVENT-STREAM-SENTINEL"

func mcpEventTestStatus() mcpStatusSummary {
	return mcpStatusSummary{
		Supervision:            Supervision{Manager: "launchd", Installed: true, Autostart: true},
		Authenticated:          false,
		CredentialStored:       true,
		NodeAuthorized:         false,
		AuthorizedServiceCount: 1,
		DaemonRunning:          true,
		ServiceCount:           2,
		Status:                 authStatusNeedsLogin,
		AuthURL:                mcpEventAuthURLSentinel,
		Next:                   []string{"tslink status"},
	}
}

// TestMCPEventStateOmitsTheAuthURL is a reverse assertion, so it ships with its
// control.
//
// The control is the first half: it proves the sentinel really is present in
// the status result the event is built from, and is therefore findable by the
// same search. Without it, "the sentinel is absent from the event" would also
// pass if the sentinel had been misspelled, if the status fixture had stopped
// carrying it, or if the projection had returned an empty value for unrelated
// reasons — a test that can never go red.
func TestMCPEventStateOmitsTheAuthURL(t *testing.T) {
	status := mcpEventTestStatus()

	sourceEncoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal source status: %v", err)
	}
	if !strings.Contains(string(sourceEncoded), mcpEventAuthURLSentinel) {
		t.Fatalf("control failed: the status tool result does not carry %q, so the assertion below proves nothing", mcpEventAuthURLSentinel)
	}

	state, err := buildMCPEventState(map[string]any{"services": []ListServiceSummary{}}, status)
	if err != nil {
		t.Fatalf("buildMCPEventState() error = %v", err)
	}
	eventEncoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal event state: %v", err)
	}
	if strings.Contains(string(eventEncoded), mcpEventAuthURLSentinel) {
		t.Fatalf("event payload carries the interactive enrollment URL: %s", eventEncoded)
	}
	if strings.Contains(string(eventEncoded), "auth_url") {
		t.Fatalf("event payload declares an auth_url field: %s", eventEncoded)
	}

	// The status the client can act on still arrives; only the bearer value is
	// withheld.
	if state.Status.Status != authStatusNeedsLogin {
		t.Fatalf("event status = %q, want %q so a client can still show the banner", state.Status.Status, authStatusNeedsLogin)
	}
}

// TestMCPEventStateMirrorsTheListAndStatusTools pins the alignment requirement:
// a client holding an event has the same facts a list + status round trip would
// have given it.
func TestMCPEventStateMirrorsTheListAndStatusTools(t *testing.T) {
	url := "https://demo.tail.ts.net"
	services := []ListServiceSummary{
		{Name: "demo", Type: registry.TypeProxy, URL: &url, State: "up", FunnelState: "not_requested"},
		{Name: "pending", Type: registry.TypeFile, URLPending: true, State: listStatePending, FunnelState: "not_requested"},
	}
	status := mcpEventTestStatus()

	state, err := buildMCPEventState(map[string]any{"services": services}, status)
	if err != nil {
		t.Fatalf("buildMCPEventState() error = %v", err)
	}
	if state.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %v, want %v", state.SchemaVersion, inspect.SchemaVersion)
	}
	if len(state.Services) != len(services) {
		t.Fatalf("services = %d, want %d", len(state.Services), len(services))
	}
	for i, want := range services {
		got := state.Services[i]
		if got.Name != want.Name || got.Type != want.Type || got.State != want.State || got.URLPending != want.URLPending {
			t.Fatalf("service[%d] = %+v, want %+v", i, got, want)
		}
		if (got.URL == nil) != (want.URL == nil) {
			t.Fatalf("service[%d] url presence = %v, want %v", i, got.URL != nil, want.URL != nil)
		}
		if got.URL != nil && *got.URL != *want.URL {
			t.Fatalf("service[%d] url = %q, want %q", i, *got.URL, *want.URL)
		}
	}
	if state.Status.DaemonRunning != status.DaemonRunning ||
		state.Status.ServiceCount != status.ServiceCount ||
		state.Status.CredentialStored != status.CredentialStored ||
		state.Status.AuthorizedServiceCount != status.AuthorizedServiceCount ||
		state.Status.Supervision.Manager != status.Supervision.Manager {
		t.Fatalf("event status = %+v, want the status tool's fields: %+v", state.Status, status)
	}
}

// TestMCPEventStateEmitsAnEmptyServiceArray keeps a client's list rendering
// from having to handle null as well as empty.
func TestMCPEventStateEmitsAnEmptyServiceArray(t *testing.T) {
	state, err := buildMCPEventState(map[string]any{"services": nil}, mcpStatusSummary{})
	if err != nil {
		t.Fatalf("buildMCPEventState() error = %v", err)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"services":[]`) {
		t.Fatalf("payload = %s, want an empty services array", encoded)
	}
}

// TestMCPEventStateSanitizesServiceErrorText is the second reverse assertion
// and carries the same shape of control: the unsanitized error is checked to
// contain the secret before the projection is checked not to.
func TestMCPEventStateSanitizesServiceErrorText(t *testing.T) {
	const secret = "tskey-auth-EVENTSTREAMSECRET"
	failing := ListServiceSummary{
		Name:  "broken",
		Type:  registry.TypeProxy,
		State: "error",
		Error: &tsruntime.ServiceError{
			Code:    "service_start_failed",
			Message: "auth key " + secret + " rejected for https://user:hunter2@backend.example.com/path?token=abc",
			Next:    []string{"retry with " + secret},
			Provision: &registry.ProvisionOutcome{
				Attempted: true,
				Reason:    "policy write rejected using " + secret,
			},
		},
	}

	sourceEncoded, err := json.Marshal(map[string]any{"services": []ListServiceSummary{failing}})
	if err != nil {
		t.Fatalf("marshal source list: %v", err)
	}
	if strings.Count(string(sourceEncoded), secret) != 3 {
		t.Fatalf("control failed: the list result carries the secret %d times, want 3; the assertion below would prove nothing", strings.Count(string(sourceEncoded), secret))
	}

	state, err := buildMCPEventState(map[string]any{"services": []ListServiceSummary{failing}}, mcpStatusSummary{})
	if err != nil {
		t.Fatalf("buildMCPEventState() error = %v", err)
	}
	eventEncoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal event state: %v", err)
	}
	if strings.Contains(string(eventEncoded), secret) {
		t.Fatalf("event payload leaked the auth key: %s", eventEncoded)
	}
	if strings.Contains(string(eventEncoded), "hunter2") {
		t.Fatalf("event payload leaked URL userinfo: %s", eventEncoded)
	}
	// The diagnosis survives the redaction; only the values are replaced.
	if state.Services[0].Error == nil || state.Services[0].Error.Code != "service_start_failed" {
		t.Fatalf("event payload dropped the error code: %+v", state.Services[0].Error)
	}

	// The caller's own value is untouched, so a tool result sharing this
	// pointer does not silently acquire the event stream's redaction.
	if !strings.Contains(failing.Error.Message, secret) {
		t.Fatalf("buildMCPEventState mutated its input: %q", failing.Error.Message)
	}
}

// TestMCPEventsSnapshotFnRunsTheListAndStatusActions pins that the stream is
// fed by the same actions the tools are, rather than a second reader.
func TestMCPEventsSnapshotFnRunsTheListAndStatusActions(t *testing.T) {
	listCalls, statusCalls := 0, 0
	actions := fakeMCPActions()
	baseList := actions.list
	actions.list = func() (any, error) { listCalls++; return baseList() }
	baseStatus := actions.status
	actions.status = func() (any, error) { statusCalls++; return baseStatus() }

	snapshot := mcpEventsSnapshotFn(actions)
	if snapshot == nil {
		t.Fatal("mcpEventsSnapshotFn() = nil for complete actions")
	}
	value, err := snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot() error = %v", err)
	}
	if listCalls != 1 || statusCalls != 1 {
		t.Fatalf("list calls = %d, status calls = %d, want 1 and 1", listCalls, statusCalls)
	}
	state, ok := value.(mcpEventState)
	if !ok {
		t.Fatalf("snapshot returned %T, want mcpEventState", value)
	}
	if len(state.Services) != 1 || state.Services[0].Name != "demo" {
		t.Fatalf("snapshot services = %+v, want the list action's own result", state.Services)
	}

	// The daemon builds the control plane from these actions; incomplete
	// actions must disable the stream rather than panic in the handler.
	if mcpEventsSnapshotFn(mcpActions{}) != nil {
		t.Fatal("mcpEventsSnapshotFn() = non-nil for actions with no list or status")
	}
}

// TestBuildMCPControlPlaneMountsTheEventStream ties the wiring together: the
// shipped builder hands the daemon a snapshot function and the configured
// keepalive.
func TestBuildMCPControlPlaneMountsTheEventStream(t *testing.T) {
	cp := buildMCPControlPlane(
		mcpControlPlaneSettings{Enabled: true, Allow: []string{"alice@example.com"}},
		fakeMCPActions(),
		[]string{"tag:tsmain"},
		25_000_000_000,
	)
	if cp == nil {
		t.Fatal("buildMCPControlPlane() = nil when enabled")
	}
	if cp.EventsSnapshot == nil {
		t.Fatal("EventsSnapshot = nil; the event stream would not be mounted")
	}
	if cp.EventsKeepalive != 25_000_000_000 {
		t.Fatalf("EventsKeepalive = %s, want the configured value", cp.EventsKeepalive)
	}
}

func TestParseMCPEventsKeepalive(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{"", "0s", false},
		{"  ", "0s", false},
		{"20s", "20s", false},
		{"5m", "5m0s", false},
		{"1s", "", true},
		{"10m", "", true},
		{"banana", "", true},
	}
	for _, tc := range cases {
		got, err := parseMCPEventsKeepalive(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseMCPEventsKeepalive(%q) = %s, want an error", tc.raw, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseMCPEventsKeepalive(%q) error = %v", tc.raw, err)
		}
		if got.String() != tc.want {
			t.Fatalf("parseMCPEventsKeepalive(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}
