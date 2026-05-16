package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	if _, err := Add(path, Service{
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

	if _, err := Add(path, Service{
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

	if _, err := Add(path, Service{
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

	if _, err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add report returned error: %v", err)
	}
	if _, err := Add(path, Service{Name: "docs", Type: TypeFile, Path: "/tmp/docs"}); err != nil {
		t.Fatalf("Add docs returned error: %v", err)
	}

	if removed, err := Remove(path, "report"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	} else if !removed {
		t.Fatal("expected removed=true for existing service")
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

func TestRemoveAndReturn(t *testing.T) {
	path := testRegistryPath(t)

	original := Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	if _, err := Add(path, original); err != nil {
		t.Fatalf("Add report returned error: %v", err)
	}
	if _, err := Add(path, Service{Name: "docs", Type: TypeFile, Path: "/tmp/docs"}); err != nil {
		t.Fatalf("Add docs returned error: %v", err)
	}

	removedSvc, removed, err := RemoveAndReturn(path, "report")
	if err != nil {
		t.Fatalf("RemoveAndReturn returned error: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true for existing service")
	}
	if removedSvc.Name != "report" || removedSvc.Target != original.Target {
		t.Fatalf("removed service = %+v, want report target %q", removedSvc, original.Target)
	}
	if len(removedSvc.Tags) != 1 || removedSvc.Tags[0] != "tag:tsmain" {
		t.Fatalf("removed service tags = %v, want [tag:tsmain]", removedSvc.Tags)
	}
	if removedSvc.CreatedAt.IsZero() {
		t.Fatal("removed service should include created_at from registry")
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "docs" {
		t.Fatalf("remaining services = %+v, want docs only", reg.Services)
	}
}

func TestRemoveNotFound(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	if removed, err := Remove(path, "missing"); err != nil {
		t.Fatalf("Remove returned error: %v", err)
	} else if removed {
		t.Fatal("expected removed=false for missing service")
	}
}

func TestRemoveAndReturnNotFound(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}

	removedSvc, removed, err := RemoveAndReturn(path, "missing")
	if err != nil {
		t.Fatalf("RemoveAndReturn returned error: %v", err)
	}
	if removed {
		t.Fatal("expected removed=false for missing service")
	}
	if removedSvc.Name != "" || removedSvc.Type != "" || len(removedSvc.Tags) != 0 || !removedSvc.CreatedAt.IsZero() {
		t.Fatalf("removed service = %+v, want zero value", removedSvc)
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

		{"-abc", false}, // leading hyphen
		{"abc-", false}, // trailing hyphen
		{"ABC", false},  // uppercase
		{"a_b", false},  // underscore
		{"a b", false},  // space
		{"a/b", false},  // slash
		{"a.b", false},  // dot
		{"", false},     // empty
		{"-", false},    // just hyphen
		{"a--b", true},  // double hyphen is valid per regex
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

func TestValidateTag(t *testing.T) {
	valid := []string{"tag:web", "tag:a", "tag:web-1", "tag:internal-api", "tag:" + strings.Repeat("a", 59)}
	for _, tag := range valid {
		if err := ValidateTag(tag); err != nil {
			t.Errorf("ValidateTag(%q) = error %v, want valid", tag, err)
		}
	}

	invalid := []string{"tag:", "web", "tag:Web", "tag:-web", "tag:web-", "tag:web_api", "", "tag:" + strings.Repeat("a", 60)}
	for _, tag := range invalid {
		if err := ValidateTag(tag); err == nil {
			t.Errorf("ValidateTag(%q) = nil, want error", tag)
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
	_, err := Add(path, Service{Name: "INVALID", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() with invalid name should return error")
	}
}

func TestRemoveFromEmpty(t *testing.T) {
	path := testRegistryPath(t)
	removed, err := Remove(path, "anything")
	if err != nil {
		t.Fatalf("Remove() from empty registry returned error: %v", err)
	}
	if removed {
		t.Fatal("expected removed=false from empty registry")
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
		if _, err := Add(path, svc); err != nil {
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
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:1000"}); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
	}

	if _, err := Remove(path, "beta"); err != nil {
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

	if _, err := Add(path, Service{
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

func TestLoadNullServices(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte(`{"services": null}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg.Services == nil {
		t.Fatal("Services should be non-nil even when JSON has null")
	}
}

func TestLoadReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry-dir")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	path := testRegistryPath(t)

	svc := Service{
		Name:   "roundtrip",
		Type:   TypeProxy,
		Target: "http://localhost:9999",
	}
	if _, err := Add(path, svc); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	loaded := reg.Services[0]
	if loaded.Name != svc.Name || loaded.Type != svc.Type || loaded.Target != svc.Target {
		t.Fatalf("round-trip mismatch: got %+v want %+v", loaded, svc)
	}
}

func TestRemoveLastService(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "only", Type: TypeProxy, Target: "http://localhost:1000"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	if _, err := Remove(path, "only"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(reg.Services))
	}
}

func TestAddPreservesOtherServices(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "first", Type: TypeProxy, Target: "http://localhost:1000"}); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if _, err := Add(path, Service{Name: "second", Type: TypeFile, Path: "/tmp"}); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}

	if _, err := Add(path, Service{Name: "first", Type: TypeProxy, Target: "http://localhost:2000"}); err != nil {
		t.Fatalf("Add(update first) error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(reg.Services))
	}
}

func TestAdd_PathError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Add(filepath.Join(parent, "registry.json"), Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestAdd_LockFileOpenError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	defer os.Chmod(dir, 0o700)

	_, err := Add(filepath.Join(dir, "registry.json"), Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestSave_NilServices(t *testing.T) {
	path := testRegistryPath(t)

	if err := save(path, &Registry{}); err != nil {
		t.Fatalf("save() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reg.Services == nil {
		t.Fatal("Services should be initialized by save()")
	}
}

func TestSave_PathError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := save(filepath.Join(parent, "registry.json"), &Registry{Services: []Service{{Name: "svc"}}})
	if err == nil {
		t.Fatal("save() error = nil, want error")
	}
}

func TestSave_TempWriteError(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	err := save(path, &Registry{Services: []Service{{Name: "svc"}}})
	if err == nil {
		t.Fatal("save() error = nil, want error")
	}
}

func TestSave_RenameError(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	err := save(path, &Registry{Services: []Service{{Name: "svc"}}})
	if err == nil {
		t.Fatal("save() error = nil, want error")
	}
}

func TestAdd_LoadError(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Add(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestAdd_UpdateSaveError(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := Add(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:4000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestRemove_LoadError(t *testing.T) {
	path := testRegistryPath(t)
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Remove(path, "svc")
	if err == nil {
		t.Fatal("Remove() error = nil, want error")
	}
}

func TestAddTCPService(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name: "db",
		Type: TypeTCP,
		Port: 5432,
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
	if svc.Name != "db" {
		t.Fatalf("expected name db, got %q", svc.Name)
	}
	if svc.Type != TypeTCP {
		t.Fatalf("expected type %q, got %q", TypeTCP, svc.Type)
	}
	if svc.Port != 5432 {
		t.Fatalf("expected port 5432, got %d", svc.Port)
	}
	if svc.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestAddEphemeralService(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:      "temp",
		Type:      TypeProxy,
		Target:    "http://localhost:8080",
		Ephemeral: true,
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
	if !svc.Ephemeral {
		t.Fatal("expected ephemeral to be true")
	}
}

func TestAddServiceWithTags(t *testing.T) {
	path := testRegistryPath(t)

	tags := []string{"tag:web", "tag:production", "tag:api"}
	if _, err := Add(path, Service{
		Name:   "tagged",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
		Tags:   tags,
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
	if len(svc.Tags) != len(tags) {
		t.Fatalf("expected %d tags, got %d", len(tags), len(svc.Tags))
	}
	for i, tag := range tags {
		if svc.Tags[i] != tag {
			t.Fatalf("expected tag[%d] = %q, got %q", i, tag, svc.Tags[i])
		}
	}
}

func TestAddServiceWithControlURL(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:       "custom",
		Type:       TypeProxy,
		Target:     "http://localhost:3000",
		ControlURL: "https://headscale.example.com",
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
	if svc.ControlURL != "https://headscale.example.com" {
		t.Fatalf("expected control_url %q, got %q", "https://headscale.example.com", svc.ControlURL)
	}
}

func TestValidateControlURLRequiresAbsoluteHTTPURL(t *testing.T) {
	valid := []string{
		"",
		"http://headscale.example.com",
		"https://headscale.example.com",
		"https://headscale.example.com:8080/path",
	}
	for _, value := range valid {
		t.Run("valid_"+value, func(t *testing.T) {
			if err := ValidateControlURL(value); err != nil {
				t.Fatalf("ValidateControlURL(%q) error = %v, want nil", value, err)
			}
		})
	}

	invalid := []string{
		"/control",
		"https://",
		"ftp://example.com",
		"not-a-url",
	}
	for _, value := range invalid {
		t.Run("invalid_"+value, func(t *testing.T) {
			err := ValidateControlURL(value)
			if err == nil {
				t.Fatalf("ValidateControlURL(%q) error = nil, want invalid URL error", value)
			}
			if !strings.Contains(err.Error(), "invalid URL") {
				t.Fatalf("ValidateControlURL(%q) error = %v, want invalid URL error", value, err)
			}
		})
	}
}

func TestValidateServiceRejectsInvalidControlURL(t *testing.T) {
	err := ValidateService(Service{
		Name:       "custom",
		Type:       TypeProxy,
		Target:     "http://localhost:3000",
		ControlURL: "not-a-url",
	})
	if err == nil {
		t.Fatal("ValidateService() error = nil, want invalid control_url error")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("ValidateService() error = %v, want invalid URL error", err)
	}
}

func TestAddRejectsInvalidControlURLBeforeMutation(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:       "custom",
		Type:       TypeProxy,
		Target:     "http://localhost:3000",
		ControlURL: "https://control.example.com",
	}); err != nil {
		t.Fatalf("initial Add returned error: %v", err)
	}

	created, err := Add(path, Service{
		Name:       "custom",
		Type:       TypeProxy,
		Target:     "http://localhost:4000",
		ControlURL: "not-a-url",
	})
	if err == nil {
		t.Fatal("Add() error = nil, want invalid control_url error")
	}
	if created {
		t.Fatal("Add() created = true, want false on validation error")
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service after rejected update, got %d", len(reg.Services))
	}
	svc := reg.Services[0]
	if svc.Target != "http://localhost:3000" {
		t.Fatalf("target mutated to %q, want original target", svc.Target)
	}
	if svc.ControlURL != "https://control.example.com" {
		t.Fatalf("control_url mutated to %q, want original control_url", svc.ControlURL)
	}
}

func TestAddRejectsInvalidTag(t *testing.T) {
	path := testRegistryPath(t)

	_, err := Add(path, Service{
		Name:   "tagged",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:"},
	})
	if err == nil {
		t.Fatal("Add() error = nil, want invalid tag error")
	}
}

func TestAddRejectsTCPAllowedUsers(t *testing.T) {
	path := testRegistryPath(t)

	_, err := Add(path, Service{
		Name:         "db",
		Type:         TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	})
	if err == nil {
		t.Fatal("Add() error = nil, want tcp allowed_users error")
	}
	if !strings.Contains(err.Error(), "tcp services do not support allowed_users") {
		t.Fatalf("Add() error = %v, want tcp allowed_users error", err)
	}
}

func TestAddServiceAllFields(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:       "full",
		Type:       TypeTCP,
		Port:       3306,
		Ephemeral:  true,
		Tags:       []string{"tag:db", "tag:internal"},
		ControlURL: "https://control.example.com",
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
	if svc.Name != "full" {
		t.Fatalf("expected name full, got %q", svc.Name)
	}
	if svc.Type != TypeTCP {
		t.Fatalf("expected type %q, got %q", TypeTCP, svc.Type)
	}
	if svc.Port != 3306 {
		t.Fatalf("expected port 3306, got %d", svc.Port)
	}
	if !svc.Ephemeral {
		t.Fatal("expected ephemeral to be true")
	}
	if len(svc.Tags) != 2 || svc.Tags[0] != "tag:db" || svc.Tags[1] != "tag:internal" {
		t.Fatalf("expected tags [tag:db tag:internal], got %v", svc.Tags)
	}
	if svc.ControlURL != "https://control.example.com" {
		t.Fatalf("expected control_url %q, got %q", "https://control.example.com", svc.ControlURL)
	}
	if svc.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestUpdateServicePreservesNewFields(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:       "updatable",
		Type:       TypeTCP,
		Target:     "http://localhost:3000",
		Port:       5432,
		Ephemeral:  true,
		Tags:       []string{"tag:v1", "tag:staging"},
		ControlURL: "https://control.example.com",
	}); err != nil {
		t.Fatalf("first Add returned error: %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load after first Add returned error: %v", err)
	}
	firstCreatedAt := reg.Services[0].CreatedAt

	if _, err := Add(path, Service{
		Name:       "updatable",
		Type:       TypeTCP,
		Target:     "http://localhost:4000",
		Port:       3306,
		Ephemeral:  false,
		Tags:       []string{"tag:v2", "tag:production"},
		ControlURL: "https://new-control.example.com",
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
	if svc.Port != 3306 {
		t.Fatalf("expected port to be updated to 3306, got %d", svc.Port)
	}
	if svc.Ephemeral {
		t.Fatal("expected ephemeral to be updated to false")
	}
	if len(svc.Tags) != 2 || svc.Tags[0] != "tag:v2" || svc.Tags[1] != "tag:production" {
		t.Fatalf("expected tags to be updated to [tag:v2 tag:production], got %v", svc.Tags)
	}
	if svc.ControlURL != "https://new-control.example.com" {
		t.Fatalf("expected control_url to be updated, got %q", svc.ControlURL)
	}
	if !svc.CreatedAt.Equal(firstCreatedAt) {
		t.Fatalf("expected created_at to be preserved, got %v want %v", svc.CreatedAt, firstCreatedAt)
	}
}

func TestSave_MarshalError(t *testing.T) {
	origMarshal := marshalFn
	marshalFn = func(_ any, _ string, _ string) ([]byte, error) {
		return nil, errors.New("injected marshal error")
	}
	defer func() { marshalFn = origMarshal }()

	path := testRegistryPath(t)
	err := save(path, &Registry{Services: []Service{{Name: "svc"}}})
	if err == nil {
		t.Fatal("save() error = nil, want error from marshal failure")
	}
	if err.Error() != "injected marshal error" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWithLock_LockError(t *testing.T) {
	origLock := lockFn
	lockFn = func(_ *os.File) error {
		return errors.New("injected lock error")
	}
	defer func() { lockFn = origLock }()

	path := testRegistryPath(t)
	_, err := Add(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error from lock failure")
	}
	if err.Error() != "injected lock error" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAddServiceWithAcmeEmail(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:      "acme-svc",
		Type:      TypeProxy,
		Target:    "http://localhost:3000",
		Domain:    "app.example.com",
		AcmeEmail: "admin@example.com",
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
	if svc.AcmeEmail != "admin@example.com" {
		t.Fatalf("expected acme_email %q, got %q", "admin@example.com", svc.AcmeEmail)
	}
	if svc.Domain != "app.example.com" {
		t.Fatalf("expected domain %q, got %q", "app.example.com", svc.Domain)
	}
}

func TestAcmeEmailJSONRoundTrip(t *testing.T) {
	path := testRegistryPath(t)

	original := Service{
		Name:      "roundtrip-acme",
		Type:      TypeProxy,
		Target:    "http://localhost:8080",
		Domain:    "test.example.com",
		AcmeEmail: "certs@example.com",
	}
	if _, err := Add(path, original); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	loaded := reg.Services[0]
	if loaded.AcmeEmail != original.AcmeEmail {
		t.Fatalf("AcmeEmail round-trip: got %q, want %q", loaded.AcmeEmail, original.AcmeEmail)
	}
}

func TestAcmeEmailOmittedWhenEmpty(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:   "no-acme",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if strings.Contains(string(data), "acme_email") {
		t.Fatal("acme_email should be omitted from JSON when empty")
	}
}
