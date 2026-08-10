package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const registryAddHelperEnv = "TSLINK_REGISTRY_ADD_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(registryAddHelperEnv) == "1" {
		path := os.Getenv("TSLINK_REGISTRY_ADD_PATH")
		name := os.Getenv("TSLINK_REGISTRY_ADD_NAME")
		target := os.Getenv("TSLINK_REGISTRY_ADD_TARGET")
		if path == "" || name == "" || target == "" {
			fmt.Fprintln(os.Stderr, "registry add helper: incomplete synthetic fixture")
			os.Exit(2)
		}
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: target}); err != nil {
			fmt.Fprintf(os.Stderr, "registry add helper failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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
	if reg.SchemaVersion != CurrentRegistrySchemaVersion {
		t.Fatalf("schema_version = %d, want %d", reg.SchemaVersion, CurrentRegistrySchemaVersion)
	}
}

func TestAddConcurrentGoroutinesPreservesExactRegistry(t *testing.T) {
	const additions = 32
	path := testRegistryPath(t)

	var wg sync.WaitGroup
	errs := make(chan error, additions)
	for i := 0; i < additions; i++ {
		name := fmt.Sprintf("goroutine-%02d", i)
		target := fmt.Sprintf("http://127.0.0.1:%d", 3000+i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := Add(path, Service{Name: name, Type: TypeProxy, Target: target})
			if err != nil {
				errs <- err
				return
			}
			if !created {
				errs <- fmt.Errorf("distinct service was unexpectedly updated")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Add() error = %v", err)
	}

	assertExactConcurrentServices(t, path, "goroutine", additions, 3000)
}

func TestAddConcurrentProcessesPreservesExactRegistry(t *testing.T) {
	const additions = 12
	path := testRegistryPath(t)

	type processResult struct {
		index  int
		output []byte
		err    error
	}
	results := make(chan processResult, additions)
	for i := 0; i < additions; i++ {
		name := fmt.Sprintf("process-%02d", i)
		target := fmt.Sprintf("http://127.0.0.1:%d", 4000+i)
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(),
			registryAddHelperEnv+"=1",
			"TSLINK_REGISTRY_ADD_PATH="+path,
			"TSLINK_REGISTRY_ADD_NAME="+name,
			"TSLINK_REGISTRY_ADD_TARGET="+target,
		)
		go func(index int) {
			output, err := cmd.CombinedOutput()
			results <- processResult{index: index, output: output, err: err}
		}(i)
	}
	for i := 0; i < additions; i++ {
		result := <-results
		if result.err != nil {
			t.Fatalf("helper process %d failed: %v; output=%s", result.index, result.err, result.output)
		}
	}

	assertExactConcurrentServices(t, path, "process", additions, 4000)
}

func assertExactConcurrentServices(t *testing.T, path, prefix string, count, firstPort int) {
	t.Helper()
	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(final) error = %v", err)
	}
	if len(reg.Services) != count {
		t.Fatalf("final service count = %d, want %d", len(reg.Services), count)
	}

	want := make(map[string]string, count)
	for i := 0; i < count; i++ {
		want[fmt.Sprintf("%s-%02d", prefix, i)] = fmt.Sprintf("http://127.0.0.1:%d", firstPort+i)
	}
	for _, svc := range reg.Services {
		target, ok := want[svc.Name]
		if !ok {
			t.Fatalf("unexpected or duplicate service in final registry: %q", svc.Name)
		}
		if svc.Type != TypeProxy || svc.Target != target {
			t.Fatalf("service %q has incorrect final contents", svc.Name)
		}
		delete(want, svc.Name)
	}
	if len(want) != 0 {
		t.Fatalf("final registry is missing %d expected services", len(want))
	}
}

func TestLoadLegacyRegistryWithoutSchemaVersionThenSaveAddsCurrentVersion(t *testing.T) {
	path := testRegistryPath(t)
	legacy := `{
  "services": [
    {
      "name": "web",
      "type": "proxy",
      "target": "http://localhost:3000",
      "tags": ["tag:web"],
      "created_at": "2026-07-06T12:00:00Z"
    },
    {
      "name": "db",
      "type": "tcp",
      "target": "localhost:5432",
      "port": 5432,
      "created_at": "2026-07-06T12:01:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() legacy registry error = %v", err)
	}
	if reg.SchemaVersion != CurrentRegistrySchemaVersion {
		t.Fatalf("schema_version = %d, want %d", reg.SchemaVersion, CurrentRegistrySchemaVersion)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("services = %+v, want 2 legacy services intact", reg.Services)
	}
	if reg.Services[0].Name != "web" || reg.Services[0].Target != "http://localhost:3000" || reg.Services[0].Tags[0] != "tag:web" {
		t.Fatalf("web service did not round-trip intact: %+v", reg.Services[0])
	}
	if reg.Services[1].Name != "db" || reg.Services[1].Type != TypeTCP || reg.Services[1].Port != 5432 {
		t.Fatalf("db service did not round-trip intact: %+v", reg.Services[1])
	}

	if err := save(path, reg); err != nil {
		t.Fatalf("save() migrated registry error = %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() migrated registry error = %v", err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("unmarshal saved registry: %v\nraw: %s", err, raw)
	}
	var version int
	if err := json.Unmarshal(saved["schema_version"], &version); err != nil {
		t.Fatalf("schema_version missing or invalid after save: %v\nraw: %s", err, raw)
	}
	if version != CurrentRegistrySchemaVersion {
		t.Fatalf("saved schema_version = %d, want %d", version, CurrentRegistrySchemaVersion)
	}
	var services []Service
	if err := json.Unmarshal(saved["services"], &services); err != nil {
		t.Fatalf("services missing or invalid after save: %v\nraw: %s", err, raw)
	}
	if len(services) != 2 || services[0].Name != "web" || services[1].Name != "db" {
		t.Fatalf("saved services = %+v, want legacy services intact", services)
	}
}

func TestLoadRejectsUnavailableFeatureFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "custom domain",
			raw:  `{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000","domain":"app.example.com"}]}`,
		},
		{
			name: "middleware",
			raw:  `{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000","middleware":{"basic_auth":"user:pass"}}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := testRegistryPath(t)
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil, want feature_unavailable")
			}
			if code, ok := ErrorCode(err); !ok || code != CodeFeatureUnavailable {
				t.Fatalf("ErrorCode() = %q,%v; want %s,true; err=%v", code, ok, CodeFeatureUnavailable, err)
			}
			if strings.Contains(err.Error(), "user:pass") {
				t.Fatalf("Load() error leaked middleware secret: %v", err)
			}
		})
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

func TestAddIfMissingCreatesWhenAbsent(t *testing.T) {
	path := testRegistryPath(t)

	created, err := AddIfMissing(path, Service{
		Name:   "report",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tslink"},
	})
	if err != nil {
		t.Fatalf("AddIfMissing returned error: %v", err)
	}
	if !created {
		t.Fatal("AddIfMissing created = false, want true")
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	svc := reg.Services[0]
	if svc.Name != "report" || svc.Target != "http://localhost:3000" {
		t.Fatalf("service = %+v, want report target http://localhost:3000", svc)
	}
	if len(svc.Tags) != 1 || svc.Tags[0] != "tag:tslink" {
		t.Fatalf("tags = %v, want [tag:tslink]", svc.Tags)
	}
	if svc.CreatedAt.IsZero() {
		t.Fatal("expected created_at to be set")
	}
}

func TestAddIfMissingNoopWhenPresentPreservesExistingService(t *testing.T) {
	path := testRegistryPath(t)
	createdAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)

	if _, err := Add(path, Service{
		Name:      "report",
		Type:      TypeProxy,
		Target:    "http://localhost:3000",
		Tags:      []string{"tag:custom"},
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("initial Add returned error: %v", err)
	}

	created, err := AddIfMissing(path, Service{
		Name:      "report",
		Type:      TypeProxy,
		Target:    "http://localhost:4000",
		Tags:      []string{"tag:tslink"},
		CreatedAt: time.Date(2026, 5, 17, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AddIfMissing returned error: %v", err)
	}
	if created {
		t.Fatal("AddIfMissing created = true, want false")
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	svc := reg.Services[0]
	if svc.Target != "http://localhost:3000" {
		t.Fatalf("target = %q, want original target", svc.Target)
	}
	if len(svc.Tags) != 1 || svc.Tags[0] != "tag:custom" {
		t.Fatalf("tags = %v, want original tags [tag:custom]", svc.Tags)
	}
	if !svc.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %v, want original %v", svc.CreatedAt, createdAt)
	}
}

func TestAddIfMissingRejectsInvalidNameAndTag(t *testing.T) {
	path := testRegistryPath(t)

	cases := []struct {
		name string
		svc  Service
	}{
		{
			name: "invalid name",
			svc:  Service{Name: "INVALID", Type: TypeProxy, Target: "http://localhost:3000"},
		},
		{
			name: "invalid tag",
			svc:  Service{Name: "tagged", Type: TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			created, err := AddIfMissing(path, tc.svc)
			if err == nil {
				t.Fatal("AddIfMissing error = nil, want validation error")
			}
			if created {
				t.Fatal("AddIfMissing created = true, want false")
			}
		})
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("services = %+v, want none after rejected inputs", reg.Services)
	}
}

func TestAddIfMissingPreservesOtherExistingServices(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "first", Type: TypeProxy, Target: "http://localhost:1000", Tags: []string{"tag:first"}}); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if _, err := Add(path, Service{Name: "second", Type: TypeProxy, Target: "http://localhost:2000", Tags: []string{"tag:second"}}); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}

	created, err := AddIfMissing(path, Service{Name: "third", Type: TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:third"}})
	if err != nil {
		t.Fatalf("AddIfMissing(third) error = %v", err)
	}
	if !created {
		t.Fatal("AddIfMissing(third) created = false, want true")
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(reg.Services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(reg.Services))
	}
	wantTargets := map[string]string{
		"first":  "http://localhost:1000",
		"second": "http://localhost:2000",
		"third":  "http://localhost:3000",
	}
	for _, svc := range reg.Services {
		if wantTargets[svc.Name] != svc.Target {
			t.Fatalf("%s target = %q, want %q", svc.Name, svc.Target, wantTargets[svc.Name])
		}
	}
}

func TestAddIfMissing_LockError(t *testing.T) {
	origLock := lockFn
	lockFn = func(_ *os.File) error {
		return errors.New("injected lock error")
	}
	defer func() { lockFn = origLock }()

	path := testRegistryPath(t)
	created, err := AddIfMissing(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:3000"})
	if err == nil {
		t.Fatal("AddIfMissing error = nil, want error from lock failure")
	}
	if created {
		t.Fatal("AddIfMissing created = true, want false")
	}
	if err.Error() != "injected lock error" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemove(t *testing.T) {
	path := testRegistryPath(t)
	docsDir := t.TempDir()

	if _, err := Add(path, Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("Add report returned error: %v", err)
	}
	if _, err := Add(path, Service{Name: "docs", Type: TypeFile, Path: docsDir}); err != nil {
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
	docsDir := t.TempDir()

	original := Service{Name: "report", Type: TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	if _, err := Add(path, original); err != nil {
		t.Fatalf("Add report returned error: %v", err)
	}
	if _, err := Add(path, Service{Name: "docs", Type: TypeFile, Path: docsDir}); err != nil {
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

func TestValidateName_DNSLabelLength(t *testing.T) {
	// Exactly 63 characters should be valid (if it matches the regex).
	name63 := strings.Repeat("a", 63)
	if err := ValidateName(name63); err != nil {
		t.Errorf("ValidateName(63 chars) = error %v, want valid", err)
	}

	// 64 characters should be rejected.
	name64 := strings.Repeat("a", 64)
	err := ValidateName(name64)
	if err == nil {
		t.Fatal("ValidateName(64 chars) = nil, want error")
	}
	if !strings.Contains(err.Error(), "DNS label") {
		t.Errorf("expected DNS label error, got: %v", err)
	}

	// Very long name should also be rejected.
	name200 := strings.Repeat("b", 200)
	err = ValidateName(name200)
	if err == nil {
		t.Fatal("ValidateName(200 chars) = nil, want error")
	}
	if !strings.Contains(err.Error(), "63-character") {
		t.Errorf("expected 63-character limit error, got: %v", err)
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
	lockPath := filepath.Join(dir, "registry.json.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("Mkdir(lock) error = %v", err)
	}

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

func TestSave_IgnoresMaliciousFixedTempPath(t *testing.T) {
	path := testRegistryPath(t)
	old := &Registry{Services: []Service{{Name: "old", Type: TypeProxy, Target: "http://localhost:3000"}}}
	if err := save(path, old); err != nil {
		t.Fatalf("save(old) error = %v", err)
	}
	if err := os.Mkdir(path+".tmp", 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	err := save(path, &Registry{Services: []Service{{Name: "new", Type: TypeProxy, Target: "http://localhost:4000"}}})
	if err != nil {
		t.Fatalf("save() error = %v", err)
	}
	if info, err := os.Stat(path + ".tmp"); err != nil || !info.IsDir() {
		t.Fatalf("fixed temp path should remain untouched directory, info=%v err=%v", info, err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "new" {
		t.Fatalf("registry = %+v, want new service despite malicious fixed temp", reg.Services)
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
	lockPath := path + ".lock"
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("Remove(lock) error = %v", err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatalf("Mkdir(lock) error = %v", err)
	}

	_, err := Add(path, Service{Name: "svc", Type: TypeProxy, Target: "http://localhost:4000"})
	if err == nil {
		t.Fatal("Add() error = nil, want error")
	}
}

func TestMutateServiceMergesWithLatestRegistryState(t *testing.T) {
	path := testRegistryPath(t)
	if _, err := Add(path, Service{Name: "web", Type: TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}}); err != nil {
		t.Fatalf("Add(initial) error = %v", err)
	}
	stale, err := Load(path)
	if err != nil {
		t.Fatalf("Load(stale) error = %v", err)
	}
	if _, err := Add(path, Service{Name: "web", Type: TypeProxy, Target: "http://localhost:4000", Tags: []string{"tag:old"}}); err != nil {
		t.Fatalf("Add(concurrent target edit) error = %v", err)
	}

	if _, err := MutateService(path, "web", func(svc Service) (Service, error) {
		if svc.Target == stale.Services[0].Target {
			t.Fatalf("MutateService saw stale target %q, want latest registry state", svc.Target)
		}
		svc.Tags = append(svc.Tags, "tag:new")
		return svc, nil
	}); err != nil {
		t.Fatalf("MutateService() error = %v", err)
	}

	reg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(final) error = %v", err)
	}
	got := reg.Services[0]
	if got.Target != "http://localhost:4000" {
		t.Fatalf("target = %q, want concurrent target edit preserved", got.Target)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "tag:old" || got.Tags[1] != "tag:new" {
		t.Fatalf("tags = %v, want merged tag update", got.Tags)
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
		Name:   "db",
		Type:   TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
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

func TestValidateServiceDiscriminatedShape(t *testing.T) {
	fileRoot := t.TempDir()
	fileAsPath := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(fileAsPath, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missingPath := filepath.Join(t.TempDir(), "missing")

	valid := []Service{
		{Name: "proxy-http", Type: TypeProxy, Target: "http://localhost:3000"},
		{Name: "proxy-https", Type: TypeProxy, Target: "https://example.com"},
		{Name: "files", Type: TypeFile, Path: fileRoot},
		{Name: "tcp", Type: TypeTCP, Target: "localhost:5432", Port: 5432},
	}
	for _, svc := range valid {
		t.Run("valid_"+svc.Name, func(t *testing.T) {
			if err := ValidateService(svc); err != nil {
				t.Fatalf("ValidateService(%+v) error = %v", svc, err)
			}
		})
	}

	invalid := []Service{
		{Name: "unknown", Type: "websocket", Target: "http://localhost:3000"},
		{Name: "proxy-empty", Type: TypeProxy},
		{Name: "proxy-relative", Type: TypeProxy, Target: "localhost:3000"},
		{Name: "proxy-hostless", Type: TypeProxy, Target: "https:///app"},
		{Name: "proxy-unsupported", Type: TypeProxy, Target: "ftp://example.com"},
		{Name: "proxy-path", Type: TypeProxy, Target: "http://localhost:3000", Path: fileRoot},
		{Name: "proxy-port", Type: TypeProxy, Target: "http://localhost:3000", Port: 443},
		{Name: "file-empty", Type: TypeFile},
		{Name: "file-relative", Type: TypeFile, Path: "relative"},
		{Name: "file-missing", Type: TypeFile, Path: missingPath},
		{Name: "file-notdir", Type: TypeFile, Path: fileAsPath},
		{Name: "file-target", Type: TypeFile, Path: fileRoot, Target: "http://localhost:3000"},
		{Name: "file-port", Type: TypeFile, Path: fileRoot, Port: 443},
		{Name: "tcp-empty", Type: TypeTCP},
		{Name: "tcp-hostless", Type: TypeTCP, Target: ":5432", Port: 5432},
		{Name: "tcp-relative", Type: TypeTCP, Target: "localhost", Port: 5432},
		{Name: "tcp-nonnum", Type: TypeTCP, Target: "localhost:abc"},
		{Name: "tcp-zero", Type: TypeTCP, Target: "localhost:0", Port: 0},
		{Name: "tcp-overflow", Type: TypeTCP, Target: "localhost:70000", Port: 70000},
		{Name: "tcp-path", Type: TypeTCP, Target: "localhost:5432", Port: 5432, Path: fileRoot},
		{Name: "tcp-allow", Type: TypeTCP, Target: "localhost:5432", Port: 5432, AllowedUsers: []string{"alice@example.com"}},
		{Name: "domain", Type: TypeProxy, Target: "http://localhost:3000", Domain: "app.example.com"},
		{Name: "acme", Type: TypeProxy, Target: "http://localhost:3000", AcmeEmail: "admin@example.com"},
	}
	for _, svc := range invalid {
		t.Run("invalid_"+svc.Name, func(t *testing.T) {
			if err := ValidateService(svc); err == nil {
				t.Fatalf("ValidateService(%+v) error = nil, want error", svc)
			}
		})
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

func TestValidateServiceRejectsFunnelAllowedUsers(t *testing.T) {
	err := ValidateService(Service{
		Name:         "public-app",
		Type:         TypeProxy,
		Target:       "http://localhost:3000",
		Funnel:       true,
		AllowedUsers: []string{"alice@example.com"},
	})
	if err == nil {
		t.Fatal("ValidateService() error = nil, want funnel allowed_users error")
	}
	if !strings.Contains(err.Error(), ErrFunnelAllowedUsers) {
		t.Fatalf("ValidateService() error = %v, want funnel allowed_users error", err)
	}
	if !strings.Contains(err.Error(), CodeFunnelAllowConflict) {
		t.Fatalf("ValidateService() error = %v, want stable code %s", err, CodeFunnelAllowConflict)
	}
	if code, ok := ErrorCode(err); !ok || code != CodeFunnelAllowConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, CodeFunnelAllowConflict)
	}
}

func TestValidateServiceRejectsFunnelWithoutPublicAck(t *testing.T) {
	err := ValidateService(Service{
		Name:   "public-app",
		Type:   TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
	})
	if err == nil {
		t.Fatal("ValidateService() error = nil, want public_ack error")
	}
	if !strings.Contains(err.Error(), ErrFunnelPublicAck) {
		t.Fatalf("ValidateService() error = %v, want public_ack error", err)
	}
	if !strings.Contains(err.Error(), CodeFunnelPublicAckRequired) {
		t.Fatalf("ValidateService() error = %v, want stable code %s", err, CodeFunnelPublicAckRequired)
	}
	if code, ok := ErrorCode(err); !ok || code != CodeFunnelPublicAckRequired {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, CodeFunnelPublicAckRequired)
	}
}

func TestValidateServiceAcceptsFunnelWithPublicAck(t *testing.T) {
	err := ValidateService(Service{
		Name:      "public-app",
		Type:      TypeProxy,
		Target:    "http://localhost:3000",
		Funnel:    true,
		PublicAck: true,
	})
	if err != nil {
		t.Fatalf("ValidateService() error = %v, want nil", err)
	}
}

func TestValidateServiceRejectsFunnelControlURL(t *testing.T) {
	err := ValidateService(Service{
		Name:       "public-app",
		Type:       TypeProxy,
		Target:     "http://localhost:3000",
		Funnel:     true,
		ControlURL: "https://headscale.example.com",
	})
	if err == nil {
		t.Fatal("ValidateService() error = nil, want funnel control_url error")
	}
	if !strings.Contains(err.Error(), ErrFunnelControlURL) {
		t.Fatalf("ValidateService() error = %v, want funnel control_url error", err)
	}
	if !strings.Contains(err.Error(), CodeFunnelControlURLConflict) {
		t.Fatalf("ValidateService() error = %v, want stable code %s", err, CodeFunnelControlURLConflict)
	}
	if code, ok := ErrorCode(err); !ok || code != CodeFunnelControlURLConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, CodeFunnelControlURLConflict)
	}
}

func TestValidateServiceRejectsFunnelNonProxyTypes(t *testing.T) {
	cases := []Service{
		{
			Name:   "public-files",
			Type:   TypeFile,
			Path:   "/tmp/public-files",
			Funnel: true,
		},
		{
			Name:   "public-db",
			Type:   TypeTCP,
			Target: "localhost:5432",
			Port:   5432,
			Funnel: true,
		},
	}
	for _, svc := range cases {
		t.Run(svc.Type, func(t *testing.T) {
			err := ValidateService(svc)
			if err == nil {
				t.Fatal("ValidateService() error = nil, want funnel type conflict error")
			}
			if !strings.Contains(err.Error(), ErrFunnelTypeConflict) {
				t.Fatalf("ValidateService() error = %v, want funnel type conflict error", err)
			}
			if !strings.Contains(err.Error(), CodeFunnelTypeConflict) {
				t.Fatalf("ValidateService() error = %v, want stable code %s", err, CodeFunnelTypeConflict)
			}
			if code, ok := ErrorCode(err); !ok || code != CodeFunnelTypeConflict {
				t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, CodeFunnelTypeConflict)
			}
		})
	}
}

func TestAddServiceAllFields(t *testing.T) {
	path := testRegistryPath(t)

	if _, err := Add(path, Service{
		Name:       "full",
		Type:       TypeTCP,
		Target:     "localhost:3306",
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
		Target:     "localhost:5432",
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
		Target:     "localhost:3306",
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
	if svc.Target != "localhost:3306" {
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

func TestAddRejectsDomainAndAcmeEmail(t *testing.T) {
	path := testRegistryPath(t)

	cases := []Service{
		{Name: "domain-svc", Type: TypeProxy, Target: "http://localhost:3000", Domain: "app.example.com"},
		{Name: "acme-svc", Type: TypeProxy, Target: "http://localhost:3000", AcmeEmail: "admin@example.com"},
	}
	for _, svc := range cases {
		t.Run(svc.Name, func(t *testing.T) {
			created, err := Add(path, svc)
			if err == nil {
				t.Fatal("Add() error = nil, want custom-domain/ACME unavailable error")
			}
			if created {
				t.Fatal("Add() created = true, want false")
			}
			if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
				t.Fatalf("Add() error = %v, want custom-domain/ACME unavailable error", err)
			}
		})
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
