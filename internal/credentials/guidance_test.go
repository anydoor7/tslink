package credentials

import (
	"strings"
	"testing"
)

func TestBootstrapGuidanceIsStableAndValueFree(t *testing.T) {
	apiSteps := NextAPIKeyBootstrap()
	if len(apiSteps) != 3 {
		t.Fatalf("NextAPIKeyBootstrap() = %d steps, want 3", len(apiSteps))
	}
	joined := strings.Join(apiSteps, "\n")
	for _, want := range []string{KeysPageURL, "Generate access token", "tslink login --api-key-stdin", "--expires-in 90d", "tslink login --open-keys-page", "pbpaste"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("NextAPIKeyBootstrap() missing %q:\n%s", want, joined)
		}
	}
	oauthSteps := NextOAuthBootstrap()
	if len(oauthSteps) != 3 {
		t.Fatalf("NextOAuthBootstrap() = %d steps, want 3", len(oauthSteps))
	}
	joinedOAuth := strings.Join(oauthSteps, "\n")
	for _, want := range []string{OAuthPageURL, "OAuth client", "tslink login --client-secret-stdin", "tslink login --open-oauth-page"} {
		if !strings.Contains(joinedOAuth, want) {
			t.Fatalf("NextOAuthBootstrap() missing %q:\n%s", want, joinedOAuth)
		}
	}
	forbidden := strings.Join(NextAPIForbidden(), "\n")
	if !strings.Contains(forbidden, "role") || !strings.Contains(forbidden, "tslink doctor --probe-remote") || strings.Contains(forbidden, KeysPageURL) {
		t.Fatalf("NextAPIForbidden() = %q, want role/scope guidance without a new-token instruction", forbidden)
	}
	// Callers may append to the returned slice; the next call must be pristine.
	apiSteps[0] = "mutated"
	if NextAPIKeyBootstrap()[0] == "mutated" {
		t.Fatal("NextAPIKeyBootstrap() shares its backing array between calls")
	}
	for _, step := range append(append(NextAPIKeyBootstrap(), NextOAuthBootstrap()...), NextAPIForbidden()...) {
		if strings.Contains(step, "tskey-api-") && !strings.Contains(step, "tskey-api-*") {
			t.Fatalf("guidance step looks like it carries a token: %q", step)
		}
	}
}
