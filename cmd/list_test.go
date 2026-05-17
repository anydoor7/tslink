package cmd

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

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

func TestListJSONRedactsMiddlewareAuth(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	oldRegPath := registryPathFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		_ = rootCmd.PersistentFlags().Set("json", "false")
		rootCmd.SetArgs(nil)
	})
	registryPathFn = func() (string, error) { return regPath, nil }

	rootCmd.SetArgs([]string{"list", "--json"})
	got := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("run list: %v", err)
		}
	})
	if strings.Contains(got, "user:pass") {
		t.Fatalf("list --json leaked credential: %s", got)
	}
	if strings.Contains(got, "basic_auth") {
		t.Fatalf("list --json leaked private field name: %s", got)
	}

	var result output.Result
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, got)
	}
	if !result.OK || result.Command != "list" {
		t.Fatalf("result = %+v, want ok list response", result)
	}
}
