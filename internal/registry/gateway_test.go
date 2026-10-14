package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func gatewayRegistry(t *testing.T) (string, *Registry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	p := &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner@example.com", Gateway: &GatewayConfig{Enabled: true, Ports: DefaultGatewayPorts()}}
	if err := SetPortal(path, p); err != nil {
		t.Fatal(err)
	}
	return path, &Registry{Portal: p}
}

func TestGatewayAllocatorAndPersistence(t *testing.T) {
	path, _ := gatewayRegistry(t)
	for _, name := range []string{"a", "b"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000", PortalOnly: true}); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 3 || reg.Services[0].PortalRoute.Port != 8443 || reg.Services[1].PortalRoute.Port != 8444 || !reg.Services[0].PeopleScoped {
		t.Fatalf("assignments: %+v", reg)
	}
	if err := SetPortalRoute(context.Background(), path, "a", &PortalRoute{}); err != nil {
		t.Fatal(err)
	}
	reg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Services[0].PortalRoute.Port != 8443 {
		t.Fatal("assignment moved")
	}
	before, _ := os.ReadFile(path)
	for _, port := range []int{8444, 6000, 443, 80, 8500, -1} {
		if err := SetPortalRoute(context.Background(), path, "a", &PortalRoute{Port: port}); err == nil {
			t.Fatalf("accepted %d", port)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("failed write changed registry")
		}
	}
	if err := SetPortalRoute(context.Background(), path, "a", nil); err == nil {
		t.Fatal("removed only route")
	}
}

func TestGatewayBadPorts(t *testing.T) {
	// Independent transcription of the fetched WHATWG first table column.
	ports := []int{0, 1, 7, 9, 11, 13, 15, 17, 19, 20, 21, 22, 23, 25, 37, 42, 43, 53, 69, 77, 79, 87, 95, 101, 102, 103, 104, 109, 110, 111, 113, 115, 117, 119, 123, 135, 137, 139, 143, 161, 179, 389, 427, 465, 512, 513, 514, 515, 526, 530, 531, 532, 540, 548, 554, 556, 563, 587, 601, 636, 989, 990, 993, 995, 1719, 1720, 1723, 2049, 3659, 4045, 4190, 5060, 5061, 6000, 6566, 6665, 6666, 6667, 6668, 6669, 6679, 6697, 10080, 80, 443}
	_, reg := gatewayRegistry(t)
	for _, port := range ports {
		if !GatewayBadPort(port) {
			t.Errorf("bad port %d allowed", port)
		}
		if port == 0 {
			continue
		}
		reg.Portal.Gateway.Ports = GatewayPorts{From: port, To: port + 1}
		got, err := AllocateGatewayPort(reg, "new", 0)
		if err == nil && got == port {
			t.Errorf("allocated blocked %d", port)
		}
	}
	if GatewayBadPort(8443) {
		t.Fatal("positive control blocked")
	}
}

func TestGatewayWriteValidation(t *testing.T) {
	for _, kind := range []string{"tcp", "funnel", "guest", "unscoped", "missing-route", "conflict", "outside", "bad", "upstream", "range"} {
		t.Run(kind, func(t *testing.T) {
			path, reg := gatewayRegistry(t)
			svc := Service{Name: "app", Type: TypeProxy, Target: "http://localhost:3000", PeopleScoped: true, PortalRoute: &PortalRoute{Port: 8443}}
			switch kind {
			case "tcp":
				svc.Type = TypeTCP
			case "funnel":
				svc.Funnel = true
			case "guest":
				svc.GuestGate = true
			case "unscoped":
				svc.PeopleScoped = false
			case "missing-route":
				svc.PortalOnly = true
				svc.PortalRoute = nil
			case "conflict":
				other := svc
				other.Name = "other"
				reg.Services = append(reg.Services, other)
			case "outside":
				svc.PortalRoute.Port = 8500
			case "bad":
				svc.PortalRoute.Port = 443
			case "upstream":
				svc.Upstream = "tailnet"
			case "range":
				reg.Portal.Gateway.Ports.From = 0
			}
			reg.Services = append(reg.Services, svc)
			before, _ := os.ReadFile(path)
			if err := save(path, reg); err == nil {
				t.Fatal("invalid write accepted")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid write published")
			}
		})
	}
}

func TestGatewayLegacyRoundTrip(t *testing.T) {
	for _, withPortal := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "registry.json")
		reg := emptyRegistry()
		if withPortal {
			reg.Portal = &PortalConfig{Hostname: "home", Owner: "owner@example.com"}
		}
		if err := save(path, reg); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(path)
		loaded, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := save(path, loaded); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("legacy bytes changed: %s", after)
		}
		if loaded.SchemaVersion > 2 {
			t.Fatal("legacy schema upgraded")
		}
	}
}

func TestGatewayStrictDecode(t *testing.T) {
	path, _ := gatewayRegistry(t)
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["portal"].(map[string]any)["gateway"].(map[string]any)["unknown_gateway"] = true
	data, _ = json.Marshal(doc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown key accepted")
	}
	var svc Service
	if err := json.Unmarshal([]byte(`{"name":"a","portal_route":{"port":8443,"unknown":true}}`), &svc); err == nil {
		t.Fatal("unknown route key accepted")
	}
}
