package cmd

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
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
	if _, err := call(limitsSetArguments{App: "app", MaxBody: str("unlimited")}); err == nil {
		t.Fatal("unlimited without acknowledgement accepted")
	} else if code, _ := registry.ErrorCode(err); code != registry.CodeInvalidRequestLimits {
		t.Fatalf("unlimited without ack: %v", err)
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
