package accesslog

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Install the shared events verbatim: querying must not rewrite historical
// fingerprints, and readers in other languages must see the identical input.
func trafficFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/traffic-events.json")
	if err != nil {
		t.Fatal(err)
	}
	var events []json.RawMessage
	if err = json.Unmarshal(raw, &events); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.MkdirAll(filepath.Join(dir, "access-log"), 0700); err != nil {
		t.Fatal(err)
	}
	var lines bytes.Buffer
	for _, event := range events {
		if err = json.Compact(&lines, event); err != nil {
			t.Fatal(err)
		}
		lines.WriteByte('\n')
	}
	if err = os.WriteFile(filepath.Join(dir, "access-log", "2030-01-02-000000.jsonl"), lines.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTrafficGolden(t *testing.T) {
	dir := trafficFixture(t)
	wantRaw, err := os.ReadFile("testdata/traffic-expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var want TrafficSummary
	if err = json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "access-log", "2030-01-02-000000.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 10000} {
		r, err := Query(dir, Filter{Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r.Summary.Traffic, want) {
			got, _ := json.MarshalIndent(r.Summary.Traffic, "", "  ")
			t.Fatalf("limit %d traffic mismatch\ngot %s\nwant %s", limit, got, wantRaw)
		}
		if r.Summary.Count != 21 || r.Summary.Allowed != 15 || r.Summary.Denied != 6 || len(r.Events) != min(limit, 21) || r.Truncated != (limit < 21) {
			t.Fatalf("legacy event semantics changed: %+v", r)
		}
		// The old people table deliberately keeps raw login case and mixed kinds.
		var rawCase, tcp bool
		for _, p := range r.Summary.People {
			rawCase = rawCase || p.Key == " \tALICE@EXAMPLE.COM\r\n"
		}
		for _, a := range r.Summary.Apps {
			tcp = tcp || a.Key == "database" && a.Count == 4
		}
		if !rawCase || !tcp {
			t.Fatal("old people/apps semantics changed")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("query rewrote raw history", err)
	}
}

func TestTrafficFiltersBeforeAggregation(t *testing.T) {
	dir := trafficFixture(t)
	since := time.Date(2030, 1, 2, 3, 4, 1, 0, time.UTC)
	until := since.Add(time.Second)
	for _, tc := range []struct {
		name             string
		filter           Filter
		requests, bi, bo int
	}{
		{"authority", Filter{AllowedApps: []string{"docs"}, Limit: 1}, 1, 4, 9},
		{"no authority", Filter{AllowedApps: []string{}, Limit: 1}, 0, 0, 0},
		{"combined", Filter{App: "photos", Who: "ALICE@example.com", Since: &since, Until: &until, Decision: "denied", Limit: 1}, 1, 0, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Query(dir, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			h := r.Summary.Traffic.HTTP
			if h.Requests != tc.requests || h.BytesIn != int64(tc.bi) || h.BytesOut != int64(tc.bo) {
				t.Fatalf("filter: %+v", r.Summary.Traffic)
			}
			b, _ := json.Marshal(r)
			if bytes.Contains(b, []byte("hidden@example.com")) || bytes.Contains(b, []byte("secret")) {
				t.Fatalf("scope leak: %s", b)
			}
			if tc.requests == 0 && (len(r.Summary.Traffic.PeopleApps) != 0 || len(r.Summary.Traffic.Apps) != 0 || r.Summary.Traffic.TCP != (TCPTraffic{})) {
				t.Fatalf("empty scope leaked groups: %s", b)
			}
			if tc.name == "combined" && h.LastAllowedAt != nil {
				t.Fatal("denial advanced last allowed")
			}
		})
	}
}

func TestTrafficEmptyAndUnreadable(t *testing.T) {
	dir := t.TempDir()
	r, err := Query(dir, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	want := TrafficSummary{PeopleApps: []SubjectAppTraffic{}, Apps: []AppTraffic{}}
	if !reflect.DeepEqual(r.Summary.Traffic, want) {
		t.Fatalf("empty traffic: %+v", r.Summary.Traffic)
	}
	if err = os.WriteFile(filepath.Join(dir, "access-log"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Query(dir, Filter{}); err == nil {
		t.Fatal("read failure reported zero traffic")
	}
	// A real nonempty control proves that the empty-query probe can see events.
	r, err = Query(trafficFixture(t), Filter{})
	if err != nil || r.Summary.Traffic.HTTP.Requests != 13 {
		t.Fatal("nonempty control failed", err)
	}
}
