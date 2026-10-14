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

// A client cancellation stays allowed traffic with its reason, and only the
// fixed backend failure classes reach the file.
func TestClientCancellationAndErrorClassSurviveStoreAndQuery(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	for _, e := range []Event{
		{Time: testTime, Kind: "http", App: "app", Path: "/canceled", Status: 499, Decision: "allowed", Reason: "client_canceled"},
		{Time: testTime, Kind: "http", App: "app", Path: "/route-closed", Status: 503, Decision: "allowed", Reason: "route_closed", Via: "portal"},
		{Time: testTime, Kind: "http", App: "app", Path: "/node-closed", Status: 503, Decision: "allowed", Reason: "node_closed"},
		{Time: testTime, Kind: "http", App: "app", Path: "/refused", Status: 502, Decision: "allowed", ErrorClass: "connection_refused"},
		{Time: testTime, Kind: "http", App: "app", Path: "/leaky", Status: 502, Decision: "allowed", ErrorClass: "dial tcp 10.0.0.9:8080: refused"},
		{Time: testTime, Kind: "http", App: "app", Path: "/other", Status: 200, Decision: "allowed", Reason: "acl"},
	} {
		if !s.Record(e) {
			t.Fatal("event dropped")
		}
	}
	closeStore(t, s)
	got, err := Query(dir, Filter{App: "app"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Event{}
	for _, e := range got.Events {
		byPath[e.Path] = e
	}
	if e := byPath["/canceled"]; e.Decision != "allowed" || e.Reason != "client_canceled" || e.Status != 499 {
		t.Fatalf("%+v", e)
	}
	// TSLink's own cancellations keep their reasons as allowed traffic.
	for path, reason := range map[string]string{"/route-closed": "route_closed", "/node-closed": "node_closed"} {
		if e := byPath[path]; e.Decision != "allowed" || e.Reason != reason || e.Status != 503 || e.ErrorClass != "" {
			t.Fatalf("%+v", e)
		}
	}
	if e := byPath["/refused"]; e.ErrorClass != "connection_refused" || e.Reason != "" {
		t.Fatalf("%+v", e)
	}
	if e := byPath["/leaky"]; e.ErrorClass != "other" {
		t.Fatalf("%+v", e)
	}
	if e := byPath["/other"]; e.Reason != "" {
		t.Fatalf("allowed events keep only client_canceled and portal reasons: %+v", e)
	}
	if got.Summary.Denied != 0 || got.Summary.Allowed != 6 {
		t.Fatalf("%+v", got.Summary)
	}
}
