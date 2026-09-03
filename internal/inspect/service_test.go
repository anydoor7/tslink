package inspect

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

func TestServiceViewForProxyFileAndTCP(t *testing.T) {
	proxy := ServiceViewFor(registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		Tags:         []string{"tag:web"},
		AllowedUsers: []string{"alice@example.com"},
	})
	if proxy.SchemaVersion != SchemaVersion {
		t.Fatalf("proxy schema_version = %q, want %q", proxy.SchemaVersion, SchemaVersion)
	}
	if proxy.Endpoint.Kind != EndpointKindHTTPS || proxy.Endpoint.Display != "" || proxy.Endpoint.Host != "" || proxy.Endpoint.State != EndpointStateExpected {
		t.Fatalf("proxy endpoint = %+v, want pending https endpoint without placeholder", proxy.Endpoint)
	}
	if proxy.Exposure.Kind != ExposureTailnetAllow || proxy.Exposure.Public {
		t.Fatalf("proxy exposure = %+v, want tailnet_allow private exposure", proxy.Exposure)
	}
	if proxy.Tags.Count != 1 || proxy.Tags.Entries[0] != "tag:web" {
		t.Fatalf("proxy tags = %+v, want tag summary", proxy.Tags)
	}
	if proxy.Allow.Mode != "restricted" || proxy.Allow.Count != 1 || !proxy.Allow.Redacted || len(proxy.Allow.Entries) != 0 {
		t.Fatalf("proxy allow = %+v, want redacted restricted allow summary", proxy.Allow)
	}
	if proxy.Backend.Kind != "http_target" || proxy.Backend.Display != "http://localhost:3000" {
		t.Fatalf("proxy backend = %+v, want http target", proxy.Backend)
	}

	file := ServiceViewFor(registry.Service{
		Name: "docs",
		Type: registry.TypeFile,
		Path: "/srv/docs",
	})
	if file.Endpoint.Kind != EndpointKindHTTPS || file.Endpoint.Display != "" || file.Endpoint.Host != "" || file.Endpoint.State != EndpointStateExpected {
		t.Fatalf("file endpoint = %+v, want pending https endpoint without placeholder", file.Endpoint)
	}
	if file.Exposure.Kind != ExposureTailnet {
		t.Fatalf("file exposure = %+v, want tailnet", file.Exposure)
	}
	if file.Backend.Kind != "directory" || file.Backend.Display != "/srv/docs" {
		t.Fatalf("file backend = %+v, want directory", file.Backend)
	}

	tcp := ServiceViewFor(registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	})
	if tcp.Endpoint.Kind != EndpointKindTCP || tcp.Endpoint.Display != "db.<tailnet>.ts.net:5432" || tcp.Endpoint.Host != "db.<tailnet>.ts.net" || tcp.Endpoint.Port != 5432 {
		t.Fatalf("tcp endpoint = %+v, want internal expected TCP placeholder", tcp.Endpoint)
	}
	if strings.HasPrefix(tcp.Endpoint.Display, "https://") {
		t.Fatalf("tcp endpoint must not be rendered as HTTPS: %s", tcp.Endpoint.Display)
	}
	if tcp.Exposure.Kind != ExposureTailnet {
		t.Fatalf("tcp exposure = %+v, want tailnet", tcp.Exposure)
	}
	if tcp.Allow.Mode != "not_applicable" {
		t.Fatalf("tcp allow = %+v, want not_applicable", tcp.Allow)
	}
	if tcp.Backend.Kind != "tcp_target" || tcp.Backend.Display != "localhost:5432" {
		t.Fatalf("tcp backend = %+v, want tcp target", tcp.Backend)
	}
	if len(tcp.Warnings) != 1 || tcp.Warnings[0].Code != WarningCodeTCPHTTPACLNotApplicable {
		t.Fatalf("tcp warnings = %+v, want tcp boundary warning", tcp.Warnings)
	}
}

