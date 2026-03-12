package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"tailscale.com/client/tailscale"
)

type LocalClient = tailscale.LocalClient

func NewProxyHandler(target string, localClient *LocalClient) (http.Handler, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("parse target URL %q: %w", target, err)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(targetURL)
			r.SetXForwarded()
			// Strip X-Tailscale-* headers from inbound to prevent spoofing
			for key := range r.In.Header {
				if strings.HasPrefix(strings.ToLower(key), "x-tailscale-") {
					r.Out.Header.Del(key)
				}
			}
			// Inject caller identity
			if localClient != nil {
				whois, err := localClient.WhoIs(r.In.Context(), r.In.RemoteAddr)
				if err == nil {
					r.Out.Header.Set("X-Tailscale-User-Login", whois.UserProfile.LoginName)
					r.Out.Header.Set("X-Tailscale-User-Name", whois.UserProfile.DisplayName)
					r.Out.Header.Set("X-Tailscale-Node", whois.Node.ComputedName)
				}
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error for %s: %v", r.URL.Path, err)
			if isTimeout(err) {
				http.Error(w, "Gateway timeout — backend did not respond in time", http.StatusGatewayTimeout)
			} else {
				http.Error(w, "Service unavailable — backend is offline", http.StatusBadGateway)
			}
		},
	}
	return proxy, nil
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
