package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// Survivor M37: re-adding an existing route with port 0 keeps its port even
// when a lower port became free.
func TestGatewayRouteReAddKeepsPort(t *testing.T) {
	path, _ := gatewayRegistry(t)
	for _, name := range []string{"a", "b"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
		if err := SetPortalRoute(context.Background(), path, name, &PortalRoute{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetPortalRoute(context.Background(), path, "a", nil); err != nil {
		t.Fatal(err)
	}
	// Toggle the opt-out on b, as `portal route add b --allow-cross-origin` would.
	if err := SetPortalRoute(context.Background(), path, "b", &PortalRoute{AllowCrossOrigin: true}); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reg.Services {
		if s.Name == "b" && s.PortalRoute.Port != 8444 {
			t.Fatalf("b moved from 8444 to %d", s.PortalRoute.Port)
		}
	}
}

func TestGatewayServiceFieldsFollowExistingFields(t *testing.T) {
	data, err := json.Marshal(Service{Name: "app", Type: TypeProxy, Target: "http://localhost:9", PortalOnly: true, PortalRoute: &PortalRoute{Port: 8443}, Upstream: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(data, []byte(`"name"`)) > bytes.Index(data, []byte(`"portal_route"`)) || bytes.Index(data, []byte(`"created_at"`)) > bytes.Index(data, []byte(`"upstream"`)) {
		t.Fatalf("new fields precede existing fields: %s", data)
	}
}
