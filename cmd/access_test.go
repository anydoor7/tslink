package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
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

func runAccessExplainWithRawServices(t *testing.T, serviceName string, isJSON bool, services ...registry.Service) (string, AccessExplainResult, error) {
	t.Helper()

	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	payload, err := json.Marshal(registry.Registry{Services: services})
	if err != nil {
		t.Fatalf("marshal raw registry: %v", err)
	}
	if err := os.WriteFile(regPath, payload, 0o600); err != nil {
		t.Fatalf("write raw registry: %v", err)
	}

	oldRegPath := registryPathFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
	})
	registryPathFn = func() (string, error) { return regPath, nil }

	var buf bytes.Buffer
	err = runAccessExplain(serviceName, &buf, isJSON)
	var result AccessExplainResult
	if err == nil && isJSON {
		if decodeErr := json.Unmarshal(buf.Bytes(), &result); decodeErr != nil {
			t.Fatalf("unmarshal raw access explain JSON: %v\nraw: %s", decodeErr, buf.String())
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
		"Out-of-scope layers: backend application authentication, database authentication, SSH authentication",
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

func TestAccessExplainBackendDisplayRedactsSchemelessSecrets(t *testing.T) {
	cases := []struct {
		name               string
		svc                registry.Service
		wantDisplay        string
		forbidden          []string
		wantClassification string
		wantHost           string
		wantPort           string
	}{
		{
			name: "schemeless proxy target",
			svc: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "localhost:3000?token=abc#frag",
			},
			wantDisplay:        "localhost:3000",
			forbidden:          []string{"token=abc", "#frag"},
			wantClassification: "loopback_or_local",
			wantHost:           "localhost",
			wantPort:           "3000",
		},
		{
			name: "schemeless proxy userinfo target",
			svc: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "user:pass@localhost:5432",
			},
			wantDisplay:        "localhost:5432",
			forbidden:          []string{"user:pass"},
			wantClassification: "loopback_or_local",
			wantHost:           "localhost",
			wantPort:           "5432",
		},
		{
			name: "tcp userinfo target",
			svc: registry.Service{
				Name:   "db",
				Type:   registry.TypeTCP,
				Target: "user:pass@localhost:5432",
				Port:   5432,
			},
			wantDisplay: "localhost:5432",
			forbidden:   []string{"user:pass"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/json", func(t *testing.T) {
			raw, result, err := runAccessExplainWithServices(t, tc.svc.Name, true, tc.svc)
			if err != nil {
				t.Fatalf("runAccessExplain JSON: %v", err)
			}
			if result.TSLinkKnown.Backend.Display != tc.wantDisplay {
				t.Fatalf("backend display = %q, want %q", result.TSLinkKnown.Backend.Display, tc.wantDisplay)
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(raw, forbidden) {
					t.Fatalf("access explain JSON leaked %q: %s", forbidden, raw)
				}
			}
			if tc.wantClassification != "" {
				classification := result.TSLinkKnown.TargetLoopbackClassification
				if classification.Classification != tc.wantClassification || classification.Host != tc.wantHost || classification.Port != tc.wantPort {
					t.Fatalf("target classification = %+v, want classification %q host %q port %q", classification, tc.wantClassification, tc.wantHost, tc.wantPort)
				}
			}
		})

		t.Run(tc.name+"/human", func(t *testing.T) {
			raw, _, err := runAccessExplainWithServices(t, tc.svc.Name, false, tc.svc)
			if err != nil {
				t.Fatalf("runAccessExplain human: %v", err)
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(raw, forbidden) {
					t.Fatalf("access explain human output leaked %q:\n%s", forbidden, raw)
				}
			}
			if !strings.Contains(raw, "Backend: "+tc.wantDisplay) {
				t.Fatalf("human output missing sanitized backend %q:\n%s", tc.wantDisplay, raw)
			}
		})
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

func TestAccessExplainTCPAllowedUsersSurfacesWarningsWithoutPrincipals(t *testing.T) {
	rawJSON, result, err := runAccessExplainWithRawServices(t, "db", true, registry.Service{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("runAccessExplain JSON: %v", err)
	}
	if strings.Contains(rawJSON, "alice@example.com") {
		t.Fatalf("access explain JSON leaked allow principal: %s", rawJSON)
	}
	if !hasAccessWarningCode(result.TSLinkKnown.Warnings, inspect.WarningCodeTCPAllowedUsersInvalid) {
		t.Fatalf("known warnings = %+v, want %s", result.TSLinkKnown.Warnings, inspect.WarningCodeTCPAllowedUsersInvalid)
	}
	if result.TSLinkKnown.Allow.Count != 1 || !result.TSLinkKnown.Allow.Redacted {
		t.Fatalf("known allow = %+v, want redacted count", result.TSLinkKnown.Allow)
	}

	rawHuman, _, err := runAccessExplainWithRawServices(t, "db", false, registry.Service{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	})
	if err != nil {
		t.Fatalf("runAccessExplain human: %v", err)
	}
	if strings.Contains(rawHuman, "alice@example.com") {
		t.Fatalf("access explain human output leaked allow principal:\n%s", rawHuman)
	}
	for _, required := range []string{
		inspect.WarningCodeTCPAllowedUsersInvalid,
		"Raw TCP services cannot enforce allowed_users",
	} {
		if !strings.Contains(rawHuman, required) {
			t.Fatalf("human output missing %q:\n%s", required, rawHuman)
		}
	}
}

func TestAccessExplainFunnelMarksPublicAndPolicyUnknown(t *testing.T) {
	_, result, err := runAccessExplainWithServices(t, "public-app", true, registry.Service{
		Name:   "public-app",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
		PublicAck: true,
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

func TestAccessExplainCustomDomainIncludesDNSAndCertificateCaveat(t *testing.T) {
	_, result, err := runAccessExplainWithServices(t, "site", true, registry.Service{
		Name:   "site",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Domain: "site.example.com",
	})
	if err != nil {
		t.Fatalf("runAccessExplain JSON: %v", err)
	}
	for _, layer := range []string{"custom domain DNS", "custom domain certificate posture"} {
		if !stringSliceContains(result.ExternalPolicyUnknown.UnknownLayers, layer) {
			t.Fatalf("unknown layers missing %q: %+v", layer, result.ExternalPolicyUnknown.UnknownLayers)
		}
	}
	if !stringSliceContains(result.TSLinkLocalEnforcement.Notes, "Custom domain reachability depends on external DNS and certificate posture, which this command does not evaluate.") {
		t.Fatalf("notes = %+v, want custom-domain caveat", result.TSLinkLocalEnforcement.Notes)
	}

	raw, _, err := runAccessExplainWithServices(t, "site", false, registry.Service{
		Name:   "site",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Domain: "site.example.com",
	})
	if err != nil {
		t.Fatalf("runAccessExplain human: %v", err)
	}
	for _, required := range []string{
		"Custom domain reachability depends on external DNS and certificate posture",
		"custom domain DNS",
		"custom domain certificate posture",
	} {
		if !strings.Contains(raw, required) {
			t.Fatalf("human output missing %q:\n%s", required, raw)
		}
	}
}

func TestAccessExplainTCPFunnelIncludesRawTCPCaveat(t *testing.T) {
	raw, result, err := runAccessExplainWithRawServices(t, "db", true, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
		Funnel: true,
	})
	if err != nil {
		t.Fatalf("runAccessExplain JSON: %v", err)
	}
	if !stringSliceContains(result.TSLinkLocalEnforcement.Notes, "Tailscale Funnel does not carry raw TCP; a hand-edited TCP Funnel registry mark is not proof of public reachability.") {
		t.Fatalf("notes = %+v, want raw TCP Funnel caveat", result.TSLinkLocalEnforcement.Notes)
	}
	if !strings.Contains(raw, "Tailscale Funnel does not carry raw TCP") {
		t.Fatalf("JSON missing raw TCP Funnel caveat: %s", raw)
	}

	raw, _, err = runAccessExplainWithRawServices(t, "db", false, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
		Funnel: true,
	})
	if err != nil {
		t.Fatalf("runAccessExplain human: %v", err)
	}
	if !strings.Contains(raw, "Tailscale Funnel does not carry raw TCP") {
		t.Fatalf("human output missing raw TCP Funnel caveat:\n%s", raw)
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
		{Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true},
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

func TestAccessAuthorityHasExplicitPort(t *testing.T) {
	cases := []struct {
		authority string
		want      bool
	}{
		{"localhost:3000", true},
		{"localhost", false},
		{"[::1]:443", true},
		{"[::1]", false},
		{"2001:db8::1", false},
		{"user:pass@localhost:5432", false},
	}

	for _, tc := range cases {
		if got := accessAuthorityHasExplicitPort(tc.authority); got != tc.want {
			t.Fatalf("accessAuthorityHasExplicitPort(%q) = %v, want %v", tc.authority, got, tc.want)
		}
	}
}

func TestAccessClassifyHostPortTargetBranches(t *testing.T) {
	cases := []struct {
		name        string
		authority   string
		defaultPort string
		wantHost    string
		wantPort    string
		wantLoop    bool
		wantErr     string
	}{
		{
			name:        "default port local host",
			authority:   "localhost",
			defaultPort: "80",
			wantHost:    "localhost",
			wantPort:    "80",
			wantLoop:    true,
		},
		{
			name:        "bracketed ipv6 default port",
			authority:   "[::1]",
			defaultPort: "443",
			wantHost:    "::1",
			wantPort:    "443",
			wantLoop:    true,
		},
		{
			name:      "missing required port",
			authority: "localhost",
			wantErr:   "missing port in address",
		},
		{
			name:        "explicit malformed port is not defaulted",
			authority:   "localhost:http",
			defaultPort: "80",
			wantErr:     `invalid target port "http"`,
		},
		{
			name:      "empty explicit port",
			authority: "localhost:",
			wantErr:   "missing target port",
		},
		{
			name:      "port out of range",
			authority: "localhost:70000",
			wantErr:   `invalid target port "70000"`,
		},
		{
			name:      "external target",
			authority: "10.0.0.5:8080",
			wantHost:  "10.0.0.5",
			wantPort:  "8080",
			wantLoop:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := accessClassifyHostPortTarget(tc.authority, tc.defaultPort)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("accessClassifyHostPortTarget() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("accessClassifyHostPortTarget() error = %v", err)
			}
			if got.Host != tc.wantHost || got.Port != tc.wantPort || got.LoopbackOrLocal != tc.wantLoop {
				t.Fatalf("access target = %+v, want host %q port %q loopback %v", got, tc.wantHost, tc.wantPort, tc.wantLoop)
			}
		})
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

func hasAccessWarningCode(warnings []inspect.WarningView, want string) bool {
	for _, warning := range warnings {
		if warning.Code == want {
			return true
		}
	}
	return false
}
