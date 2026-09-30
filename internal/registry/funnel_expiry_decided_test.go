package registry

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// handWrittenUndecidedFunnel is the entry the audit wrote by hand: a Funnel
// with public_ack and neither a deadline nor the explicit never.
const handWrittenUndecidedFunnel = `{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true}]}`

func writeRegistryFixture(t *testing.T, raw string) string {
	t.Helper()
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFunnelWithoutDecidedExpiryIsAServiceIssue pins that absence is not
// "never": the runtime loader isolates the entry, so the daemon never builds a
// listener for it, and registry check's loader reports it with the code and a
// next naming both ways to decide.
func TestFunnelWithoutDecidedExpiryIsAServiceIssue(t *testing.T) {
	path := writeRegistryFixture(t, handWrittenUndecidedFunnel)
	for name, load := range map[string]func(string) (*Registry, []ServiceIssue, error){
		"LoadForRuntime": LoadForRuntime,
		"Preflight":      Preflight,
	} {
		reg, issues, err := load(path)
		if err != nil {
			t.Fatalf("%s error = %v, want a per-service issue", name, err)
		}
		if len(reg.Services) != 0 {
			t.Fatalf("%s services = %+v, want the undecided Funnel kept out of the runnable set", name, reg.Services)
		}
		if len(issues) != 1 || issues[0].Name != "pub" {
			t.Fatalf("%s issues = %+v, want one issue for pub", name, issues)
		}
		code, ok := ErrorCode(issues[0].Err)
		if !ok || code != CodeFunnelExpiryRequired {
			t.Fatalf("%s issue code = %q,%v, want %s; err=%v", name, code, ok, CodeFunnelExpiryRequired, issues[0].Err)
		}
		var recovery interface{ NextCommands() []string }
		if !errors.As(issues[0].Err, &recovery) {
			t.Fatalf("%s issue carries no next: %v", name, issues[0].Err)
		}
		next := strings.Join(recovery.NextCommands(), "\n")
		for _, want := range []string{`"funnel_expires_at": "never"`, "RFC 3339", "tslink registry check"} {
			if !strings.Contains(next, want) {
				t.Fatalf("%s next = %q, want it to mention %s", name, next, want)
			}
		}
	}

	// The diagnostic loader keeps showing the entry, but must not describe the
	// undecided Funnel as a permanent one.
	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("Load() services = %+v, want the entry visible to diagnostics", reg.Services)
	}
	svc := reg.Services[0]
	if !svc.FunnelExpiryUndecided() {
		t.Fatalf("diagnostic view lost the undecided marker: %+v", svc)
	}
	if remaining := FunnelRemainingAt(svc, time.Now()); remaining != nil {
		t.Fatalf("FunnelRemainingAt(undecided) = %q, want nil: it is not a permanent Funnel", *remaining)
	}
	if err := ValidateService(svc); err == nil {
		t.Fatal("ValidateService(undecided diagnostic entry) = nil, want funnel_expiry_required")
	} else if code, _ := ErrorCode(err); code != CodeFunnelExpiryRequired {
		t.Fatalf("ValidateService code = %q, want %s", code, CodeFunnelExpiryRequired)
	}
}

// TestFunnelExpiryNullIsNotADecision keeps a JSON null from reading as never.
func TestFunnelExpiryNullIsNotADecision(t *testing.T) {
	path := writeRegistryFixture(t, `{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":null}]}`)
	reg, issues, err := LoadForRuntime(path)
	if err != nil || len(reg.Services) != 0 || len(issues) != 1 {
		t.Fatalf("LoadForRuntime = %+v, %+v, %v; want one issue", reg, issues, err)
	}
	if code, _ := ErrorCode(issues[0].Err); code != CodeFunnelExpiryRequired {
		t.Fatalf("null expiry issue code = %q, want %s", code, CodeFunnelExpiryRequired)
	}
}

// TestExplicitNeverFunnelRoundTripsThroughEveryLoader pins the stored form of
// an explicit never: written by Add, readable by hand, accepted by every
// loader, preserved by a later typed rewrite, and not reported.
func TestExplicitNeverFunnelRoundTripsThroughEveryLoader(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "forever", Type: TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true}); err != nil {
		t.Fatalf("Add(never) error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"funnel_expires_at": "never"`) {
		t.Fatalf("registry.json = %s, want the explicit never stored", data)
	}

	// A typed rewrite of another entry keeps it.
	if _, err := Add(path, Service{Name: "other", Type: TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatalf("Add(other) error = %v", err)
	}
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), `"funnel_expires_at": "never"`) != 1 {
		t.Fatalf("registry.json after rewrite = %s, want the explicit never kept", data)
	}

	for name, load := range map[string]func(string) (*Registry, []ServiceIssue, error){
		"LoadForRuntime": LoadForRuntime,
		"Preflight":      Preflight,
	} {
		reg, issues, err := load(path)
		if err != nil || len(issues) != 0 || len(reg.Services) != 2 {
			t.Fatalf("%s = %+v, %+v, %v; want two valid services", name, reg, issues, err)
		}
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := reg.Services[0]
	if svc.Name != "forever" || svc.FunnelExpiresAt != nil || svc.FunnelExpiryUndecided() || FunnelExpiredAt(svc, time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("never service = %+v, want a decided Funnel that never expires", svc)
	}
	if remaining := FunnelRemainingAt(svc, time.Now()); remaining == nil || *remaining != "never" {
		t.Fatalf("FunnelRemainingAt(never) = %v, want never", remaining)
	}

	// The same form written by hand is accepted.
	handPath := writeRegistryFixture(t, `{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":"never"}]}`)
	if reg, issues, err := LoadForRuntime(handPath); err != nil || len(issues) != 0 || len(reg.Services) != 1 || reg.Services[0].FunnelExpiresAt != nil {
		t.Fatalf("hand-written never = %+v, %+v, %v; want one valid never service", reg, issues, err)
	}
}

// TestFunnelDeadlineEntryIsUnchanged keeps the deadline form byte-stable.
func TestFunnelDeadlineEntryIsUnchanged(t *testing.T) {
	path := writeRegistryFixture(t, `{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":"2030-01-02T03:04:05Z"}]}`)
	reg, issues, err := LoadForRuntime(path)
	if err != nil || len(issues) != 0 || len(reg.Services) != 1 {
		t.Fatalf("LoadForRuntime = %+v, %+v, %v", reg, issues, err)
	}
	want := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := reg.Services[0].FunnelExpiresAt; got == nil || !got.Equal(want) {
		t.Fatalf("deadline = %v, want %v", got, want)
	}
	if _, err := Add(path, Service{Name: "other", Type: TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"funnel_expires_at": "2030-01-02T03:04:05Z"`) || strings.Contains(string(data), "never") {
		t.Fatalf("registry.json = %s, want the deadline rewritten as is", data)
	}
}

// TestFunnelExpiryRejectsOtherStrings keeps the vocabulary closed.
func TestFunnelExpiryRejectsOtherStrings(t *testing.T) {
	for _, value := range []string{`"nevr"`, `"Never"`, `"24h"`, `""`, `0`} {
		path := writeRegistryFixture(t, `{"schema_version":1,"services":[{"name":"pub","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":`+value+`}]}`)
		reg, issues, err := LoadForRuntime(path)
		if err != nil || len(reg.Services) != 0 || len(issues) != 1 {
			t.Fatalf("funnel_expires_at=%s: LoadForRuntime = %+v, %+v, %v; want one issue", value, reg, issues, err)
		}
	}
}
