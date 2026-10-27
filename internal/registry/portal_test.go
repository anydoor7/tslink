package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if _, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:8000", AllowedUsers: []string{"owner"}}); err != nil {
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

func TestDefaultGatewayInitialization(t *testing.T) {
	ctx := context.Background()
	for _, legacy := range []string{"new", "schema1", "schema2", "schema3", "nodes", "portal-nodes", "runtime.json", "node-ownership.json", "supervisor"} {
		t.Run(legacy, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "registry.json")
			if strings.HasPrefix(legacy, "schema") {
				if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"schema_version":%s,"services":[]}`, strings.TrimPrefix(legacy, "schema"))), 0600); err != nil {
					t.Fatal(err)
				}
			} else if legacy == "nodes" || legacy == "portal-nodes" {
				if err := os.MkdirAll(filepath.Join(dir, legacy, "old-node"), 0700); err != nil {
					t.Fatal(err)
				}
			} else if legacy == "runtime.json" || legacy == "node-ownership.json" {
				if err := os.WriteFile(filepath.Join(dir, legacy), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			prior := func() (bool, error) { return legacy == "supervisor", nil }
			if err := InitializeGateway(ctx, path, prior); err != nil {
				t.Fatal(err)
			}
			reg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if legacy != "new" {
				if reg.Portal != nil {
					t.Fatal("legacy installation was migrated")
				}
				after, _ := os.ReadFile(path)
				if !bytes.Equal(before, after) {
					t.Fatal("legacy bytes changed")
				}
				return
			}
			if !GatewayEnabled(reg) || reg.SchemaVersion != 3 || reg.Portal.Owner != "" || reg.Portal.Gateway.Ports != DefaultGatewayPorts() {
				t.Fatalf("first initialization: %+v", reg)
			}
			if err := DisablePortal(path); err != nil {
				t.Fatal(err)
			}
			before, _ = os.ReadFile(path)
			if err := InitializeGateway(ctx, path, prior); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			reg, _ = Load(path)
			if GatewayEnabled(reg) || !bytes.Equal(before, after) {
				t.Fatal("reinstall reenabled the disabled gateway")
			}
		})
	}
}

func TestDefaultGatewayFirstRegistrationsAndOwner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	// EnsureDir creates an empty nodes directory before the first registration.
	if err := os.Mkdir(filepath.Join(dir, "nodes"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read initialized registry")
	}
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := AddWithDefaults(ctx, path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000"}, false, nil)
			if err != nil {
				t.Error(err)
			}
		}(name)
	}
	wg.Wait()
	reg, err := Load(path)
	if err != nil || !GatewayEnabled(reg) || len(reg.Services) != 3 {
		t.Fatalf("first adds: %+v %v", reg, err)
	}
	ports := map[int]bool{}
	for _, svc := range reg.Services {
		if svc.PortalOnly || !svc.PeopleScoped || svc.PortalRoute == nil || ports[svc.PortalRoute.Port] {
			t.Fatalf("route/node defaults: %+v", svc)
		}
		ports[svc.PortalRoute.Port] = true
	}
	if err := BootstrapPortalOwner(ctx, path, "other", "wrong"); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapPortalOwner(ctx, path, "home", "Owner"); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapPortalOwner(ctx, path, "home", "stranger"); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if reg.Portal.Owner != "owner" {
		t.Fatalf("owner=%q", reg.Portal.Owner)
	}
	if err := DisablePortal(path); err != nil {
		t.Fatal(err)
	}
	if _, err := AddWithDefaults(ctx, path, Service{Name: "four", Type: TypeProxy, Target: "http://localhost:4000"}, false, nil); err != nil {
		t.Fatal(err)
	}
	reg, _ = Load(path)
	if GatewayEnabled(reg) || reg.Services[3].PortalRoute != nil {
		t.Fatal("add reversed the off switch")
	}
}
