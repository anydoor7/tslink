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
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

type LocalClient = local.Client

// whoisEntry stores a cached WhoIs result with expiration.
type whoisEntry struct {
	resp      *apitype.WhoIsResponse
	expiresAt time.Time
}

// whoisCache provides a TTL cache for WhoIs results keyed by IP address.
type whoisCache struct {
	mu      sync.Mutex
	entries map[string]whoisEntry
	ttl     time.Duration
}

func newWhoisCache(ttl time.Duration) *whoisCache {
	return &whoisCache{
		entries: make(map[string]whoisEntry),
		ttl:     ttl,
	}
}

// get returns a cached WhoIs response for the given IP, or nil if not cached/expired.
func (c *whoisCache) get(ip string) *apitype.WhoIsResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[ip]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(c.entries, ip)
		}
		return nil
	}
	return entry.resp
}

// set stores a WhoIs response for the given IP.
func (c *whoisCache) set(ip string, resp *apitype.WhoIsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ip] = whoisEntry{
		resp:      resp,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func NewProxyHandler(target string, localClient *LocalClient) (http.Handler, error) {
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

	// WhoIs identity cache with 60s TTL.
	cache := newWhoisCache(60 * time.Second)

	proxy := &httputil.ReverseProxy{
		Transport: transport,
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
				ip, _, _ := net.SplitHostPort(r.In.RemoteAddr)
				if ip == "" {
					ip = r.In.RemoteAddr
				}

				whois := cache.get(ip)
				if whois == nil {
					var err error
					whois, err = localClient.WhoIs(r.In.Context(), r.In.RemoteAddr)
					if err == nil && whois != nil {
						cache.set(ip, whois)
					}
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
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Error("proxy error", "path", r.URL.Path, "error", err)
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
