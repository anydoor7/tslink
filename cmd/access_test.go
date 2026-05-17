package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func runAccessExplainWithServices(t *testing.T, serviceName string, isJSON bool, services ...registry.Service) (string, AccessExplainResult, error) {
	t.Helper()

	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	for _, svc := range services {
		if _, err := registry.Add(regPath, svc); err != nil {
			t.Fatalf("registry.Add(%s): %v", svc.Name, err)
		}
	}

	oldRegPath := registryPathFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
	})
	registryPathFn = func() (string, error) { return regPath, nil }

	var buf bytes.Buffer
	err := runAccessExplain(serviceName, &buf, isJSON)
	var result AccessExplainResult
	if err == nil && isJSON {
		if decodeErr := json.Unmarshal(buf.Bytes(), &result); decodeErr != nil {
			t.Fatalf("unmarshal access explain JSON: %v\nraw: %s", decodeErr, buf.String())
		}
	}
	return buf.String(), result, err
}

func TestAccessExplainCommandTree(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"access", "explain", "web"})
	if err != nil {
		t.Fatalf("find access explain command: %v", err)
	}
	if cmd == nil || !strings.HasPrefix(cmd.Use, "explain <service>") {
		t.Fatalf("command = %+v, want access explain command", cmd)
	}
}

func TestAccessExplainProxyAllowJSONRedactsAndUsesLocalEnforcement(t *testing.T) {
	raw, result, err := runAccessExplainWithServices(t, "web", true, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://user:pass@localhost:3000/private?token=abc",
		Tags:         []string{"tag:web"},
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	})
	if err != nil {
		t.Fatalf("runAccessExplain: %v", err)
	}

	for _, forbidden := range []string{"alice@example.com", "tag:admin", "user:pass", "token=abc"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("access explain JSON leaked %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(raw, `"tslink_local_enforcement"`) {
		t.Fatalf("JSON missing tslink_local_enforcement: %s", raw)
	}
	if strings.Contains(raw, "tslink_enforces") {
		t.Fatalf("JSON used deprecated tslink_enforces field: %s", raw)
	}

	enforcement := result.TSLinkLocalEnforcement
	if enforcement.Kind != "http_allow_list" || !enforcement.Applies {
		t.Fatalf("local enforcement = %+v, want applied HTTP allow list", enforcement)
	}
	if enforcement.FailureMode != accessIdentityFailureModeDenyWhenUnresolved {
		t.Fatalf("failure_mode = %q, want %q", enforcement.FailureMode, accessIdentityFailureModeDenyWhenUnresolved)
	}
	if enforcement.AllowList.Count != 2 || !enforcement.AllowList.Redacted || len(enforcement.AllowList.Entries) != 0 {
		t.Fatalf("allow_list = %+v, want redacted count only", enforcement.AllowList)
	}
	if result.TSLinkKnown.Allow.Count != 2 || !result.TSLinkKnown.Allow.Redacted {
		t.Fatalf("known allow = %+v, want redacted ServiceView allow summary", result.TSLinkKnown.Allow)
	}
	if result.TSLinkKnown.Backend.Display != "http://localhost:3000/private" {
		t.Fatalf("backend display = %q, want userinfo/query redacted", result.TSLinkKnown.Backend.Display)
	}
	if result.TSLinkKnown.TargetLoopbackClassification.Classification != "loopback_or_local" {
		t.Fatalf("target classification = %+v, want loopback_or_local", result.TSLinkKnown.TargetLoopbackClassification)
	}
}

func TestAccessExplainHumanRedactsAllowAndIncludesCaveats(t *testing.T) {
	raw, _, err := runAccessExplainWithServices(t, "web", false, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("runAccessExplain: %v", err)
	}
	for _, required := range []string{
		"External policy unknown:",
		"Backend auth assumption:",
		accessIdentityFailureModeDenyWhenUnresolved,
	} {
		if !strings.Contains(raw, required) {
			t.Fatalf("human output missing %q:\n%s", required, raw)
		}
	}
	if strings.Contains(raw, "alice@example.com") {
		t.Fatalf("human output leaked allow principal: %s", raw)
	}
}

