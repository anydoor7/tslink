package runtime

import (
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestPortalFingerprintAndCanonicalURL(t *testing.T) {
	reg := &registry.Registry{Services: []registry.Service{{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:8000"}}}
	before, err := RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Portal = &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}
	after, err := RegistryFingerprint(reg, nil)
	if err != nil || before == after {
		t.Fatal("portal configuration not fingerprinted")
	}
	reg.Portal.Enabled = false
	disabled, err := RegistryFingerprint(reg, nil)
	if err != nil || disabled == after {
		t.Fatal("disable did not invalidate portal runtime")
	}
	for _, tc := range []struct{ domain, want string }{
		{"Photos.Tailnet.TS.Net.", "https://photos.tailnet.ts.net"},
		{"invalid/host", ""},
	} {
		s := NewSnapshot(42, time.Time{}, "fixture", time.Time{}, []ServiceState{{Service: reg.Services[0], RuntimeHost: "fallback.tailnet.ts.net", CertDomains: []string{tc.domain}}})
		if s.Services[0].Endpoint.Display != tc.want {
			t.Fatalf("canonical URL=%q want=%q", s.Services[0].Endpoint.Display, tc.want)
		}
	}
}
