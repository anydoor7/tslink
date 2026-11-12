package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// limitsWithout decodes the registry generically and removes each service's
// request_limits, so a comparison covers every other stored field, including
// fields this test does not know by name.
func limitsWithout(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, raw := range doc["services"].([]any) {
		delete(raw.(map[string]any), "request_limits")
	}
	return doc
}

// limits set changes only the named limits: on a guest-gated app with a
// health check, preserve_host, people and other limits, and on a remote
// portal-only app whose upstream an add would drop, every other byte stays.
func TestLimitsSetChangesOnlyTheNamedLimits(t *testing.T) {
	_, regPath := healthSetFixture(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	gated := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", PreserveHost: true, Health: &registry.HealthConfig{Path: "/healthz"}, RequestLimits: &registry.RequestLimits{ReadTimeout: "2m", HeaderTimeout: "3s"}}
	remote := registry.Service{Name: "rem", Type: registry.TypeProxy, Target: "https://box.tail1234.ts.net", Upstream: "tailnet", PortalOnly: true, PeopleScoped: true, PortalRoute: &registry.PortalRoute{Port: 8450}}
	for _, svc := range []registry.Service{gated, remote} {
		if _, err := registry.Add(regPath, svc); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := registry.CreateGuest(regPath, registry.CreateGuestOptions{App: "photos", Value: "2h", PublicAck: true, Now: now}); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"photos", "rem"} {
		before := limitsWithout(t, regPath)
		body := "4GiB"
		r, err := setLimitsContext(context.Background(), regPath, limitsSetArguments{App: app, MaxBody: &body})
		if err != nil || !r.Changed || r.RequestLimits == nil || r.RequestLimits.MaxBodyBytes != 4<<30 {
			t.Fatalf("%s: %+v %v", app, r, err)
		}
		if after := limitsWithout(t, regPath); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: limits set changed other fields", app)
		}
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == "photos" && (!reflect.DeepEqual(svc.RequestLimits, &registry.RequestLimits{MaxBody: "4GiB", ReadTimeout: "2m", HeaderTimeout: "3s"}) || !svc.GuestGate) {
			t.Fatalf("photos limits = %+v gate %v", svc.RequestLimits, svc.GuestGate)
		}
	}
	// Unchanged values write nothing.
	body := "4GiB"
	if r, err := setLimitsContext(context.Background(), regPath, limitsSetArguments{App: "photos", MaxBody: &body}); err != nil || r.Changed {
		t.Fatalf("repeat: %+v %v", r, err)
	}
}

func TestLimitsSetValidation(t *testing.T) {
	_, regPath := healthSetFixture(t)
	str := func(v string) *string { return &v }
	yes, no := true, false
	call := func(args limitsSetArguments) (LimitsSetResult, error) {
		return setLimitsContext(context.Background(), regPath, args)
	}
	if _, err := call(limitsSetArguments{App: "app"}); err == nil {
		t.Fatal("empty update accepted")
	}
	for _, bad := range []string{"unlimited", "banana"} {
		_, err := call(limitsSetArguments{App: "app", MaxBody: str(bad)})
		var coded registry.CodedError
		if !errors.As(err, &coded) || coded.Code != registry.CodeInvalidRequestLimits || len(coded.Next) != 1 || coded.Next[0] != "tslink limits set --help" {
			t.Fatalf("max body %q: %v next %q", bad, err, coded.Next)
		}
	}
	if r, err := call(limitsSetArguments{App: "app", MaxBody: str("unlimited"), UnlimitedAck: &yes}); err != nil || r.RequestLimits.MaxBodyBytes != -1 {
		t.Fatalf("acknowledged unlimited: %+v %v", r, err)
	}
	// A new size drops the old acknowledgement; ack without unlimited is refused.
	if r, err := call(limitsSetArguments{App: "app", MaxBody: str("1GiB")}); err != nil || r.RequestLimits.MaxBodyBytes != 1<<30 {
		t.Fatalf("size after unlimited: %+v %v", r, err)
	}
	if _, err := call(limitsSetArguments{App: "app", UnlimitedAck: &yes}); err == nil {
		t.Fatal("ack without unlimited accepted")
	}
	if _, err := call(limitsSetArguments{App: "app", UnlimitedAck: &no, ReadTimeout: str("-1s")}); err == nil {
		t.Fatal("negative timeout accepted")
	}
	// Empty values return limits to their defaults; all defaults store nothing.
	if r, err := call(limitsSetArguments{App: "app", MaxBody: str(""), ReadTimeout: str("")}); err != nil || r.RequestLimits.MaxBodyBytes != 32<<20 || r.RequestLimits.ReadTimeout != "30s" {
		t.Fatalf("reset: %+v %v", r, err)
	}
	if _, err := call(limitsSetArguments{App: "db", MaxBody: str("1GiB")}); err == nil {
		t.Fatal("tcp service accepted request limits")
	}
	if _, err := call(limitsSetArguments{App: "missing", MaxBody: str("1GiB")}); err == nil {
		t.Fatal("unknown app accepted")
	} else if code, _ := registry.ErrorCode(err); code != "not_found" {
		t.Fatalf("unknown app: %v", err)
	}
}

