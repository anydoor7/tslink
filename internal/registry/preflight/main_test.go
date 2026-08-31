package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsCanonicalRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(path, &stdout, &stderr); code != 0 {
		t.Fatalf("run() code = %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var report reportJSON
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !report.OK || report.ValidServices != 1 || report.TotalServices != 1 || len(report.Issues) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestRunReportsAllowHintAndGlobalErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"services":[{"name":"public-app","type":"proxy","target":"http://localhost:3000","allow":["alice@example.com"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(path, &stdout, &stderr); code != 1 {
		t.Fatalf("run(issue) code = %d, stdout=%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "registry.json uses allowed_users; allow is API-only") {
		t.Fatalf("issue output missing allow hint: %s", stdout.String())
	}
	stdout.Reset()
	if code := run("", &stdout, &stderr); code != 2 || !strings.Contains(stdout.String(), "-registry is required") {
		t.Fatalf("run(empty) code/output = %d %s", code, stdout.String())
	}
	stdout.Reset()
	if code := run(filepath.Join(t.TempDir(), "missing"), &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "global_error") {
		t.Fatalf("run(missing) code/output = %d %s", code, stdout.String())
	}
}