func TestServiceViewsPreservesOrderAndBuildsViews(t *testing.T) {
	services := []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"},
		{Name: "docs", Type: registry.TypeFile, Path: "/srv/docs"},
	}

	views := ServiceViews(services)
	if len(views) != 2 {
		t.Fatalf("ServiceViews() returned %d views, want 2", len(views))
	}
	if views[0].Name != "web" || views[0].Endpoint.Display != "" || views[0].Endpoint.State != EndpointStateExpected {
		t.Fatalf("first view = %+v, want web service view", views[0])
	}
	if views[1].Name != "docs" || views[1].Backend.Display != "/srv/docs" {
		t.Fatalf("second view = %+v, want docs service view", views[1])
	}
	if empty := ServiceViews(nil); len(empty) != 0 {
		t.Fatalf("ServiceViews(nil) = %d views, want 0", len(empty))
	}
}

func TestServiceViewForCustomDomainDoesNotPresentDomainEndpoint(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Domain: "app.example.com",
	})
	if view.Endpoint.Display == "https://app.example.com" || view.Endpoint.Host == "app.example.com" {
		t.Fatalf("endpoint = %+v, must not present custom domain as reachable", view.Endpoint)
	}
	if view.Endpoint.Display != "" || view.Endpoint.Host != "" {
		t.Fatalf("endpoint = %+v, want pending endpoint without custom-domain or placeholder host", view.Endpoint)
	}
	if view.Exposure.Kind == ExposureCustomDomain {
		t.Fatalf("exposure = %+v, must not present custom domain exposure", view.Exposure)
	}
	if len(view.Warnings) != 1 || view.Warnings[0].Code != WarningCodeCustomDomainNotWired {
		t.Fatalf("warnings = %+v, want custom-domain unavailable warning", view.Warnings)
	}
}

func TestServiceViewRedactsHTTPAuthCredentials(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	})
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	raw := string(data)
	if strings.Contains(raw, "user:pass") {
		t.Fatalf("public service view leaked credential: %s", raw)
	}
	if strings.Contains(raw, "basic_auth") {
		t.Fatalf("public service view leaked private field name: %s", raw)
	}
	if view.Middleware == nil || !view.Middleware.HTTPAuth {
		t.Fatalf("middleware summary = %+v, want redacted auth presence", view.Middleware)
	}
	if len(view.Warnings) != 1 || view.Warnings[0].Code != WarningCodeMiddlewareNotEnforced {
		t.Fatalf("warnings = %+v, want middleware_not_enforced", view.Warnings)
	}
	if !strings.Contains(view.Warnings[0].Message, "NOT enforced") {
		t.Fatalf("warning message = %q, want explicit unenforced wording", view.Warnings[0].Message)
	}
}

func TestServiceViewRedactsBackendURLSecrets(t *testing.T) {
	cases := []struct {
		name        string
		svc         registry.Service
		wantBackend string
		forbidden   []string
	}{
		{
			name: "proxy URL userinfo query fragment",
			svc: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://user:pass@localhost:3000/private?token=abc#frag-secret",
			},
			wantBackend: "http://localhost:3000/private",
			forbidden:   []string{"user:pass", "token=abc", "frag-secret"},
		},
		{
			name: "tcp schemeless userinfo query fragment",
			svc: registry.Service{
				Name:   "db",
				Type:   registry.TypeTCP,
				Target: "user:pass@localhost:5432?token=abc#frag-secret",
				Port:   5432,
			},
			wantBackend: "localhost:5432",
			forbidden:   []string{"user:pass", "token=abc", "frag-secret"},
		},
		{
			name: "proxy URL keeps diagnostic path",
			svc: registry.Service{
				Name:   "api",
				Type:   registry.TypeProxy,
				Target: "https://user:pass@example.com:8443/app/v1?token=abc#frag-secret",
			},
			wantBackend: "https://example.com:8443/app/v1",
			forbidden:   []string{"user:pass", "token=abc", "frag-secret"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := ServiceViewFor(tc.svc)
			if view.Backend.Display != tc.wantBackend {
				t.Fatalf("backend display = %q, want %q", view.Backend.Display, tc.wantBackend)
			}
			data, err := json.Marshal(view)
			if err != nil {
				t.Fatalf("marshal view: %v", err)
			}
			raw := string(data)
			for _, forbidden := range tc.forbidden {
				if strings.Contains(raw, forbidden) {
					t.Fatalf("public service view leaked %q: %s", forbidden, raw)
				}
			}
		})
	}
}

