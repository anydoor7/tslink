package inspect

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// TestServiceViewHasNoReservedFeatureSurface replaces the view tests of the
// removed custom-domain and middleware features: the view has no middleware
// summary and emits none of their warning codes, and the codes are no longer
// registered. The Go fields are gone too, so no caller can set them.
func TestServiceViewHasNoReservedFeatureSurface(t *testing.T) {
	view := ServiceViewFor(registry.Service{
		Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000",
	})
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"middleware", "user:pass", "app.example.com", "custom_domain", "http_auth"} {
		if strings.Contains(string(encoded), removed) {
			t.Fatalf("service view %s contains %q", encoded, removed)
		}
	}
	if !strings.Contains(string(encoded), `"schema_version"`) {
		t.Fatalf("view probe is blind: %s", encoded)
	}
	for _, code := range []string{"middleware_not_enforced", "custom_domain_not_wired", "http_auth_configured"} {
		if _, ok := WarningCodeRegistry[code]; ok {
			t.Fatalf("removed warning code %s is still registered", code)
		}
	}
}
