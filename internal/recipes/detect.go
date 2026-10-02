package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const RequestTimeout = 700 * time.Millisecond
const DetectionTimeout = 15 * time.Second
const maxBody = 64 << 10

type Listener struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}
type RegisteredService struct {
	Name   string
	Target string
}
type Match struct {
	RecipeID    string   `json:"recipe_id"`
	DisplayName string   `json:"display_name"`
	Target      string   `json:"target"`
	Confidence  string   `json:"confidence"`
	Evidence    []string `json:"evidence"`
	Registered  []string `json:"registered"`
}
type Detection struct {
	SchemaVersion int        `json:"schema_version"`
	Listeners     []Listener `json:"listeners"`
	Matches       []Match    `json:"matches"`
	Complete      bool       `json:"complete"`
	Warnings      []string   `json:"warnings"`
}

func isLoopbackHost(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }

// loopbackDial is a second boundary in addition to listener filtering. No DNS,
// environment proxy, redirect, cookies or credentials enter the probe client.
func loopbackDial(ctx context.Context, network, address string) (net.Conn, error) {
	return dialLoopback(ctx, network, address, (&net.Dialer{Timeout: RequestTimeout}).DialContext)
}

func dialLoopback(ctx context.Context, network, address string, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !isLoopbackHost(host) {
		return nil, fmt.Errorf("probe refused non-loopback address")
	}
	return dial(ctx, network, address)
}
func probeClient() *http.Client {
	return &http.Client{Timeout: RequestTimeout, Transport: &http.Transport{Proxy: nil, DialContext: loopbackDial, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Detect inventories OS listeners then fingerprints only loopback HTTP. An
// incomplete scan is explicit; lack of a match is never evidence of app health.
var listenTCPFn = listeningTCP

func Detect(ctx context.Context, registered []RegisteredService) (Detection, error) {
	ctx, cancel := context.WithTimeout(ctx, DetectionTimeout)
	defer cancel()
	listeners, err := listenTCPFn(ctx)
	if err != nil {
		return Detection{}, err
	}
	return detectListeners(ctx, listeners, registered), nil
}
func detectListeners(ctx context.Context, listeners []Listener, registered []RegisteredService) Detection {
	result := Detection{SchemaVersion: 1, Listeners: []Listener{}, Matches: []Match{}, Warnings: []string{}, Complete: true}
	seen := map[Listener]bool{}
	for _, l := range listeners {
		if !isLoopbackHost(l.Host) || l.Port < 1 || l.Port > 65535 || seen[l] {
			continue
		}
		seen[l] = true
		result.Listeners = append(result.Listeners, l)
	}
	sort.Slice(result.Listeners, func(i, j int) bool {
		a, b := result.Listeners[i], result.Listeners[j]
		if a.Port == b.Port {
			return a.Host < b.Host
		}
		return a.Port < b.Port
	})
	client := probeClient()
	catalog := List()
	jobs := make(chan Listener)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for l := range jobs {
				matches := probeListener(ctx, client, l, catalog, registered)
				mu.Lock()
				result.Matches = append(result.Matches, matches...)
				mu.Unlock()
			}
		}()
	}
	for _, l := range result.Listeners {
		if ctx.Err() != nil {
			break
		}
		jobs <- l
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		result.Complete = false
		result.Warnings = append(result.Warnings, "Detection deadline or cancellation reached; matches are partial.")
	}
	sort.Slice(result.Matches, func(i, j int) bool {
		a, b := result.Matches[i], result.Matches[j]
		if a.Target == b.Target {
			return a.RecipeID < b.RecipeID
		}
		return a.Target < b.Target
	})
	return result
}

type response struct {
	body   string
	header http.Header
	status int
}

var titlePattern = regexp.MustCompile(`(?is)<title\b[^>]*>\s*(.*?)\s*</title>`)

func fingerprintMatches(f Fingerprint, r response) bool {
	if r.status < 200 || r.status >= 300 {
		return false
	}
	switch f.Kind {
	case "title":
		m := titlePattern.FindStringSubmatch(r.body)
		return len(m) > 1 && strings.TrimSpace(m[1]) != "" && strings.Contains(strings.ToLower(m[1]), strings.ToLower(f.Value))
	case "body":
		return strings.Contains(strings.ToLower(r.body), strings.ToLower(f.Value))
	case "header":
		v := r.header.Get(f.Header)
		return v != "" && strings.Contains(strings.ToLower(v), strings.ToLower(f.Value))
	case "json":
		var v any
		if json.Unmarshal([]byte(r.body), &v) != nil {
			return false
		}
		for key, want := range f.JSONMatch {
			field := v
			for _, part := range strings.Split(key, ".") {
				m, ok := field.(map[string]any)
				if !ok {
					return false
				}
				field = m[part]
			}
			if field == nil || (want != "*" && fmt.Sprint(field) != want) {
				return false
			}
		}
		return true
	}
	return false
}
func probeListener(ctx context.Context, client *http.Client, l Listener, catalog Catalog, registered []RegisteredService) []Match {
	target := "http://" + net.JoinHostPort(l.Host, strconv.Itoa(l.Port))
	cache := map[string]response{}
	fetch := func(path string) response {
		if r, ok := cache[path]; ok {
			return r
		}
		r := response{}
		if ctx.Err() != nil || !safePath(path) {
			return r
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target+path, nil)
		if err == nil {
			req.Header.Set("User-Agent", "TSLink-app-discovery/1")
			resp, err := client.Do(req)
			if err == nil {
				b, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
				resp.Body.Close()
				if readErr == nil {
					r = response{string(b), resp.Header, resp.StatusCode}
				}
			}
		}
		cache[path] = r
		return r
	}
	// Avoid sending app endpoints to non-HTTP listeners after a failed root GET.
	root := fetch("/")
	if root.status == 0 {
		return nil
	}
	matches := []Match{}
	for _, recipe := range catalog.Recipes {
		if recipe.ID == "generic-web" {
			continue
		}
		m := Match{RecipeID: recipe.ID, DisplayName: recipe.DisplayName, Target: target, Confidence: "medium", Evidence: []string{}, Registered: registeredNames(l, registered)}
		for _, f := range recipe.Fingerprints {
			if fingerprintMatches(f, fetch(f.Path)) {
				m.Evidence = append(m.Evidence, f.Kind+" fingerprint at "+f.Path)
				if f.Confidence == "high" {
					m.Confidence = "high"
				}
			}
		}
		if len(m.Evidence) > 0 {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 && fingerprintMatches(Fingerprint{Kind: "title"}, root) {
		matches = append(matches, Match{RecipeID: "generic-web", DisplayName: "Generic web app", Target: target, Confidence: "low", Evidence: []string{"nonempty HTTP title; application identity unknown"}, Registered: registeredNames(l, registered)})
	}
	return matches
}
func registeredNames(l Listener, services []RegisteredService) []string {
	names := []string{}
	for _, s := range services {
		u, err := url.Parse(s.Target)
		if err != nil || u.Scheme != "http" {
			continue
		}
		port := u.Port()
		if port == "" {
			port = "80"
		}
		// localhost may resolve to either family; numeric loopbacks must match.
		if port == strconv.Itoa(l.Port) && (strings.EqualFold(u.Hostname(), "localhost") || u.Hostname() == l.Host) {
			names = append(names, s.Name)
		}
	}
	sort.Strings(names)
	return names
}

// loopbackListeners converts wildcard binds to numeric loopback targets and
// drops LAN-only binds. It never treats a hostname as trusted loopback.
func loopbackListeners(address string) []Listener {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil
	}
	switch host {
	case "*", "":
		return []Listener{{"127.0.0.1", port}, {"::1", port}}
	case "0.0.0.0":
		return []Listener{{"127.0.0.1", port}}
	case "::":
		return []Listener{{"::1", port}, {"127.0.0.1", port}}
	default:
		if isLoopbackHost(host) {
			return []Listener{{host, port}}
		}
	}
	return nil
}
