package middleware

import (
	"log"
	"net"
	"net/http"
)

// IPAllowList returns a Middleware that restricts access to the given CIDR ranges.
// If cidrs is empty, all requests are allowed (no restriction).
// Returns 403 Forbidden if the remote IP is not in any allowed CIDR.
// Invalid CIDR or IP entries are logged and skipped (they do not cause a panic).
func IPAllowList(cidrs []string) Middleware {
	var networks []*net.IPNet
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			// Try parsing as a plain IP and convert to /32 or /128.
			ip := net.ParseIP(cidr)
			if ip == nil {
				log.Printf("middleware: skipping invalid CIDR or IP %q: %v", cidr, err)
				continue
			}
			if ip.To4() != nil {
				_, ipNet, _ = net.ParseCIDR(ip.String() + "/32")
			} else {
				_, ipNet, _ = net.ParseCIDR(ip.String() + "/128")
			}
		}
		networks = append(networks, ipNet)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Empty allow list means allow all.
			if len(networks) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			ipStr, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ipStr = r.RemoteAddr
			}

			ip := net.ParseIP(ipStr)
			if ip == nil {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			for _, n := range networks {
				if n.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, "Forbidden", http.StatusForbidden)
		})
	}
}
