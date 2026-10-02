package registry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPortalRegistryLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:8000"}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := Preflight(path)
	p := &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"admin"}}
	if err := SetPortal(path, p); err != nil {
		t.Fatal(err)
	}
	reg, issues, err := PortalPreflight(path)
	if err != nil || len(issues) != 0 || !reflect.DeepEqual(reg.Portal, p) || !reflect.DeepEqual(before.Services, reg.Services) || reg.SchemaVersion != 2 {
		t.Fatalf("round trip: %+v %v %v", reg, issues, err)
	}
	if _, err := Add(path, Service{Name: "home", Type: TypeProxy, Target: "http://localhost:9000"}); err == nil {
		t.Fatal("app took portal hostname")
	}
	if err := DisablePortal(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err = PortalPreflight(path)
	if err != nil || reg.Portal.Enabled || !reflect.DeepEqual(reg.Services, before.Services) || reg.Portal.Owner != "owner" {
		t.Fatalf("disable: %+v %v", reg, err)
	}
	b, _ := os.ReadFile(path)
	if err := DisablePortal(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(b, after) {
		t.Fatal("repeated disable rewrote state")
	}
	if err := SetPortal(path, nil); err != nil {
		t.Fatal(err)
	}
	if err := DisablePortal(path); err != nil {
		t.Fatal(err)
	}
}

func TestPortalAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *PortalConfig
		code string
	}{
		{"funnel", &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Funnel: true}, CodePortalFunnelRefused},
		{"name", &PortalConfig{Hostname: "Bad/Name", Owner: "owner"}, CodeInvalidServiceName},
		{"owner", &PortalConfig{Hostname: "home", Owner: ""}, "portal_identity_invalid"},
		{"admin", &PortalConfig{Hostname: "home", Owner: "owner", Admins: []string{"Alice"}}, "portal_identity_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := SetPortal(filepath.Join(t.TempDir(), "registry.json"), tc.p)
			code, _ := ErrorCode(err)
			if code != tc.code {
				t.Fatalf("code=%s err=%v", code, err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "home", Type: TypeProxy, Target: "http://localhost:8000"}); err != nil {
		t.Fatal(err)
	}
	err := SetPortal(path, &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"})
	code, _ := ErrorCode(err)
	if code != "portal_hostname_conflict" {
		t.Fatalf("collision=%v", err)
	}
	for _, raw := range []string{
		`{"schema_version":2,"services":[],"portal":{"hostname":"home","owner":"owner","funnel":true}}`,
		`{"schema_version":2,"services":[],"portal":{"hostname":"home","owner":"owner","typo":true}}`,
		`{"schema_version":2,"services":[{"name":"home","type":"proxy","target":"localhost:8000"}],"portal":{"hostname":"home","owner":"owner"}}`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := PortalPreflight(path); err == nil {
			t.Fatal("unsafe portal admitted")
		}
		if err := DisablePortal(path); err == nil {
			t.Fatal("mutation dropped invalid config")
		}
	}
}

func TestPortalReadOnlyBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if _, _, err := PortalPreflight(path); err == nil {
		t.Fatal("missing registry accepted")
	}
	if _, _, err := PortalPreflight(dir); err == nil {
		t.Fatal("directory registry accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 4<<20+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PortalPreflight(path); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Fatalf("oversize=%v", err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":2,"services":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if _, _, err := PortalPreflight(path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("read modified registry")
	}
}

func TestPortalConcurrentRegistryWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:8000"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 12; n++ {
				var err error
				if i == 0 {
					err = SetPortal(path, &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"})
				} else {
					_, err = ChangePerson(path, "alice", []string{"photos"}, nil, false, n > 0)
				}
				if err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	for n := 0; n < 30; n++ {
		reg, _, err := PortalPreflight(path)
		if err != nil || len(reg.Services) != 1 {
			t.Fatalf("concurrent read: %+v %v", reg, err)
		}
	}
	wg.Wait()
	reg, _, err := PortalPreflight(path)
	if err != nil || reg.Portal == nil || len(reg.People) != 1 {
		t.Fatalf("lost writer: %+v %v", reg, err)
	}
	b, _ := os.ReadFile(path)
	if !json.Valid(b) {
		t.Fatal("partial write persisted")
	}
}

func TestPortalMutationPreservesCorruptRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	before := []byte(`{"future-field":true}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetPortal(path, &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err == nil {
		t.Fatal("portal enable dropped unknown registry state")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed mutation changed registry: %s %v", after, err)
	}
}
