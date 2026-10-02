package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
)

func peopleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	for _, s := range []Service{{Name: "photos", Type: TypeProxy, Target: "http://localhost:3000"}, {Name: "finance", Type: TypeProxy, Target: "http://localhost:3001"}, {Name: "files", Type: TypeFile, Path: t.TempDir()}, {Name: "db", Type: TypeTCP, Target: "localhost:5432"}, {Name: "pub", Type: TypeProxy, Target: "http://localhost:3002", Funnel: true, PublicAck: true}} {
		if _, err := Add(path, s); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestPeopleLifecycleAndStickyScope(t *testing.T) {
	path := peopleFixture(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expires, err := ParsePersonExpiry("7d", now)
	if err != nil || !expires.Equal(now.Add(168*time.Hour)) {
		t.Fatal(expires, err)
	}
	p, err := ChangePerson(path, " Alice@Example.com ", []string{"photos", "finance"}, expires, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Login != "alice@example.com" || !PersonGrantActiveAt(p, "photos", now) {
		t.Fatalf("wrong grant: %+v", p)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 2 || len(reg.People) != 1 || !reg.Services[0].PeopleScoped {
		t.Fatalf("not persisted/scoped: %+v", reg)
	}
	if _, err := ChangePerson(path, p.Login, []string{"photos"}, nil, false, true); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	p = reg.People[0]
	if !p.Grants[0].ExpiresAt.Equal(*expires) || PersonGrantActiveAt(p, "finance", now) {
		t.Fatal("update did not replace apps/preserve deadline")
	}
	if ok, auth := PeopleAccessAt(reg, reg.Services[0], "eve@example.com", nil, now); ok || !auth {
		t.Fatal("unknown person got a scoped app")
	}
	if ok, auth := PeopleAccessAt(reg, reg.Services[1], p.Login, nil, now); ok || !auth {
		t.Fatal("removed app access survives")
	}
	if ok, auth := PeopleAccessAt(reg, reg.Services[0], p.Login, []string{"tag:admin"}, now); ok || !auth {
		t.Fatal("tagged device impersonated a person")
	}
	removed, err := RemovePerson(path, p.Login)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
	reg, _ = Load(path)
	if !reg.People[0].Revoked || len(reg.People[0].Grants) != 0 || !reg.Services[0].PeopleScoped || !reg.Services[1].PeopleScoped {
		t.Fatal("removal widened access")
	}
	if _, err := ChangePerson(path, p.Login, nil, nil, false, true); err == nil || !strings.Contains(err.Error(), "person not found") {
		t.Fatal(err)
	}
	if removed, err = RemovePerson(path, p.Login); err != nil || removed {
		t.Fatal("not idempotent", removed, err)
	}
	if _, err = ChangePerson(path, p.Login, []string{"photos"}, nil, true, false); err != nil {
		t.Fatal("explicit re-add failed", err)
	}
	if _, err = Remove(path, "finance"); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if len(reg.People) != 1 || reg.People[0].Revoked {
		t.Fatal("service removal lost people")
	}
	if _, err = Remove(path, "photos"); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if len(reg.People[0].Grants) != 0 {
		t.Fatal("service removal retained stale grant")
	}
}

func TestPeopleExpiryClockChangesAndRestart(t *testing.T) {
	path := peopleFixture(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(time.Hour)
	if _, err := ChangePerson(path, "alice@example.com", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	if changed, err := ExpirePeople(path, now.Add(-time.Hour)); err != nil || changed {
		t.Fatal(changed, err)
	}
	reg, _ := Load(path)
	if !PersonGrantActiveAt(reg.People[0], "photos", now) {
		t.Fatal("early expiry")
	}
	if changed, err := ExpirePeople(path, deadline); err != nil || !changed {
		t.Fatal(changed, err)
	}
	reg, _ = Load(path)
	if PersonGrantActiveAt(reg.People[0], "photos", now) || !reg.People[0].Grants[0].Expired {
		t.Fatal("clock rollback/reload revived observed expiry")
	}
	if changed, err := ExpirePeople(path, deadline.Add(time.Hour)); err != nil || changed {
		t.Fatal("repeat expiry", changed, err)
	}
	if _, err := ChangePerson(path, "alice@example.com", nil, nil, true, true); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if !PersonGrantActiveAt(reg.People[0], "photos", deadline.Add(24*time.Hour)) {
		t.Fatal("explicit never did not renew")
	}
}

func TestPeopleAllAndUnsupportedServices(t *testing.T) {
	path := peopleFixture(t)
	p, err := ChangePerson(path, "alice", []string{"all"}, nil, false, false)
	if err != nil || len(p.Grants) != 3 {
		t.Fatal(p, err)
	}
	for _, app := range []string{"db", "pub"} {
		t.Run(app, func(t *testing.T) {
			before, _ := os.ReadFile(path)
			_, err := ChangePerson(path, "bob", []string{app}, nil, false, false)
			if code, _ := ErrorCode(err); code != "people_service_unsupported" {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("refusal mutated registry")
			}
		})
	}
	reg, _ := Load(path)
	svc := reg.Services[0]
	svc.Type = TypeTCP
	svc.Target = "localhost:3000"
	if _, err := Add(path, svc); err == nil || !strings.Contains(err.Error(), "person-scoped") {
		t.Fatal("conversion bypassed scope", err)
	}
	svc = reg.Services[0]
	svc.PeopleScoped = false
	svc.Target = "http://localhost:9000"
	if _, err := Add(path, svc); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if !reg.Services[0].PeopleScoped {
		t.Fatal("legacy add erased sticky scope")
	}
}

func TestPeopleValidationAndLegacyRevocation(t *testing.T) {
	path := peopleFixture(t)
	for _, login := range []string{"", "bad login", "a\nb", "a b", "a\x7fb"} {
		if _, err := NormalizePerson(login); err == nil {
			t.Fatal("accepted invalid login", login)
		}
	}
	for _, duration := range []string{"", "0h", "-1h", "xd", "0d", "36501d", "neverx"} {
		if _, err := ParsePersonExpiry(duration, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
			t.Fatal("accepted expiry", duration)
		}
	}
	if e, err := ParsePersonExpiry("never", time.Time{}); err != nil || e != nil {
		t.Fatal(e, err)
	}
	if _, err := ParsePersonExpiry("90m", time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		who    string
		apps   []string
		update bool
		want   string
	}{{"alice", nil, false, "apps must"}, {"alice", []string{"missing"}, false, "service not found"}, {"alice", nil, true, "person not found"}} {
		if _, err := ChangePerson(path, tc.who, tc.apps, nil, false, tc.update); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatal(err)
		}
	}
	reg, _ := Load(path)
	svc := reg.Services[0]
	svc.AllowedUsers = []string{" Alice ", "bob"}
	if _, err := Add(path, svc); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemovePerson(path, "alice"); err != nil || !removed {
		t.Fatal(removed, err)
	}
	reg, _ = Load(path)
	if len(reg.Services[0].AllowedUsers) != 1 || reg.Services[0].AllowedUsers[0] != "bob" || !reg.Services[0].PeopleScoped {
		t.Fatal("legacy allow not removed", reg.Services[0])
	}
	if allowed, auth := PeopleAccessAt(reg, reg.Services[2], "alice", nil, time.Time{}); allowed || !auth {
		t.Fatal("legacy open service bypassed tombstone")
	}
	if _, err := ChangePerson(path, "bob", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangePerson(path, "bob", []string{"files"}, nil, false, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}
}

func TestPeopleRegistryStrictVersionAndMigration(t *testing.T) {
	path := peopleFixture(t)
	before, _ := os.ReadFile(path)
	reg, err := Load(path)
	after, _ := os.ReadFile(path)
	if err != nil || reg.SchemaVersion != 1 || string(before) != string(after) {
		t.Fatal("legacy load changed bytes", err)
	}
	for _, people := range []string{`[{"login":"Alice","grants":[]}]`, `[{"login":"a","grants":[]},{"login":"a","grants":[]}]`, `[{"login":"a","grants":[{"app":"photos"},{"app":"photos"}]}]`, `[{"login":"a","grants":[{"app":"INVALID"}]}]`, `[{"login":"a","grants":[],"unknown":true}]`} {
		t.Run(people, func(t *testing.T) {
			bad := filepath.Join(t.TempDir(), "registry.json")
			if err := os.WriteFile(bad, []byte(`{"schema_version":2,"services":[],"people":`+people+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(bad); err == nil {
				t.Fatal("invalid people admitted")
			}
		})
	}
	if _, err := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	// The shipped older decoder disallows top-level fields, and supports only
	// version 1. A real old binary is also exercised in the author receipts.
	var old struct {
		SchemaVersion int               `json:"schema_version"`
		Services      []json.RawMessage `json:"services"`
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&old); err == nil || !strings.Contains(err.Error(), `unknown field "people"`) {
		t.Fatal("old decoder could erase people", err)
	}
	if old.SchemaVersion != 2 {
		t.Fatal("people data not versioned")
	}
}

func TestPeopleStoreFailuresDoNotAdmitInvalidState(t *testing.T) {
	path := peopleFixture(t)
	if _, err := ChangePerson(path, "bad login", []string{"photos"}, nil, false, false); err == nil {
		t.Fatal("malformed login accepted")
	}
	if _, err := RemovePerson(path, ""); err == nil {
		t.Fatal("empty revocation accepted")
	}
	reg, _ := Load(path)
	if allowed, authoritative := PeopleAccessAt(reg, reg.Services[0], "unknown", nil, time.Time{}); allowed || authoritative {
		t.Fatal("legacy ACL fallback changed")
	}
	empty := filepath.Join(t.TempDir(), "registry.json")
	if _, err := ChangePerson(empty, "alice", []string{"all"}, nil, false, false); err == nil || !strings.Contains(err.Error(), "apps must") {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{func() error { _, err := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); return err }, func() error { _, err := RemovePerson(path, "alice"); return err }, func() error { _, err := ExpirePeople(path, time.Time{}); return err }} {
		if err := operation(); err == nil || !strings.Contains(err.Error(), "load registry") {
			t.Fatal("corrupt registry mutation accepted", err)
		}
	}
}

func TestPeopleExpiryRechecksAfterAcquiringWriterLock(t *testing.T) {
	if changed, err := ExpirePeople(filepath.Join(t.TempDir(), "missing.json"), time.Time{}); err != nil || changed {
		t.Fatal(changed, err)
	}
	for _, phase := range []string{"corrupt while waiting", "renew while waiting"} {
		t.Run(phase, func(t *testing.T) {
			path := peopleFixture(t)
			deadline := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			if _, err := ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
				t.Fatal(err)
			}
			old := lockFn
			t.Cleanup(func() { lockFn = old })
			lockFn = func(f *os.File) error {
				if err := old(f); err != nil {
					return err
				}
				if phase == "corrupt while waiting" {
					return os.WriteFile(path, []byte("broken"), 0600)
				}
				reg, err := Load(path)
				if err != nil {
					return err
				}
				reg.People[0].Grants[0].ExpiresAt = nil
				return save(path, reg)
			}
			changed, err := ExpirePeople(path, deadline)
			if phase == "corrupt while waiting" {
				if err == nil || !strings.Contains(err.Error(), "load registry") {
					t.Fatal("did not re-read corrupt state", err)
				}
			} else {
				if err != nil || changed {
					t.Fatal("expiry overwrote concurrent renewal", changed, err)
				}
				reg, _ := Load(path)
				if reg.People[0].Grants[0].Expired {
					t.Fatal("renewed grant expired")
				}
			}
		})
	}
}
