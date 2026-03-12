package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func testRegistryPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "registry.json")
}

func TestLoadEmpty(t *testing.T) {
	path := testRegistryPath(t)

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected empty registry, got %d services", len(reg.Services))
	}
}

func TestAddAndLoad(t *testing.T) {
	path := testRegistryPath(t)

	if err := Add(path, Service{
		Name:   "report",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}

	svc := reg.Services[0]
	if svc.Name != "report" {
		t.Fatalf("expected name report, got %q", svc.Name)
	}
	if svc.Type != TypeProxy {
		t.Fatalf("expected type %q, got %q", TypeProxy, svc.Type)
	}
	if svc.Target != "http://localhost:3000" {
		t.Fatalf("expected target to round-trip, got %q", svc.Target)
	}
	if svc.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestAddIdempotent(t *testing.T) {
	path := testRegistryPath(t)

	if err := Add(path, Service{
		Name:   "report",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after first Add returned error: %v", err)
	}
	firstCreatedAt := reg.Services[0].CreatedAt

	if err := Add(path, Service{
		Name:   "report",
		Type:   TypeProxy,
		Target: "http://localhost:4000",
	}); err != nil {
		t.Fatalf("second Add returned error: %v", err)
	}

	reg, err = Load(path)
	if err != nil {
		t.Fatalf("Load after second Add returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service after update, got %d", len(reg.Services))
	}

	svc := reg.Services[0]
	if svc.Target != "http://localhost:4000" {
		t.Fatalf("expected target to be updated, got %q", svc.Target)
	}
	if !svc.CreatedAt.Equal(firstCreatedAt) {
		t.Fatalf("expected created_at to be preserved, got %v want %v", svc.CreatedAt, firstCreatedAt)
	}
}

func TestRemove(t *testing.T) {
	path := testRegistryPath(t)

	if err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add report returned error: %v", err)
	}
	if err := Add(path, Service{Name: "docs", Type: TypeFile, Path: "/tmp/docs"}); err != nil {
		t.Fatalf("Add docs returned error: %v", err)
	}

	if err := Remove(path, "report"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service after remove, got %d", len(reg.Services))
	}
	if reg.Services[0].Name != "docs" {
		t.Fatalf("expected remaining service to be docs, got %q", reg.Services[0].Name)
	}
}

func TestRemoveNotFound(t *testing.T) {
	path := testRegistryPath(t)

	if err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	if err := Remove(path, "missing"); err == nil {
		t.Fatal("expected Remove to fail for missing service")
	}
}

func TestValidateName(t *testing.T) {
	valid := []string{"report", "my-app", "app123", "a-b-c"}
	for _, name := range valid {
		if err := ValidateName(name); err != nil {
			t.Fatalf("expected %q to be valid, got error: %v", name, err)
		}
	}

	invalid := []string{"Report", "my app", "app_123", "", "a/b", "a.b"}
	for _, name := range invalid {
		if err := ValidateName(name); err == nil {
			t.Fatalf("expected %q to be invalid", name)
		}
	}
}

func TestValidateNameEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"a", true},
		{"ab", true},
		{"a-b", true},
		{"abc-def-ghi", true},
		{"a1b2", true},
		{"123", true},

		{"-abc", false},   // leading hyphen
		{"abc-", false},   // trailing hyphen
		{"ABC", false},    // uppercase
		{"a_b", false},    // underscore
		{"a b", false},    // space
		{"a/b", false},    // slash
		{"a.b", false},    // dot
		{"", false},       // empty
		{"-", false},      // just hyphen
		{"a--b", true},    // double hyphen is valid per regex
		{"hello world", false},
	}

	for _, tc := range cases {
		err := ValidateName(tc.name)
		if tc.valid && err != nil {
			t.Errorf("ValidateName(%q) = error %v, want valid", tc.name, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", tc.name)
		}
	}
}

func TestLoadCorruptedJSON(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() with corrupted JSON should return error")
	}
}

func TestLoadEmptyFile(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() with empty file error = %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(reg.Services))
	}
}

func TestLoadWhitespaceOnly(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte("  \n\t  \n"), 0o600); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() with whitespace-only file error = %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(reg.Services))
	}
}

func TestAddInvalidName(t *testing.T) {
	path := testRegistryPath(t)
	err := Add(path, Service{Name: "INVALID", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() with invalid name should return error")
	}
}

func TestRemoveFromEmpty(t *testing.T) {
	path := testRegistryPath(t)
	err := Remove(path, "anything")
	if err == nil {
		t.Fatal("Remove() from empty registry should return error")
	}
}

func TestAddMultipleServices(t *testing.T) {
	path := testRegistryPath(t)

	services := []Service{
		{Name: "alpha", Type: TypeProxy, Target: "http://localhost:1000"},
		{Name: "beta", Type: TypeProxy, Target: "http://localhost:2000"},
		{Name: "gamma", Type: TypeFile, Path: "/tmp"},
	}

	for _, svc := range services {
		if err := Add(path, svc); err != nil {
			t.Fatalf("Add(%q) error = %v", svc.Name, err)
		}
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(reg.Services))
	}
}

func TestRemoveMiddleService(t *testing.T) {
	path := testRegistryPath(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:1000"}); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
	}

	if err := Remove(path, "beta"); err != nil {
		t.Fatalf("Remove(beta) error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(reg.Services))
	}

	names := make(map[string]bool)
	for _, svc := range reg.Services {
		names[svc.Name] = true
	}
	if names["beta"] {
		t.Fatal("beta should have been removed")
	}
	if !names["alpha"] || !names["gamma"] {
		t.Fatal("alpha and gamma should remain")
	}
}

func TestAddFileService(t *testing.T) {
	path := testRegistryPath(t)
	dir := t.TempDir()

	if err := Add(path, Service{
		Name: "docs",
		Type: TypeFile,
		Path: dir,
	}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}

	svc := reg.Services[0]
	if svc.Type != TypeFile {
		t.Fatalf("expected type %q, got %q", TypeFile, svc.Type)
	}
	if svc.Path != dir {
		t.Fatalf("expected path %q, got %q", dir, svc.Path)
	}
}
