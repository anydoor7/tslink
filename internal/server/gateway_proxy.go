package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// UpstreamDialer is shared by local gateway transports and the future tsnet
// adapter. Only the registry target is handed to the transport; request data
// never selects the destination. TLS remains verified by http.Transport.
type UpstreamDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type GatewayProxyOptions struct {
	Remote    bool
	Authority func() string
	App       string
	HostLabel string
	Observe   func(bool)
}

type gatewayProxy struct {
	proxy     *httputil.ReverseProxy
	transport *http.Transport
}

func (p *gatewayProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.proxy.ServeHTTP(w, r) }
func (p *gatewayProxy) Close() error                                     { p.transport.CloseIdleConnections(); return nil }

// Reserved for gateway credentials. App cookies otherwise retain their names;
// ports separate browser origins but do not provide cookie isolation.
const gatewayCookiePrefix = "__tslink_gateway_"

func stripGatewayCookies(h http.Header) {
	values := h.Values("Cookie")
	h.Del("Cookie")
	for _, value := range values {
		var keep []string
		for _, part := range strings.Split(value, ";") {
			name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
			if !strings.HasPrefix(strings.ToLower(name), gatewayCookiePrefix) {
				keep = append(keep, strings.TrimSpace(part))
			}
		}
		if len(keep) > 0 {
			h.Add("Cookie", strings.Join(keep, "; "))
		}
	}
}

func urlPort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	if u.Scheme == "http" {
		return "80"
	}
	return ""
}

func rewriteGatewayURL(value string, target *url.URL, authority string) string {
	u, err := url.Parse(value)
	if err != nil {
		return value
	}
	if u.Scheme == "" && u.Host != "" {
		u.Scheme = target.Scheme
	}
	if !u.IsAbs() || u.User != nil || !strings.EqualFold(u.Scheme, target.Scheme) || !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), strings.TrimSuffix(target.Hostname(), ".")) || urlPort(u) != urlPort(target) {
		return value
	}
	u.Scheme, u.Host = "https", authority
	return u.String()
}

func rewriteGatewayResponse(r *http.Response, target *url.URL, authority string) {
	for _, key := range []string{"Location", "Content-Location"} {
		values := r.Header.Values(key)
		r.Header.Del(key)
		for _, v := range values {
			r.Header.Add(key, rewriteGatewayURL(v, target, authority))
		}
	}
	values := r.Header.Values("Refresh")
	r.Header.Del("Refresh")
	for _, value := range values {
		value = rewriteGatewayRefresh(value, target, authority)
		r.Header.Add("Refresh", value)
	}
	values = r.Header.Values("Set-Cookie")
	r.Header.Del("Set-Cookie")
	for _, v := range values {
		name, _, _ := strings.Cut(strings.TrimSpace(v), "=")
		if !strings.HasPrefix(strings.ToLower(name), gatewayCookiePrefix) {
			r.Header.Add("Set-Cookie", v)
		}
	}
}

// Follows the HTML shared declarative refresh parsing steps (2026-10-13):
// https://html.spec.whatwg.org/multipage/semantics.html#shared-declarative-refresh-steps
// Keep the original spelling and suffix, replacing only the parsed URL.
func rewriteGatewayRefresh(value string, target *url.URL, authority string) string {
	space := func(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
	i := 0
	skip := func() {
		for i < len(value) && space(value[i]) {
			i++
		}
	}
	skip()
	start := i
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == start && (i == len(value) || value[i] != '.') {
		return value
	}
	for i < len(value) && ((value[i] >= '0' && value[i] <= '9') || value[i] == '.') {
		i++
	}
	if i == len(value) {
		return value
	}
	if value[i] != ';' && value[i] != ',' && !space(value[i]) {
		return value
	}
	skip()
	if i < len(value) && (value[i] == ';' || value[i] == ',') {
		i++
	}
	skip()
	start = i
	if i+3 <= len(value) && strings.EqualFold(value[i:i+3], "url") {
		i += 3
		skip()
		if i < len(value) && value[i] == '=' {
			i++
			skip()
			start = i
		} else {
			i = start
		}
	}
	end := len(value)
	if i < len(value) && (value[i] == '\'' || value[i] == '"') {
		quote := value[i]
		i++
		start = i
		if j := strings.IndexByte(value[i:], quote); j >= 0 {
			end = i + j
		}
	}
	dest := value[start:end]
	return value[:start] + rewriteGatewayURL(dest, target, authority) + value[end:]
}
