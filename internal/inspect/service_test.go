package inspect

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

func TestServiceViewForProxyFileAndTCP(t *testing.T) {
	proxy := ServiceViewFor(registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		Tags:         []string{"tag:web"},
		AllowedUsers: []string{"alice@example.com"},
	})
	if proxy.SchemaVersion != SchemaVersion {
		t.Fatalf("proxy schema_version = %q, want %q", proxy.SchemaVersion, SchemaVersion)
	}
	if proxy.Endpoint.Kind != EndpointKindHTTPS || proxy.Endpoint.Display != "https://web.<tailnet>.ts.net" {
		t.Fatalf("proxy endpoint = %+v, want https tailnet endpoint", proxy.Endpoint)
	}
	if proxy.Exposure.Kind != ExposureTailnetAllow || proxy.Exposure.Public {
		t.Fatalf("proxy exposure = %+v, want tailnet_allow private exposure", proxy.Exposure)
	}
	if proxy.Tags.Count != 1 || proxy.Tags.Entries[0] != "tag:web" {
		t.Fatalf("proxy tags = %+v, want tag summary", proxy.Tags)
	}
	if proxy.Allow.Mode != "restricted" || proxy.Allow.Count != 1 || proxy.Allow.Entries[0] != "alice@example.com" {
		t.Fatalf("proxy allow = %+v, want restricted allow summary", proxy.Allow)
	}
	if proxy.Backend.Kind != "http_target" || proxy.Backend.Display != "http://localhost:3000" {
		t.Fatalf("proxy backend = %+v, want http target", proxy.Backend)
	}

	file := ServiceViewFor(registry.Service{
		Name: "docs",
		Type: registry.TypeFile,
		Path: "/srv/docs",
	})
	if file.Endpoint.Kind != EndpointKindHTTPS || file.Endpoint.Display != "https://docs.<tailnet>.ts.net" {
		t.Fatalf("file endpoint = %+v, want https tailnet endpoint", file.Endpoint)
	}
	if file.Exposure.Kind != ExposureTailnet {
		t.Fatalf("file exposure = %+v, want tailnet", file.Exposure)
	}
	if file.Backend.Kind != "directory" || file.Backend.Display != "/srv/docs" {
		t.Fatalf("file backend = %+v, want directory", file.Backend)
	}

	tcp := ServiceViewFor(registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	})
	if tcp.Endpoint.Kind != EndpointKindTCP || tcp.Endpoint.Display != "db.<tailnet>.ts.net:5432" {
		t.Fatalf("tcp endpoint = %+v, want typed tcp host:port", tcp.Endpoint)
	}
	if strings.HasPrefix(tcp.Endpoint.Display, "https://") {
		t.Fatalf("tcp endpoint must not be rendered as HTTPS: %s", tcp.Endpoint.Display)
	}
	if tcp.Exposure.Kind != ExposureTailnet {
		t.Fatalf("tcp exposure = %+v, want tailnet", tcp.Exposure)
	}
	if tcp.Allow.Mode != "not_applicable" {
		t.Fatalf("tcp allow = %+v, want not_applicable", tcp.Allow)
	}
	if tcp.Backend.Kind != "tcp_target" || tcp.Backend.Display != "localhost:5432" {
		t.Fatalf("tcp backend = %+v, want tcp target", tcp.Backend)
	}
	if len(tcp.Warnings) != 1 || tcp.Warnings[0].Code != "tcp_http_acl_not_applicable" {
		t.Fatalf("tcp warnings = %+v, want tcp boundary warning", tcp.Warnings)
	}
}

func TestServiceViewRedactsHTTPAuthCredentials(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	})
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	raw := string(data)
	if strings.Contains(raw, "user:pass") {
		t.Fatalf("public service view leaked credential: %s", raw)
	}
	if strings.Contains(raw, "basic_auth") {
		t.Fatalf("public service view leaked private field name: %s", raw)
	}
	if view.Middleware == nil || !view.Middleware.HTTPAuth {
		t.Fatalf("middleware summary = %+v, want redacted auth presence", view.Middleware)
	}
}
