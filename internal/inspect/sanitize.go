package inspect

import (
	"net/url"
	"strings"
)

// SanitizeBackendDisplay preserves useful backend diagnostics while removing
// credential-bearing URL parts from public service views.
func SanitizeBackendDisplay(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	return sanitizeSchemelessTargetDisplay(raw)
}

func sanitizeSchemelessTargetDisplay(raw string) string {
	display := raw
	if cut := strings.IndexAny(display, "?#"); cut >= 0 {
		display = display[:cut]
	}

	prefix := ""
	rest := display
	if schemeIndex := strings.Index(rest, "://"); schemeIndex >= 0 {
		prefix = rest[:schemeIndex+len("://")]
		rest = rest[schemeIndex+len("://"):]
	}
	return prefix + sanitizeSchemelessUserinfo(rest)
}

func sanitizeSchemelessUserinfo(raw string) string {
	if raw == "" || strings.HasPrefix(raw, "/") {
		return raw
	}
	authorityEnd := strings.Index(raw, "/")
	if authorityEnd < 0 {
		authorityEnd = len(raw)
	}
	authority := raw[:authorityEnd]
	if userinfoEnd := strings.LastIndex(authority, "@"); userinfoEnd >= 0 {
		return raw[userinfoEnd+1:]
	}
	return raw
}
