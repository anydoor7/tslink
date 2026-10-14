package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

type LocalClient = local.Client

type ProxyOptions struct {
	Gateway      *GatewayProxyOptions
	Dialer       UpstreamDialer
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
	if options.Dialer != nil {
		transport.DialContext = options.Dialer.DialContext
	}
	if options.Gateway != nil {
		transport.TLSHandshakeTimeout = 10 * time.Second
	}

	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(targetURL)
			r.SetXForwarded()
			if options.Gateway != nil {
				r.Out.Header.Set("X-Forwarded-Host", options.Gateway.Authority())
				r.Out.Header.Set("X-Forwarded-Proto", "https")
				stripGatewayCookies(r.Out.Header)
				if options.Gateway.Remote {
					r.Out.Header.Del("X-Forwarded-For")
				}
			}
			if options.PreserveHost {
				host, _ := r.In.Context().Value(canonicalProxyHostKey{}).(string)
				if options.Gateway != nil {
					host = options.Gateway.Authority()
				}
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
			public, _ := r.In.Context().Value(accessFunnelKey{}).(bool)
			var whois *apitype.WhoIsResponse
			gated, _ := r.In.Context().Value(guestProxyKey{}).(bool)
			if options.Gateway != nil {
				if !options.Gateway.Remote {
					whois, _ = r.In.Context().Value(gatewayWhoKey{}).(*apitype.WhoIsResponse)
				}
			} else if !public || !gated {
				whois = identity.whoIs(r.In.Context(), r.In.RemoteAddr)
			}
			if whois != nil && whois.UserProfile != nil {
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
		ModifyResponse: func(response *http.Response) error {
			if options.Gateway != nil {
				if options.Gateway.Observe != nil {
					options.Gateway.Observe(true)
				}
				rewriteGatewayResponse(response, targetURL, options.Gateway.Authority())
			}
			gated, _ := response.Request.Context().Value(guestProxyKey{}).(bool)
			if !gated {
				return nil
			}
			if public, _ := response.Request.Context().Value(accessFunnelKey{}).(bool); public {
				response.Header.Set("Referrer-Policy", "no-referrer")
			}
			values := response.Header.Values("Set-Cookie")
			response.Header.Del("Set-Cookie")
			for _, value := range values {
				name, _, _ := strings.Cut(value, "=")
				name = strings.TrimSpace(name)
				if cookie, err := http.ParseSetCookie(value); err == nil {
					name = cookie.Name
				}
				if name != guestCookie && name != guestPINCookie {
					response.Header.Add("Set-Cookie", value)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if failure := requestFailure(r, err); failure != nil {
				accessDeny(r, "limits")
				http.Error(w, failure.message, failure.status)
				return
			}
			if denyGuestProxyCancellation(w, r, err) {
				return
			}
			// A client that went away is neither a backend failure nor a
			// denial: no 5xx, no route-down or health observation.
			if clientCanceled(r, err) {
				accessClientCanceled(w, r)
				return
			}
			class := backendErrorClass(err)
			accessBackendFailure(r, class)
			if options.Gateway != nil {
				accessDeny(r, "upstream_down")
				if options.Gateway.Observe != nil {
					options.Gateway.Observe(false)
				}
				gatewayErrorPage(w, r, http.StatusBadGateway, "upstream_down", options.Gateway.App+" on "+options.Gateway.HostLabel+" is not reachable right now", "", false)
				return
			}
			slog.Error("proxy error", "code", "backend_request_failed", "error_class", class)
			if isTimeout(err) {
				http.Error(w, "Gateway timeout — backend did not respond in time", http.StatusGatewayTimeout)
			} else {
				http.Error(w, "Service unavailable — backend is offline", http.StatusBadGateway)
			}
		},
	}
	if options.Gateway != nil {
		return &gatewayProxy{proxy: proxy, transport: transport}, nil
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
			accessDeny(r, "preserve_host_unavailable")
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

// backendErrorClass names a backend failure without its URL, host or address.
func backendErrorClass(err error) string {
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var verify *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case isTimeout(err):
		return "timeout"
	case errnoIs(err, syscall.ECONNREFUSED, 10061):
		return "connection_refused"
	case errnoIs(err, syscall.ECONNRESET, 10054), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection_reset"
	case errors.As(err, &record), errors.As(err, &alert), errors.As(err, &verify), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &invalid):
		return "tls"
	}
	return "other"
}

// errnoIs matches a socket error. Winsock reports refusals and resets with its
// own numbers (WSAECONNREFUSED 10061, WSAECONNRESET 10054), which differ from
// the syscall package's portable values on Windows.
func errnoIs(err error, portable syscall.Errno, winsock uintptr) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && (errno == portable || runtime.GOOS == "windows" && uintptr(errno) == winsock)
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
