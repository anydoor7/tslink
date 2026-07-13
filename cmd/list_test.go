package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

type listJSONResponse struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Data    struct {
		SchemaVersion string                `json:"schema_version"`
		Services      []inspect.ServiceView `json:"services"`
		Count         int                   `json:"count"`
	} `json:"data"`
}

func runListJSONWithRegistry(t *testing.T, regPath string) string {
	t.Helper()
	oldRegPath := registryPathFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		_ = rootCmd.PersistentFlags().Set("json", "false")
		rootCmd.SetArgs(nil)
	})
	registryPathFn = func() (string, error) { return regPath, nil }

	rootCmd.SetArgs([]string{"list", "--json"})
	return captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("run list: %v", err)
		}
	})
}

func parseListJSONResponse(t *testing.T, raw string) listJSONResponse {
	t.Helper()
	var resp listJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal list --json response: %v\nraw: %s", err, raw)
	}
	return resp
}

func TestListServices_TCPTextUsesTypedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	var buf bytes.Buffer
	if err := listServices(regPath, &buf); err != nil {
		t.Fatalf("listServices: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "db.<tailnet>.ts.net:5432") {
		t.Fatalf("list output missing typed TCP endpoint: %s", out)
	}
	if strings.Contains(out, "https://db.<tailnet>.ts.net") {
		t.Fatalf("list output rendered TCP service as HTTPS: %s", out)
	}
}

func TestListRejectsMiddlewareConfig(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	rawRegistry := `{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000","middleware":{"basic_auth":"user:pass"}}]}`
	if err := os.WriteFile(regPath, []byte(rawRegistry), 0o600); err != nil {
		t.Fatalf("WriteFile registry: %v", err)
	}

	var buf bytes.Buffer
	err := listServices(regPath, &buf)
	if err == nil {
		t.Fatal("listServices() error = nil, want feature_unavailable")
	}
	if !strings.Contains(err.Error(), registry.CodeFeatureUnavailable) {
		t.Fatalf("listServices() error = %v, want feature_unavailable", err)
	}
	if strings.Contains(err.Error(), "user:pass") {
		t.Fatalf("listServices() error leaked credential: %v", err)
	}
	if strings.Contains(buf.String(), "user:pass") {
		t.Fatalf("listServices() output leaked credential: %s", buf.String())
	}
}

func TestListJSONRedactsAllowPrincipals(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	got := runListJSONWithRegistry(t, regPath)
	for _, principal := range []string{"alice@example.com", "tag:admin"} {
		if strings.Contains(got, principal) {
			t.Fatalf("list --json leaked allow principal %q: %s", principal, got)
		}
	}

	resp := parseListJSONResponse(t, got)
	if !resp.OK || resp.Command != "list" {
		t.Fatalf("response = %+v, want ok list response", resp)
	}
	if len(resp.Data.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(resp.Data.Services))
	}
	allow := resp.Data.Services[0].Allow
	if allow.Mode != "restricted" || allow.Count != 2 || !allow.Redacted || len(allow.Entries) != 0 {
		t.Fatalf("allow = %+v, want redacted restricted summary", allow)
	}
}

func TestListJSON_TCPUsesTypedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	got := runListJSONWithRegistry(t, regPath)
	if strings.Contains(got, "https://db.<tailnet>.ts.net") {
		t.Fatalf("list --json rendered TCP service as HTTPS: %s", got)
	}

	resp := parseListJSONResponse(t, got)
	if len(resp.Data.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(resp.Data.Services))
	}
	endpoint := resp.Data.Services[0].Endpoint
	if endpoint.Kind != inspect.EndpointKindTCP {
		t.Fatalf("endpoint kind = %q, want tcp", endpoint.Kind)
	}
	if endpoint.Display != "db.<tailnet>.ts.net:5432" || endpoint.Port != 5432 {
		t.Fatalf("endpoint = %+v, want typed TCP display and port", endpoint)
	}
}
