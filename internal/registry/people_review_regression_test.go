package registry

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestReviewAllSnapshotAndAtomicMixedRefusal(t *testing.T) {
	path := peopleFixture(t)
	p, err := ChangePerson(path, "alice", []string{"all"}, nil, false, false)
	if err != nil || len(p.Grants) != 3 {
		t.Fatal("positive all control", p, err)
	}
	if _, err := Add(path, Service{Name: "future", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.People[0].Grants) != 3 || PersonGrantActiveAt(reg.People[0], "future", time.Now()) {
		t.Fatal("all included future app")
	}
	for _, app := range []string{"db", "pub"} {
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ChangePerson(path, "alice", []string{"photos", app}, nil, false, true)
		if code, _ := ErrorCode(err); code != "people_service_unsupported" {
			t.Fatal("wrong refusal", err)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("mixed unsupported selection partially persisted")
		}
	}
	t.Log("all remains a 3-app snapshot after future app; mixed TCP/Funnel updates refuse with bytes unchanged")
}
func TestReviewExpiryTimezoneAndDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// 2030 spring-forward begins March 10: a 'day' is a 24-hour lifetime.
	start := time.Date(2030, 3, 9, 12, 0, 0, 0, loc)
	e, err := ParsePersonExpiry("1d", start)
	if err != nil {
		t.Fatal(err)
	}
	eUTC, err := ParsePersonExpiry("1d", start.UTC())
	if err != nil {
		t.Fatal(err)
	}
	if e.Location() != time.UTC || !e.Equal(*eUTC) || e.Sub(start) != 24*time.Hour || e.In(loc).Hour() != 13 {
		t.Fatalf("timezone lifetime mismatch start=%s expiry=%s", start, e)
	}
	p := Person{Login: "alice", Grants: []PersonGrant{{App: "photos", ExpiresAt: e}}}
	if !PersonGrantActiveAt(p, "photos", e.Add(-time.Nanosecond)) || PersonGrantActiveAt(p, "photos", e.In(loc)) {
		t.Fatal("timezone expiry boundary incorrect")
	}
	t.Logf("24-hour day: start=%s expiry UTC=%s local=%s; boundary denial is timezone-independent", start, e, e.In(loc))
}
