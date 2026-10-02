package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/local"
)

type LocalClient = local.Client

type ProxyOptions struct {
	PreserveHost bool
	// CanonicalHost reads the receiving node's trusted runtime name. It must
	// never derive a name from a request, TLS SNI, or registry service name.
	CanonicalHost func() string
}

// NewProxyHandler returns the reverse proxy for one proxy service. identity
// may be nil, in which case no X-Tailscale-* identity is injected; it is shared
// with the node's access log so one caller costs one WhoIs.
func NewProxyHandler(target string, identity *IdentityResolver) (http.Handler, error) {
	return NewProxyHandlerWithOptions(target, identity, ProxyOptions{})
}

// NewProxyHandlerWithOptions configures per-service HTTP forwarding. The
// default constructor retains target Host rewriting for existing callers.
func NewProxyHandlerWithOptions(target string, identity *IdentityResolver, options ProxyOptions) (http.Handler, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse target URL %q: %w", target, err)
	}

	// Custom transport tuned for proxying to a single local backend.
	transport := &http.Transport{
		MaxIdleConnsPerHost: 64,
		MaxIdleConns:        128,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(targetURL)
			r.SetXForwarded()
			if options.PreserveHost {
				host := r.In.Context().Value(canonicalProxyHostKey{}).(string)
				r.Out.Host = host
				r.Out.Header.Set("X-Forwarded-Host", host)
			}
			// Strip both identity namespaces, including CGI-equivalent spellings.
			for key := range r.In.Header {
				normalized := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
				if strings.HasPrefix(normalized, "x-tailscale-") || strings.HasPrefix(normalized, "tailscale-") {
					delete(r.Out.Header, key)
				}
			}
			// Inject caller identity
			if whois := identity.whoIs(r.In.Context(), r.In.RemoteAddr); whois != nil && whois.UserProfile != nil {
				r.Out.Header.Set("X-Tailscale-User-Login", whois.UserProfile.LoginName)
				r.Out.Header.Set("X-Tailscale-User-Name", whois.UserProfile.DisplayName)
				if whois.Node != nil {
					r.Out.Header.Set("X-Tailscale-Node", whois.Node.ComputedName)
				}
				if whois.UserProfile.ProfilePicURL != "" {
					r.Out.Header.Set("X-Tailscale-User-Picture", whois.UserProfile.ProfilePicURL)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if failure := requestFailure(r, err); failure != nil {
				http.Error(w, failure.message, failure.status)
				return
			}
			slog.Error("proxy error", "path", r.URL.Path, "error", err)
			if isTimeout(err) {
				http.Error(w, "Gateway timeout — backend did not respond in time", http.StatusGatewayTimeout)
			} else {
				http.Error(w, "Service unavailable — backend is offline", http.StatusBadGateway)
			}
		},
	}
	if !options.PreserveHost {
		return proxy, nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var host string
		if options.CanonicalHost != nil {
			host = normalizeCanonicalProxyHost(options.CanonicalHost())
		}
		if host == "" {
			http.Error(w, "canonical_host_unavailable: node's canonical external name is unavailable", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), canonicalProxyHostKey{}, host)))
	}), nil
}

type canonicalProxyHostKey struct{}

// A canonical authority is a DNS name without a port, normalized to lowercase
// with no terminal root dot. Invalid runtime metadata fails closed as missing.
func normalizeCanonicalProxyHost(host string) string {
	return registry.CanonicalProxyHost(nil, host)
}

func canonicalHostFor(node tsnetServer, runtimeHost string) string {
	var domains []string
	if node != nil {
		domains = node.CertDomains()
	}
	return registry.CanonicalProxyHost(domains, runtimeHost)
}

func isTimeout(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}