func TestServiceViewRedactsAllowPrincipals(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	})

	if view.Allow.Mode != "restricted" || view.Allow.Count != 2 || !view.Allow.Redacted {
		t.Fatalf("allow summary = %+v, want redacted restricted count", view.Allow)
	}
	if len(view.Allow.Entries) != 0 {
		t.Fatalf("allow entries = %v, want no public principals", view.Allow.Entries)
	}

	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	raw := string(data)
	for _, principal := range []string{"alice@example.com", "tag:admin"} {
		if strings.Contains(raw, principal) {
			t.Fatalf("public service view leaked allow principal %q: %s", principal, raw)
		}
	}
}

func TestServiceViewWarnsForTCPAllowedUsersInvalid(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	})

	if view.Exposure.Kind != ExposureTailnet {
		t.Fatalf("tcp exposure = %+v, want tailnet despite invalid allow list", view.Exposure)
	}
	if view.Allow.Mode != "restricted" || view.Allow.Count != 2 || !view.Allow.Redacted || len(view.Allow.Entries) != 0 {
		t.Fatalf("tcp allow = %+v, want redacted invalid allow summary", view.Allow)
	}

	var found bool
	for _, warning := range view.Warnings {
		if warning.Code == WarningCodeTCPAllowedUsersInvalid {
			found = true
			if warning.Severity != "error" {
				t.Fatalf("tcp allow warning severity = %q, want error", warning.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("warnings = %+v, want %s", view.Warnings, WarningCodeTCPAllowedUsersInvalid)
	}

	data, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	raw := string(data)
	for _, principal := range []string{"alice@example.com", "tag:admin"} {
		if strings.Contains(raw, principal) {
			t.Fatalf("tcp service view leaked allow principal %q: %s", principal, raw)
		}
	}
}

func TestServiceViewWarningCodesAreRegistered(t *testing.T) {
	required := []string{
		WarningCodeTCPHTTPACLNotApplicable,
		WarningCodeTCPAllowedUsersInvalid,
		WarningCodeMiddlewareNotEnforced,
		WarningCodeServiceTypeUnknown,
		WarningCodeCustomDomainNotWired,
	}
	for _, code := range required {
		meta, ok := WarningCodeRegistry[code]
		if !ok {
			t.Fatalf("warning code %s is not registered", code)
		}
		if meta.Severity == "" || meta.Source == "" || meta.Description == "" {
			t.Fatalf("warning code %s has incomplete metadata: %+v", code, meta)
		}
	}

	cases := []registry.Service{
		{
			Name:         "db",
			Type:         registry.TypeTCP,
			Target:       "localhost:5432",
			AllowedUsers: []string{"alice@example.com"},
		},
		{
			Name:   "auth",
			Type:   registry.TypeProxy,
			Target: "http://localhost:3000",
			Middleware: &registry.MiddlewareConfig{
				BasicAuth: "user:pass",
			},
		},
		{
			Name: "mystery",
			Type: "udp",
		},
		{
			Name:   "domain",
			Type:   registry.TypeProxy,
			Target: "http://localhost:3000",
			Domain: "app.example.com",
		},
	}

	emitted := map[string]bool{}
	for _, svc := range cases {
		for _, warning := range ServiceViewFor(svc).Warnings {
			emitted[warning.Code] = true
			meta, ok := WarningCodeRegistry[warning.Code]
			if !ok {
				t.Fatalf("emitted warning code %s is not registered", warning.Code)
			}
			if warning.Severity != meta.Severity || warning.Source != meta.Source {
				t.Fatalf("warning %s = %+v, want registry severity/source %+v", warning.Code, warning, meta)
			}
		}
	}
	for _, code := range required {
		if !emitted[code] {
			t.Fatalf("test cases did not emit registered warning code %s", code)
		}
	}
}

func TestFunnelConflictCodesAreRegistered(t *testing.T) {
	required := []string{
		WarningCodeFunnelAllowConflict,
		WarningCodeFunnelPublicAckRequired,
		WarningCodeFunnelControlURLConflict,
		WarningCodeFunnelTypeConflict,
	}
	for _, code := range required {
		meta, ok := WarningCodeRegistry[code]
		if !ok {
			t.Fatalf("funnel conflict code %s is not registered", code)
		}
		if meta.Severity != "error" || meta.Source == "" || meta.Description == "" {
			t.Fatalf("funnel conflict code %s has incomplete metadata: %+v", code, meta)
		}
	}
}

func TestRuntimeSnapshotCodesAreRegistered(t *testing.T) {
	required := []string{
		WarningCodeRuntimeSnapshotMissing,
		WarningCodeRuntimeSnapshotStale,
		WarningCodeRuntimeSnapshotUnreadable,
	}
	for _, code := range required {
		meta, ok := WarningCodeRegistry[code]
		if !ok {
			t.Fatalf("runtime snapshot code %s is not registered", code)
		}
		if meta.Severity != "warning" || meta.Source != "runtime.snapshot" || meta.Description == "" {
			t.Fatalf("runtime snapshot code %s has incomplete metadata: %+v", code, meta)
		}
	}
}

func TestDoctorCodesAreRegistered(t *testing.T) {
	required := []string{
		WarningCodeConfigPathUnavailable,
		WarningCodeConfigLoadFailed,
		WarningCodeRegistryLoadFailed,
		WarningCodeRegistryServiceInvalid,
		WarningCodeControlURLInvalid,
		WarningCodeCredentialNone,
		WarningCodeCredentialLegacyAuthKey,
		WarningCodeCredentialNoAPIClient,
		WarningCodeCredentialReadFailed,
		WarningCodeDaemonNotRunning,
		WarningCodeDaemonPIDUnreadable,
		WarningCodeTargetProbeSkippedExternal,
		WarningCodeTargetProbeFailed,
		WarningCodeTargetProbeRefused,
		WarningCodeTargetProbeTimeout,
		WarningCodeTargetInvalid,
		WarningCodeProxyNonLoopbackTarget,
		WarningCodeTCPNonLoopbackTarget,
		WarningCodeFilePathMissing,
		WarningCodeFilePathUnreadable,
		WarningCodeFunnelGlobalControlURLUnknownCompat,
		WarningCodeIdentityResolutionUnknown,
		WarningCodeCredentialMixedRecommended,
		WarningCodeCredentialAPITokenOnly,
		WarningCodeCredentialOAuthClientOnly,
		WarningCodeCredentialAPITokenExpiring,
		WarningCodeCredentialAPITokenExpired,
		WarningCodeCredentialExpiryUnknown,
		WarningCodeCredentialRemoteUnverified,
		WarningCodeCredentialMetaBackfilled,
		WarningCodeCredentialAPITokenRejected,
		WarningCodeCredentialRemoteForbidden,
		WarningCodeCredentialRemoteUnreachable,
	}
	for _, code := range required {
		meta, ok := WarningCodeRegistry[code]
		if !ok {
			t.Fatalf("doctor code %s is not registered", code)
		}
		if meta.Severity == "" || meta.Source == "" || meta.Description == "" {
			t.Fatalf("doctor code %s has incomplete metadata: %+v", code, meta)
		}
	}
}
