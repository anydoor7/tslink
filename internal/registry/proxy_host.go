package registry

import (
	"net"
	"strings"
)

// CanonicalProxyHost selects and validates the receiving node's trusted name.
// The first certificate domain wins, even if invalid; only an empty certificate
// list permits the startup DNS fallback. Neither value may come from a request
// or a registry service name.
func CanonicalProxyHost(certDomains []string, runtimeHost string) string {
	host := runtimeHost
	if len(certDomains) > 0 {
		host = certDomains[0]
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if len(host) > 253 || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return ""
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return ""
			}
		}
	}
	return host
}
