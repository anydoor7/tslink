package cmd

import (
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// accessRedactSchemelessTargetDisplay is the last thing that touches a
// schemeless proxy target before it is classified and rendered. A legacy or
// hand-edited registry.json can still hold "user:pass@host:port", so these
// tests are written adversarially: the fake credential must never reach output.
//
// Scope note: userinfo is recognized with RFC 3986 delimiters. A "/" "?" or "#"
// ends the authority, so a string like "u:pa/ss@host" is a path that merely
// contains "@", not credentials, and is deliberately left intact.
const fakeCredential = "FAKE-CREDENTIAL-DO-NOT-LEAK"

func TestAccessRedactSchemelessUserinfoDropsEverythingBeforeTheLastAt(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"user and password", "user:pass@localhost:5432", "localhost:5432"},
		{"password containing an at sign", "user:p@ss@localhost:5432", "localhost:5432"},
		{"percent encoded userinfo", "us%3Aer:p%40ss%2Fmore@localhost:1", "localhost:1"},
		{"empty userinfo", "@localhost:80", "localhost:80"},
		{"userinfo with empty authority remainder", "user@", ""},
		{"ipv6 literal with userinfo", "user:pass@[fe80::1%25en0]:443/x", "[fe80::1%25en0]:443/x"},
		{"ipv6 literal without userinfo", "[::1]:8080", "[::1]:8080"},
		{"at sign only in the path", "localhost:3000/p@notuserinfo", "localhost:3000/p@notuserinfo"},
		{"at sign only in a deeper path segment", "localhost:3000/a/b@c/d", "localhost:3000/a/b@c/d"},
		{"path delimiter ends the authority", "u:pa/ss@localhost:1", "u:pa/ss@localhost:1"},
		{"absolute path is not an authority", "/abs/path@x", "/abs/path@x"},
		{"empty input", "", ""},
		{"plain host and port", "localhost:3000", "localhost:3000"},
		{"multiple at signs in userinfo", "a@b@c@localhost:1", "localhost:1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessRedactSchemelessUserinfo(tc.raw); got != tc.want {
				t.Fatalf("accessRedactSchemelessUserinfo(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAccessRedactSchemelessTargetDisplayStripsUserinfoQueryAndFragment(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"schemeless userinfo", "user:pass@localhost:5432", "localhost:5432"},
		{"schemeless query", "localhost:3000?token=abc", "localhost:3000"},
		{"schemeless fragment", "localhost:3000#frag", "localhost:3000"},
		{"query before fragment", "localhost:3000?a=1#b", "localhost:3000"},
		{"fragment before query", "localhost:3000#b?a=1", "localhost:3000"},
		{"userinfo and query together", "user:pass@localhost:3000?token=abc", "localhost:3000"},
		{"scheme is preserved", "http://user:pass@localhost:3000/p?t=s#f", "http://localhost:3000/p"},
		{"https scheme is preserved", "https://user:pass@localhost:443", "https://localhost:443"},
		{"scheme with no userinfo", "http://localhost:3000/p", "http://localhost:3000/p"},
		{"question mark before a scheme separator", "weird?://user:pass@h", "weird"},
		{"fragment at position zero", "#user:pass@evil.example", ""},
		{"query at position zero", "?user:pass@evil.example", ""},
		{"empty input", "", ""},
		{"ipv6 literal", "[::1]:8080", "[::1]:8080"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessRedactSchemelessTargetDisplay(tc.raw); got != tc.want {
				t.Fatalf("accessRedactSchemelessTargetDisplay(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestAccessRedactSchemelessTargetDisplayCutsQueryAndFragmentBeforeReadingUserinfo(t *testing.T) {
	// An "@" inside a fragment or query must not be mistaken for a userinfo
	// delimiter. If the order were reversed the rendered host would become the
	// attacker-controlled tail instead of the real backend host.
	cases := []struct {
		raw  string
		want string
	}{
		{"localhost:3000#frag@evil.example", "localhost:3000"},
		{"localhost:3000?next=u:p@evil.example", "localhost:3000"},
		{"localhost:3000?a=1#b@evil.example", "localhost:3000"},
	}
	for _, tc := range cases {
		got := accessRedactSchemelessTargetDisplay(tc.raw)
		if got != tc.want {
			t.Fatalf("accessRedactSchemelessTargetDisplay(%q) = %q, want %q", tc.raw, got, tc.want)
		}
		if strings.Contains(got, "evil.example") {
			t.Fatalf("redaction of %q surfaced a query/fragment host: %q", tc.raw, got)
		}
	}
}

func TestAccessRedactSchemelessTargetDisplayNeverEmitsCredentialBearingInput(t *testing.T) {
	corpus := []string{
		fakeCredential + "@localhost:1",
		"user:" + fakeCredential + "@localhost:1",
		"user:" + fakeCredential + "@localhost:1/path",
		"user:" + fakeCredential + "@[::1]:1",
		"user:p@" + fakeCredential + "@localhost:1",
		"@" + fakeCredential + "@localhost:1",
		"user:" + fakeCredential + "@localhost:1?token=" + fakeCredential,
		"user:" + fakeCredential + "@localhost:1#" + fakeCredential,
		"localhost:1?token=" + fakeCredential,
		"localhost:1?a=1&token=" + fakeCredential + "&b=2",
		"localhost:1#" + fakeCredential,
		"http://user:" + fakeCredential + "@localhost:1/p?t=" + fakeCredential + "#" + fakeCredential,
		"https://" + fakeCredential + "@localhost:1",
		"user:" + fakeCredential + "@localhost:1?next=" + fakeCredential + "@evil.example",
	}

	for _, raw := range corpus {
		got := accessRedactSchemelessTargetDisplay(raw)
		if strings.Contains(got, fakeCredential) {
			t.Fatalf("accessRedactSchemelessTargetDisplay(%q) leaked the credential: %q", raw, got)
		}
		if strings.Contains(got, "@") {
			t.Fatalf("accessRedactSchemelessTargetDisplay(%q) kept a userinfo delimiter: %q", raw, got)
		}
	}
}

func TestAccessExplainRedactsLegacySchemelessCredentialTargetEndToEnd(t *testing.T) {
	cases := []struct {
		name     string
		target   string
		wantHost string
		wantPort string
	}{
		{"userinfo", "user:" + fakeCredential + "@localhost:5432", "localhost", "5432"},
		{"query secret", "localhost:3000?token=" + fakeCredential, "localhost", "3000"},
		{"fragment host confusion", "localhost:3000#frag@evil.example", "localhost", "3000"},
	}

	for _, tc := range cases {
		// registry.Add rejects schemeless targets, so this is the legacy or
		// hand-edited registry.json shape that still reaches the redactor.
		svc := registry.Service{Name: "web", Type: registry.TypeProxy, Target: tc.target}

		t.Run(tc.name+"/json", func(t *testing.T) {
			raw, result, err := runAccessExplainWithRawServices(t, "web", true, svc)
			if err != nil {
				t.Fatalf("runAccessExplain JSON error = %v", err)
			}
			if strings.Contains(raw, fakeCredential) {
				t.Fatalf("access explain JSON leaked the credential:\n%s", raw)
			}
			if strings.Contains(raw, "evil.example") {
				t.Fatalf("access explain JSON surfaced a fragment host:\n%s", raw)
			}
			classification := result.TSLinkKnown.TargetLoopbackClassification
			if classification.Host != tc.wantHost || classification.Port != tc.wantPort {
				t.Fatalf("target classification = %+v, want host %q port %q", classification, tc.wantHost, tc.wantPort)
			}
			if classification.Classification != "loopback_or_local" {
				t.Fatalf("classification = %q, want loopback_or_local for %q", classification.Classification, tc.target)
			}
		})

		t.Run(tc.name+"/human", func(t *testing.T) {
			raw, _, err := runAccessExplainWithRawServices(t, "web", false, svc)
			if err != nil {
				t.Fatalf("runAccessExplain human error = %v", err)
			}
			if strings.Contains(raw, fakeCredential) {
				t.Fatalf("access explain human output leaked the credential:\n%s", raw)
			}
			if strings.Contains(raw, "evil.example") {
				t.Fatalf("access explain human output surfaced a fragment host:\n%s", raw)
			}
		})
	}
}