func TestAccessExplainTCPStatesNoHTTPIdentityOrAllowEnforcement(t *testing.T) {
	raw, result, err := runAccessExplainWithServices(t, "db", true, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	})
	if err != nil {
		t.Fatalf("runAccessExplain: %v", err)
	}

	enforcement := result.TSLinkLocalEnforcement
	if enforcement.Kind != "tcp_no_http_enforcement" || enforcement.Applies {
		t.Fatalf("local enforcement = %+v, want TCP no HTTP enforcement", enforcement)
	}
	if !strings.Contains(enforcement.Summary, "do not receive TSLink HTTP identity or allow-list enforcement") {
		t.Fatalf("tcp summary = %q, want explicit HTTP boundary", enforcement.Summary)
	}
	if !strings.Contains(enforcement.Summary, "raw private route only") {
		t.Fatalf("tcp summary = %q, want raw private route boundary", enforcement.Summary)
	}
	if strings.Contains(raw, `"kind":"http_allow_list"`) || strings.Contains(raw, "allow-list enforcement applies") {
		t.Fatalf("tcp JSON implied HTTP ACL coverage: %s", raw)
	}
}

func TestAccessExplainFunnelMarksPublicAndPolicyUnknown(t *testing.T) {
	_, result, err := runAccessExplainWithServices(t, "public-app", true, registry.Service{
		Name:   "public-app",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
	})
	if err != nil {
		t.Fatalf("runAccessExplain: %v", err)
	}

	if !result.TSLinkKnown.Exposure.Public || !result.TSLinkLocalEnforcement.PublicExposure.Public {
		t.Fatalf("public exposure not marked in result: %+v", result)
	}
	if !strings.Contains(result.TSLinkLocalEnforcement.PublicExposure.Summary, "no private tailnet-only claim") {
		t.Fatalf("public exposure summary = %q, want no private tailnet-only claim", result.TSLinkLocalEnforcement.PublicExposure.Summary)
	}
	if result.ExternalPolicyUnknown.Known {
		t.Fatalf("external_policy_unknown.known = true, want false")
	}
}

func TestAccessExplainServiceWithoutAllowListSaysNoLocalAllowList(t *testing.T) {
	_, result, err := runAccessExplainWithServices(t, "docs", true, registry.Service{
		Name: "docs",
		Type: registry.TypeFile,
		Path: "/srv/docs",
	})
	if err != nil {
		t.Fatalf("runAccessExplain: %v", err)
	}

	enforcement := result.TSLinkLocalEnforcement
	if enforcement.Kind != "no_local_allow_list" || enforcement.Applies {
		t.Fatalf("local enforcement = %+v, want no local allow list", enforcement)
	}
	if !strings.Contains(enforcement.Summary, "No TSLink local user allow-list") {
		t.Fatalf("summary = %q, want no local allow-list", enforcement.Summary)
	}
	if result.TSLinkKnown.TargetLoopbackClassification.Classification != "not_applicable" {
		t.Fatalf("file target classification = %+v, want not_applicable", result.TSLinkKnown.TargetLoopbackClassification)
	}
}

func TestAccessExplainExternalPolicyAndBackendAuthAlwaysPresent(t *testing.T) {
	cases := []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"},
		{Name: "docs", Type: registry.TypeFile, Path: "/srv/docs"},
		{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Port: 5432},
		{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true},
	}
	for _, svc := range cases {
		_, result, err := runAccessExplainWithServices(t, svc.Name, true, svc)
		if err != nil {
			t.Fatalf("%s runAccessExplain: %v", svc.Name, err)
		}
		if result.ExternalPolicyUnknown.Known {
			t.Fatalf("%s external policy known = true, want false", svc.Name)
		}
		for _, layer := range []string{"Tailscale/Headscale membership", "Tailscale ACL/grants", "device sharing", "tag ownership", "Funnel policy"} {
			if !stringSliceContains(result.ExternalPolicyUnknown.UnknownLayers, layer) {
				t.Fatalf("%s missing unknown layer %q: %+v", svc.Name, layer, result.ExternalPolicyUnknown.UnknownLayers)
			}
		}
		if result.BackendAuthAssumption.Proven || !result.BackendAuthAssumption.OutsideTSLink {
			t.Fatalf("%s backend auth assumption = %+v, want outside TSLink and unproven", svc.Name, result.BackendAuthAssumption)
		}
	}
}

func TestAccessExplainServiceNotFoundReturnsSemanticError(t *testing.T) {
	_, _, err := runAccessExplainWithServices(t, "missing", false, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitNotFound)
	}
	var codeErr *output.CodeError
	if !errors.As(err, &codeErr) {
		t.Fatalf("err = %T, want *output.CodeError", err)
	}
	if !strings.Contains(codeErr.Error(), "service not found: missing") {
		t.Fatalf("not found error = %q", codeErr.Error())
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