// R2-3: the help says the node restarts and to set limits before sending a link.
func TestLimitsSetHelpSaysTheNodeRestarts(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"limits", "set"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"node restarts to apply new limits", "uploads in flight end", "guests must reopen their link", "Set limits before sending a guest link"} {
		if !strings.Contains(cmd.Long, want) {
			t.Errorf("limits set help lacks %q", want)
		}
	}
	for _, tool := range mcpToolDefinitions {
		if tool.Name == "limits_set" && !strings.Contains(tool.Description, "node restarts") {
			t.Errorf("limits_set description lacks the restart: %v", tool.Description)
		}
	}
}

// A written change says the node restarts and how to know it is back, in the
// human output, the JSON data and the MCP result; an unchanged one does not.
func TestLimitsSetSaysTheNodeIsRestarting(t *testing.T) {
	_, regPath := healthSetFixture(t)
	if _, err := registry.Add(regPath, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	body := "64MiB"
	r, err := setLimitsContext(context.Background(), regPath, limitsSetArguments{App: "photos", MaxBody: &body})
	if err != nil || !r.Changed || !r.NodeRestarting {
		t.Fatalf("changed result = %+v %v", r, err)
	}
	for _, want := range []string{"restarting", "reopen their link", "tslink url photos --wait"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message %q lacks %q", r.Message, want)
		}
	}
	var human strings.Builder
	formatLimitsSet(&human, r)
	if !strings.Contains(human.String(), "tslink url photos --wait") {
		t.Fatalf("human output = %q", human.String())
	}
	encoded, err := json.Marshal(r)
	if err != nil || !strings.Contains(string(encoded), `"node_restarting":true`) {
		t.Fatalf("json = %s %v", encoded, err)
	}
	same, err := setLimitsContext(context.Background(), regPath, limitsSetArguments{App: "photos", MaxBody: &body})
	if err != nil || same.Changed || same.NodeRestarting || same.Message != "" {
		t.Fatalf("unchanged result = %+v %v", same, err)
	}
}

// N2: with no daemon running nothing restarts, and the result says so.
func TestLimitsSetWithoutADaemonDoesNotClaimARestart(t *testing.T) {
	_, regPath := healthSetFixture(t)
	if _, err := registry.Add(regPath, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return false }
	body := "64MiB"
	r, err := setLimitsContext(context.Background(), regPath, limitsSetArguments{App: "photos", MaxBody: &body})
	if err != nil || !r.Changed || r.NodeRestarting || !strings.Contains(r.Message, "not running") || strings.Contains(r.Message, "is restarting") {
		t.Fatalf("stopped daemon result = %+v %v", r, err)
	}
}
