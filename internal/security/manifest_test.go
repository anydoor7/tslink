package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCapabilityManifestSecuritySemantics(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatalf("LoadCapabilityManifest() error = %v", err)
	}
	wantTypes := map[string]bool{"proxy": false, "file": false, "tcp": false, "funnel": false}
	for _, cap := range manifest.Capabilities {
		if _, ok := wantTypes[cap.ServiceType]; ok {
			wantTypes[cap.ServiceType] = true
		}
		if cap.HostIsolation != "not_provided" {
			t.Fatalf("%s host isolation = %q, want not_provided", cap.ID, cap.HostIsolation)
		}
		if cap.ComplianceStatus != "not_certified" {
			t.Fatalf("%s compliance = %q, want not_certified", cap.ID, cap.ComplianceStatus)
		}
	}
	for typ, seen := range wantTypes {
		if !seen {
			t.Fatalf("manifest missing service_type %q", typ)
		}
	}
}

func TestACLSideEffectPlanIsHighFrictionOptIn(t *testing.T) {
	plan := ACLMutationPlan("ensure_tags", []string{"tag:tsmain"}, false)
	if plan.SchemaVersion != 1 || plan.ID != "remote.acl.mutation" {
		t.Fatalf("plan identity = %+v", plan)
	}
	if plan.Mutates {
		t.Fatalf("disabled plan Mutates = true")
	}
	if plan.OptInFlag != "--manage-acl" || !strings.Contains(strings.Join(plan.Boundaries, " "), "disabled by default") {
		t.Fatalf("plan = %+v, want explicit --manage-acl disabled boundary", plan)
	}
	enabled := ACLMutationPlan("ensure_tags", []string{"tag:tsmain"}, true)
	if !enabled.Mutates || enabled.Default != "explicitly_enabled" {
		t.Fatalf("enabled plan = %+v, want mutating explicit opt-in", enabled)
	}
}

func TestSecurityCriticalClaimsStayWithinCapabilityManifest(t *testing.T) {
	root := repoRoot(t)
	paths := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "README_zh.md"),
		filepath.Join(root, "SECURITY.md"),
		filepath.Join(root, "cmd", "root.go"),
	}
	banned := []string{
		"end-to-end encrypted",
		"e2e encrypted",
		"host isolation",
		"certified",
		"certification",
		"nist compliant",
		"nist-certified",
		"hipaa",
		"soc 2",
		"mandatory identity",
		"always verify",
	}
	required := []string{
		"tailnet transport",
		"raw tcp",
		"whois",
		"--allow",
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		text := string(data)
		lower := strings.ToLower(text)
		for _, phrase := range banned {
			if strings.Contains(lower, phrase) {
				t.Fatalf("%s contains unsupported absolute %q", path, phrase)
			}
		}
		for _, phrase := range required {
			if !strings.Contains(lower, phrase) {
				t.Fatalf("%s missing security-critical anchor %q", path, phrase)
			}
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
