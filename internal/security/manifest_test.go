package security

import (
	"encoding/json"
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

// TestCapabilityManifestDeclaresTheAccessLogIdentitySchema pins the manifest
// against the code. The manifest is the published SSOT for what tslink does
// with a caller's identity, and a log line that started naming accounts while
// the manifest still said it named none would be a documented promise the
// product had stopped keeping -- with nothing failing to say so.
func TestCapabilityManifestDeclaresTheAccessLogIdentitySchema(t *testing.T) {
	manifest, err := LoadCapabilityManifest()
	if err != nil {
		t.Fatalf("LoadCapabilityManifest() error = %v", err)
	}
	for _, cap := range manifest.Capabilities {
		expected := "access_v1_http_attested_identity_privacy_preserving"
		if cap.ServiceType == "tcp" {
			expected = "access_v1_tcp_open_close_attested_identity"
		}
		if cap.ServiceType == "funnel" {
			expected = "access_v1_http_public_identity"
		}
		if cap.LogSchema != expected || cap.LogSink != "local_async_bounded_jsonl_access_store" {
			t.Fatalf("%s schema/sink = %s/%s", cap.ID, cap.LogSchema, cap.LogSink)
		}
		if cap.ServiceType == "funnel" {
			if cap.WhoIsCache != "not_applicable_public_identity" {
				t.Fatal(cap.WhoIsCache)
			}
		} else if !strings.HasPrefix(cap.WhoIsCache, "60s_by_source_ip") {
			t.Fatal(cap.WhoIsCache)
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

func TestFunnelAutoProvisionPlanIsDefaultOnAuditableAndDisableable(t *testing.T) {
	plan := FunnelAutoProvisionPlan("tag:tslink-funnel", []string{"tag:tsmain"}, true)
	if plan.SchemaVersion != 1 || plan.ID != "remote.acl.funnel_auto_provision" || !plan.Mutates || plan.Default != "enabled" {
		t.Fatalf("plan identity/default = %+v", plan)
	}
	if plan.OptInFlag != "" || plan.DisableFlag != "--no-auto-provision" {
		t.Fatalf("plan gates = %+v, want default-on kill switch", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, present := fields["opt_in_flag"]; present {
		t.Fatalf("serialized default-on plan contains opt_in_flag: %s", encoded)
	}
	if fields["disable_flag"] != "--no-auto-provision" {
		t.Fatalf("serialized disable_flag = %v, want --no-auto-provision", fields["disable_flag"])
	}
	if got := strings.Join(plan.Resources, ","); got != "tag:tslink-funnel,tag:tsmain" {
		t.Fatalf("plan resources = %q, want shared tag and derived owner", got)
	}
	boundaries := strings.Join(plan.Boundaries, "\n")
	for _, want := range []string{"httpsEnabled", "one Raw HuJSON read", "exact single target", "LosslessHuJSONFusedPatch", "DaemonKillSwitchWins"} {
		if !strings.Contains(boundaries, want) {
			t.Fatalf("plan boundaries = %q, want %q", boundaries, want)
		}
	}
}

func TestInviteMutationPlanAuditsExplicitCommandWithoutLeakingURL(t *testing.T) {
	plan := InviteMutationPlan("device", "create", "82001", "app", 11055)
	if plan.SchemaVersion != 1 || plan.ID != "remote.invite.device.create" || plan.Operation != "create_device_invite" || plan.RemoteSystem != "tailscale_device_invites" || !plan.Mutates || plan.Default != "explicit_command" {
		t.Fatalf("plan identity = %+v", plan)
	}
	if plan.OptInFlag != "" || plan.DisableFlag != "" {
		t.Fatalf("invite plan = %+v, want no redundant confirmation flag", plan)
	}
	joined := strings.Join(plan.Boundaries, "\n")
	for _, want := range []string{"user-owned tskey-api-", "OAuth client secrets are rejected", "--print-link", "never constructed", "stable nodeId"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("boundaries = %q, want %q", joined, want)
		}
	}
	if got := strings.Join(plan.Resources, ","); got != "82001,app,device:11055" || strings.Contains(got, "@") || strings.Contains(got, "login.tailscale.com") {
		t.Fatalf("resources = %q, want invite/device identifiers without recipient PII or bearer URL", got)
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
