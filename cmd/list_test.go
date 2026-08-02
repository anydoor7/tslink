package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

type listJSONResponse struct {
	OK      bool   `json:"ok"`
	Command string `json:"command"`
	Data    struct {
		SchemaVersion string               `json:"schema_version"`
		Services      []ListServiceSummary `json:"services"`
		Count         int                  `json:"count"`
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
	if !strings.Contains(out, "db") || !strings.Contains(out, "tcp") || !strings.Contains(out, "pending") {
		t.Fatalf("list output missing pending typed TCP service: %s", out)
	}
	if strings.Contains(out, "<tailnet>") || strings.Contains(out, "https://") {
		t.Fatalf("list output rendered an unverified placeholder URL: %s", out)
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
	service := resp.Data.Services[0]
	if service.Name != "web" || service.URL != nil || !service.URLPending || service.State != "pending" {
		t.Fatalf("service = %+v, want slim pending service without private allow data", service)
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
	service := resp.Data.Services[0]
	if service.Type != registry.TypeTCP || service.URL != nil || !service.URLPending {
		t.Fatalf("service = %+v, want pending typed TCP summary", service)
	}
}

func TestListFiltersAndFieldProjection(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	for _, service := range []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"},
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432},
	} {
		if _, err := registry.Add(regPath, service); err != nil {
			t.Fatalf("registry.Add(%s): %v", service.Name, err)
		}
	}
	withStatusURLSeams(t, false, 0, time.Time{})

	result, err := loadListResultForPaths(regPath, pidPath, snapshotPath, listOptions{
		Name:   "web",
		Fields: []string{"name", "url"},
	})
	if err != nil {
		t.Fatalf("loadListResultForPaths: %v", err)
	}
	rows, ok := result.Services.([]map[string]any)
	if !ok || result.Count != 1 || len(rows) != 1 {
		t.Fatalf("result = %#v, want one projected row", result)
	}
	if rows[0]["name"] != "web" || rows[0]["url"] != (*string)(nil) || len(rows[0]) != 2 {
		t.Fatalf("projected row = %#v, want only pending name/url", rows[0])
	}

	tcp, err := loadListResultForPaths(regPath, pidPath, snapshotPath, listOptions{Type: registry.TypeTCP})
	if err != nil {
		t.Fatalf("type filter: %v", err)
	}
	services, ok := tcp.Services.([]ListServiceSummary)
	if !ok || tcp.Count != 1 || len(services) != 1 || services[0].Name != "db" {
		t.Fatalf("TCP result = %#v, want db only", tcp)
	}
}

func TestListRejectsConflictingOrUnknownProjection(t *testing.T) {
	if err := validateListOptions(listOptions{Verbose: true, Fields: []string{"name"}}); err == nil {
		t.Fatal("--verbose with --fields error = nil")
	}
	if err := validateListOptions(listOptions{Fields: []string{"backend"}}); err == nil {
		t.Fatal("unknown --fields value error = nil")
	}
}
