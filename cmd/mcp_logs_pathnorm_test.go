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

// portBypassToken is the capability used by the port/slash bypass tests. It
// exists only in this file; grep the product code for it before trusting the
// "does not appear" assertions.
const portBypassToken = "PORTBYPASSPROBETOKEN"

// TestRedactTailscaleURLCoversPortAndSlashShapes covers the URL-shape regression: the
// outer pattern required "//host/", so ":443" and a single slash never matched
// and the whole line passed through with the capability intact.
func TestRedactTailscaleURLCoversPortAndSlashShapes(t *testing.T) {
	for _, raw := range []string{
		"https://login.tailscale.com:443/a/" + portBypassToken,
		"https://login.tailscale.com:8443/uinv/" + portBypassToken,
		"https:/login.tailscale.com/a/" + portBypassToken,
		"https:/login.tailscale.com:443/admin/invite/" + portBypassToken,
		"https://login.tailscale.com:443//a/" + portBypassToken,
	} {
		t.Run(raw, func(t *testing.T) {
			got := sanitizeLogLine("msg: " + raw)
			if strings.Contains(got, portBypassToken) {
				t.Fatalf("capability survived sanitization: %q", got)
			}
		})
	}
}

// TestRedactTailscaleURLWiderPatternStillPassesCleanLinks is the control group
// for the widened pattern. Matching more shapes is only safe if the predicate
// still says no to the ones that carry nothing -- otherwise the fix for F7
// (stop redacting documentation links) has been quietly undone.
func TestRedactTailscaleURLWiderPatternStillPassesCleanLinks(t *testing.T) {
	for _, line := range []string{
		"msg: open https://login.tailscale.com/admin/settings/keys to renew",
		"msg: open https://login.tailscale.com:443/admin/settings/keys to renew",
		"msg: see https://login.tailscale.com/admin/machines",
		"msg: bare host https://login.tailscale.com and text after",
	} {
		t.Run(line, func(t *testing.T) {
			if got := sanitizeLogLine(line); got != line {
				t.Fatalf("clean link was redacted:\n got %q\nwant %q", got, line)
			}
		})
	}
}

// TestRedactTailscaleURLRejectsLookalikeHosts pins the fail-closed half: the
// widened pattern must not become a way for a host that merely resembles the
// real one to be reasoned about as if it were the real one.
func TestRedactTailscaleURLRejectsLookalikeHosts(t *testing.T) {
	for _, raw := range []string{
		"https:/login.tailscale.com.evil.example/a/" + portBypassToken,
		"https://login.tailscale.com:notaport/a/" + portBypassToken,
	} {
		t.Run(raw, func(t *testing.T) {
			got := sanitizeLogLine("msg: " + raw)
			if strings.Contains(got, portBypassToken) {
				t.Fatalf("capability survived on lookalike host: %q", got)
			}
		})
	}
}

// TestSanitizeLogLineDoesNotLeakFragmentCarriedCapabilities is the fragment half
// of the URL-shape family that includes ":443" and the single-slash form: the
// predicate reasons about scheme, host, userinfo, query and path, and a URL
// fragment is none of those. It is asserted through sanitizeLogLine rather than
// redactTailscaleURL because the question is what leaves the pipeline, and the
// outer pattern decides how much of the line the predicate even sees.
func TestSanitizeLogLineDoesNotLeakFragmentCarriedCapabilities(t *testing.T) {
	const token = "kSEcReTtOkEn99"
	for _, line := range []string{
		"msg: https://login.tailscale.com#/a/" + token,
		"msg: https://login.tailscale.com/x#/a/" + token,
		"msg: https://login.tailscale.com#x/a/" + token,
		"msg: https:/login.tailscale.com#/a/" + token,
	} {
		t.Run(line, func(t *testing.T) {
			got := sanitizeLogLine(line)
			if strings.Contains(got, token) {
				t.Fatalf("sanitizeLogLine(%q) = %q, still carries the capability", line, got)
			}
			// Positive control. The check above is single-directional: a
			// sanitizer that returned "" would satisfy it while destroying the
			// log. Requiring the redaction marker pins that the line was
			// rewritten by this rule rather than emptied by a broken one.
			if !strings.Contains(got, doctorRedactedURL) {
				t.Fatalf("sanitizeLogLine(%q) = %q, want it to carry %q", line, got, doctorRedactedURL)
			}
		})
	}
}

// TestRedactTailscaleURLRedactsAnchoredDocumentationLinks pins the cost of the
// fragment guard rather than leaving it undocumented: a documentation link with
// an anchor is now redacted whole. That is the intended fail-closed trade -- the
// alternative is parsing the fragment to decide whether it looks like a
// capability, which is exactly the reasoning this rule refuses to do.
//
// It is pinned because the behaviour looks like a bug to anyone who meets it in
// a log. Without a test, "let anchored links through" is a one-line change that
// silently reopens the leak TestSanitizeLogLineDoesNotLeakFragmentCarriedCapabilities
// closes.
func TestRedactTailscaleURLRedactsAnchoredDocumentationLinks(t *testing.T) {
	const line = "msg: open https://login.tailscale.com/admin/settings/keys#api to renew"
	got := sanitizeLogLine(line)
	if !strings.Contains(got, doctorRedactedURL) {
		t.Fatalf("anchored documentation link was not redacted:\n got %q", got)
	}
	// Control: the same link without the anchor must still pass through intact,
	// so this is pinning the anchor as the cause rather than the whole path.
	const plain = "msg: open https://login.tailscale.com/admin/settings/keys to renew"
	if out := sanitizeLogLine(plain); out != plain {
		t.Fatalf("unanchored documentation link was redacted:\n got %q\nwant %q", out, plain)
	}
}
