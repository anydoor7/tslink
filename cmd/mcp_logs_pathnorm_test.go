package cmd

import (
	"strings"
	"testing"
)

// pathNormBearerToken is the capability this test smuggles through unnormalized
// paths. It exists only in this file; the "does not appear" assertions below
// would be tautologies if the product code contained it.
const pathNormBearerToken = "PATHNORMBEARERTOKEN"

// TestRedactTailscaleURLNormalizesPathBeforeMatching covers the fail-open half
// of the bearer-path rule: url.Parse does not collapse "//" or resolve "..",
// so a prefix rule applied to the raw path lets a capability URL through.
func TestRedactTailscaleURLNormalizesPathBeforeMatching(t *testing.T) {
	for _, raw := range []string{
		"https://login.tailscale.com/a/" + pathNormBearerToken,
		"https://login.tailscale.com//a/" + pathNormBearerToken,
		"https://login.tailscale.com///a/" + pathNormBearerToken,
		"https://login.tailscale.com/x/../a/" + pathNormBearerToken,
		"https://login.tailscale.com//uinv/" + pathNormBearerToken,
		"https://login.tailscale.com/x/../admin/invite/" + pathNormBearerToken,
	} {
		t.Run(raw, func(t *testing.T) {
			got := sanitizeLogLine("msg: visit " + raw + " to continue")
			if strings.Contains(got, pathNormBearerToken) {
				t.Fatalf("capability survived sanitization: %q", got)
			}
			if !strings.Contains(got, doctorRedactedURL) {
				t.Fatalf("expected %s, got %q", doctorRedactedURL, got)
			}
		})
	}
}

// TestRedactTailscaleURLStillPassesDocumentationLinks is the control group.
// Without it, a rule that redacted every login.tailscale.com URL would satisfy
// the test above just as well -- and that is exactly the over-redaction F7 was
// filed to remove.
func TestRedactTailscaleURLStillPassesDocumentationLinks(t *testing.T) {
	for _, raw := range []string{
		"https://login.tailscale.com/admin/settings/keys",
		"https://login.tailscale.com/admin/machines",
		"https://login.tailscale.com//admin/settings/keys",
		"https://login.tailscale.com/admin/invite",
	} {
		t.Run(raw, func(t *testing.T) {
			line := "msg: open " + raw + " to renew"
			got := sanitizeLogLine(line)
			if got != line {
				t.Fatalf("documentation link was redacted:\n got %q\nwant %q", got, line)
			}
		})
	}
}

// TestRedactTailscaleURLKeepsRedactingCredentialCarriers pins the other
// direction: normalization must not become a way to widen what gets through.
func TestRedactTailscaleURLKeepsRedactingCredentialCarriers(t *testing.T) {
	for _, raw := range []string{
		"https://login.tailscale.com/admin/settings?key=" + pathNormBearerToken,
		"https://user:" + pathNormBearerToken + "@login.tailscale.com/admin",
		"https://login.tailscale.com/admin/tskey-auth-" + pathNormBearerToken,
	} {
		t.Run(raw, func(t *testing.T) {
			got := sanitizeLogLine("msg: " + raw)
			if strings.Contains(got, pathNormBearerToken) {
				t.Fatalf("credential survived sanitization: %q", got)
			}
		})
	}
}
