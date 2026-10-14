package accesslog

import (
	"testing"
	"time"
)

func TestGatewayReasonsSurviveStoreAndQuery(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	reasons := []string{"granted", "owner", "admin", "expired", "revoked", "no_grant", "whois_failed", "registry_unavailable", "cross_origin_blocked", "upstream_down", "conn_gate_drop"}
	for i, reason := range reasons {
		decision := "denied"
		if i < 3 {
			decision = "allowed"
		}
		if !s.Record(Event{Time: testTime, Kind: "http", App: "app", Via: "portal", Reason: reason, Decision: decision}) {
			t.Fatal("event dropped")
		}
	}
	closeStore(t, s)
	got, err := Query(dir, Filter{App: "app", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len(got.Summary.Reasons) != len(reasons) || got.Summary.Allowed != 3 || got.Summary.Denied != 8 {
		t.Fatalf("reason aggregation: %+v", got)
	}
	all, err := Query(dir, Filter{App: "app"})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range all.Events {
		if e.Via != "portal" {
			t.Fatal("via not persisted")
		}
		seen[e.Reason] = true
	}
	for _, reason := range reasons {
		if !seen[reason] {
			t.Fatal("reason lost", reason)
		}
	}
	if e := sanitize(Event{Via: "client-value", Decision: "allowed", Reason: "owner"}); e.Via != "" || e.Reason != "" {
		t.Fatal("untrusted metadata accepted")
	}
}
