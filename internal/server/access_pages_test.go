package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// denialEnvelope reads the visitor code and message out of the JSON error
// envelope of a generated response.
func denialEnvelope(t *testing.T, body []byte) (code, message string) {
	t.Helper()
	var envelope struct {
		Command string `json:"command"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("not a JSON error envelope: %v: %q", err, body)
	}
	// Neither a CLI command nor an entry path: the label was once "portal route".
	if envelope.Command != "app request" {
		t.Fatalf("envelope command %q, want the neutral \"app request\": %q", envelope.Command, body)
	}
	return envelope.Error.Code, envelope.Error.Message
}

type generatedResult struct {
	status   int
	header   http.Header
	body     string
	decision string
	reason   string
}

// serveGenerated sends one request through handler and returns what the
// visitor got and what the access observation recorded.
func serveGenerated(handler http.Handler, method string, header map[string]string) generatedResult {
	obs := &accessObservation{}
	r := httptest.NewRequest(method, "/path", strings.NewReader("payload"))
	r = r.WithContext(context.WithValue(r.Context(), accessObservationKey{}, obs))
	r.RemoteAddr = "100.64.0.9:4000"
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	obs.mu.Lock()
	defer obs.mu.Unlock()
	return generatedResult{status: w.Code, header: w.Header(), body: w.Body.String(), decision: obs.decision, reason: obs.reason}
}

// A refusal must read the same whether the request came through the gateway or
// reached the app's own node: the access log records the bytes actually
// written, so any difference in the generated response is a difference in
// bytes_out for that person and app. Both paths are built from the same
// registry and the same caller. The access-log reason may differ by path
// (people vs no_grant); the visitor-facing code and body may not.
func TestAccessDenialIsTheSameOnBothEntryPaths(t *testing.T) {
	const dashboard = "https://home.tailnet.ts.net"
	now := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	type shape struct {
		name, method string
		header       map[string]string
		html         bool
	}
	shapes := []shape{
		{name: "api_post", method: http.MethodPost},
		{name: "api_json", method: http.MethodGet, header: map[string]string{"Accept": "application/json"}},
		{name: "browser_accept", method: http.MethodGet, header: map[string]string{"Accept": "text/html,application/xhtml+xml"}, html: true},
		{name: "browser_navigate", method: http.MethodGet, header: map[string]string{"Sec-Fetch-Mode": "navigate"}, html: true},
		{name: "browser_head", method: http.MethodHead, header: map[string]string{"Sec-Fetch-Mode": "navigate"}, html: true},
	}
	makeRequestable := func(t *testing.T, path string) {
		if _, err := registry.MutateService(path, "photos", func(s registry.Service) (registry.Service, error) { s.Requestable = true; return s, nil }); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name       string
		login      string
		who        *apitype.WhoIsResponse // the caller; whoIsUser(login) when nil
		setup      func(t *testing.T, path string)
		whoErr     bool
		code       string
		message    string
		directLog  string // the access-log reason of the app's own node
		gatewayLog string // and of the gateway
		later      time.Duration
	}{
		{name: "no grant", login: "alice", code: "no_grant", message: "You do not have access to this app", directLog: "people", gatewayLog: "no_grant"},
		// The app is named to a caller who can ask for it, and to nobody else.
		{name: "no grant, requestable app, caller who can request", login: "alice", who: requestMember("alice"), code: "no_grant", message: "You do not have access to this app: photos", directLog: "people", gatewayLog: "no_grant",
			setup: makeRequestable},
		{name: "no grant, requestable app, node not described", login: "alice", code: "no_grant", message: "You do not have access to this app", directLog: "people", gatewayLog: "no_grant",
			setup: makeRequestable},
		{name: "no grant, requestable app, tagged caller", login: "alice", who: func() *apitype.WhoIsResponse {
			w := requestMember("alice")
			w.Node.Tags = []string{"tag:server"}
			return w
		}(), code: "no_grant", message: "You do not have access to this app", directLog: "people", gatewayLog: "no_grant", setup: makeRequestable},
		{name: "no grant, requestable app, shared-in caller", login: "alice", who: func() *apitype.WhoIsResponse {
			w := requestMember("alice")
			w.Node.Sharer = 7
			return w
		}(), code: "no_grant", message: "You do not have access to this app", directLog: "people", gatewayLog: "no_grant", setup: makeRequestable},
		{name: "no grant, requestable app, sharee node", login: "alice", who: func() *apitype.WhoIsResponse {
			w := requestMember("alice")
			w.Node.Hostinfo = (&tailcfg.Hostinfo{ShareeNode: true}).View()
			return w
		}(), code: "no_grant", message: "You do not have access to this app", directLog: "people", gatewayLog: "no_grant", setup: makeRequestable},
		{name: "expired grant", login: "alice", code: "expired", message: "Your access has expired", directLog: "expired", gatewayLog: "expired", later: time.Hour,
			setup: func(t *testing.T, path string) {
				deadline := now.Add(time.Minute)
				if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, &deadline, true, true); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "revoked person", login: "alice", code: "revoked", message: "Your access has been revoked", directLog: "people", gatewayLog: "revoked",
			setup: func(t *testing.T, path string) {
				if _, err := registry.RemovePerson(path, "alice"); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "caller not identified", login: "alice", whoErr: true, code: "whois_failed", message: "Unable to identify this connection", directLog: "people", gatewayLog: "whois_failed"},
		{name: "policy unavailable", login: "alice", code: "registry_unavailable", message: "Access policy is temporarily unavailable", directLog: "people", gatewayLog: "registry_unavailable",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			t.Setenv("TSLINK_DISABLE_KEYRING", "1")
			path := filepath.Join(dir, "registry.json")
			if err := registry.SetPortal(path, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"admin"}, Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"photos", "payroll"} {
				if _, err := registry.Add(path, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://127.0.0.1:9", PortalOnly: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.MutateService(path, name, func(s registry.Service) (registry.Service, error) { s.PortalOnly = false; return s, nil }); err != nil {
					t.Fatal(err)
				}
			}
			// alice may use payroll only, so the gateway admits her and she is
			// refused at photos.
			if _, err := registry.ChangePerson(path, "alice", []string{"payroll"}, nil, true, false); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, path)
			}
			var svc registry.Service
			if reg, err := registry.Load(path); err == nil {
				for _, s := range reg.Services {
					if s.Name == "photos" {
						svc = s
					}
				}
			} else if tc.code != "registry_unavailable" {
				t.Fatal(err)
			} else {
				svc = registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:9", PeopleScoped: true, PortalRoute: &registry.PortalRoute{Port: 8443}}
			}
			var lc *LocalClient
			if tc.whoErr {
				lc = fakeWhoIsClient(t, nil, os.ErrDeadlineExceeded)
			} else {
				who := tc.who
				if who == nil {
					who = whoIsUser(tc.login, "phone")
				}
				lc = fakeWhoIsClient(t, who, nil)
			}
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("a refused request reached the app") })
			clock := func() time.Time { return now.Add(tc.later) }
			gate := &gatewayGate{path: path, svc: svc, lc: lc, now: clock, authority: "home.tailnet.ts.net:8443", dashboard: dashboard, expire: registry.TryExpirePeople}
			viaGateway := gate.handler(next)
			viaNode := peopleMiddleware(path, svc, func() (*LocalClient, error) { return lc, nil }, clock)(next)
			for _, sh := range shapes {
				t.Run(sh.name, func(t *testing.T) {
					gw, node := serveGenerated(viaGateway, sh.method, sh.header), serveGenerated(viaNode, sh.method, sh.header)
					if gw.status != http.StatusForbidden || node.status != http.StatusForbidden {
						t.Fatalf("status gateway=%d node=%d, want 403\ngateway: %q\nnode:    %q", gw.status, node.status, gw.body, node.body)
					}
					if gw.body != node.body {
						t.Fatalf("bodies differ:\ngateway: %q\nnode:    %q", gw.body, node.body)
					}
					for _, name := range []string{"Content-Type", "Cache-Control", "Content-Security-Policy"} {
						if gw.header.Get(name) != node.header.Get(name) {
							t.Fatalf("%s differs: gateway=%q node=%q", name, gw.header.Get(name), node.header.Get(name))
						}
					}
					if gw.decision != "denied" || node.decision != "denied" || gw.reason != tc.gatewayLog || node.reason != tc.directLog {
						t.Fatalf("access log: gateway=%s/%s node=%s/%s, want denied %s / denied %s", gw.decision, gw.reason, node.decision, node.reason, tc.gatewayLog, tc.directLog)
					}
					isHTML := strings.HasPrefix(gw.header.Get("Content-Type"), "text/html")
					if isHTML != sh.html {
						t.Fatalf("content negotiation: html=%v for %s", isHTML, sh.name)
					}
					if sh.method == http.MethodHead {
						if gw.body != "" {
							t.Fatalf("HEAD carries a body: %q", gw.body)
						}
						return
					}
					if isHTML {
						if !strings.Contains(gw.body, "<h1>"+tc.message+"</h1>") {
							t.Fatalf("page lacks %q: %q", tc.message, gw.body)
						}
						// The gateway knows the dashboard's address (the gate is given it),
						// yet its refusal links nowhere: a link would make the bytes
						// depend on whether the gateway is running.
						if strings.Contains(gw.body, "href") || strings.Contains(gw.body, "<a ") || strings.Contains(gw.body, dashboard) {
							t.Fatalf("a refusal links somewhere: %q", gw.body)
						}
						return
					}
					if code, message := denialEnvelope(t, []byte(gw.body)); code != tc.code || message != tc.message {
						t.Fatalf("envelope %q %q, want %q %q", code, message, tc.code, tc.message)
					}
				})
			}
		})
	}
}

// Legacy allow-list refusals exist on an app's own node only (the gateway
// routes people-scoped apps). They still use the shared page, with no app name
// and no link.
func TestLegacyACLRefusalUsesTheSharedPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		who     *apitype.WhoIsResponse
		err     error
		code    string
		message string
	}{
		{"not on the list", whoIsUser("eve@example.com", "phone"), nil, "no_grant", "You do not have access to this app"},
		{"caller not identified", nil, os.ErrDeadlineExceeded, "whois_failed", "Unable to identify this connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := ACLMiddleware([]string{"alice@example.com"}, fakeWhoIsClient(t, tc.who, tc.err))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("refused request reached the app") }))
			for _, header := range []map[string]string{nil, {"Sec-Fetch-Mode": "navigate"}} {
				got := serveGenerated(h, http.MethodGet, header)
				if got.status != http.StatusForbidden || got.decision != "denied" || got.reason != "acl" {
					t.Fatalf("legacy refusal %+v", got)
				}
				if header == nil {
					if code, message := denialEnvelope(t, []byte(got.body)); code != tc.code || message != tc.message {
						t.Fatalf("envelope %q %q", code, message)
					}
				} else if !strings.Contains(got.body, "<h1>"+tc.message+"</h1>") || strings.Contains(got.body, "href") {
					t.Fatalf("page %q", got.body)
				}
			}
		})
	}
}

// Closing a gateway route and stopping an app node are different events in the
// access log (route_closed, node_closed) and the same one to the visitor.
func TestClosingPageDoesNotDependOnTheEntryPath(t *testing.T) {
	for _, header := range []map[string]string{nil, {"Accept": "application/json"}, {"Sec-Fetch-Mode": "navigate"}} {
		var bodies []string
		for _, gateway := range []bool{true, false} {
			cause := errNodeClosed
			options := ProxyOptions{App: "photos"}
			if gateway {
				cause = errRouteClosed
				options = ProxyOptions{Gateway: &GatewayProxyOptions{Authority: func() string { return "home.tailnet.ts.net:8443" }, App: "photos", HostLabel: "this host"}}
			}
			options.Dialer = failingDialer{context.Canceled}
			h, err := NewProxyHandlerWithOptions("http://192.0.2.77:6543", nil, options)
			if err != nil {
				t.Fatal(err)
			}
			obs := &accessObservation{}
			ctx, cancel := context.WithCancelCause(context.WithValue(context.Background(), accessObservationKey{}, obs))
			cancel(cause)
			r := httptest.NewRequest(http.MethodPost, "/path", strings.NewReader("payload")).WithContext(ctx)
			for k, v := range header {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			obs.mu.Lock()
			reason, decision, class := obs.reason, obs.decision, obs.errorClass
			obs.mu.Unlock()
			want := "node_closed"
			if gateway {
				want = "route_closed"
			}
			if w.Code != http.StatusServiceUnavailable || reason != want || decision != "" || class != "" {
				t.Fatalf("gateway=%v: %d reason=%q decision=%q class=%q: %q", gateway, w.Code, reason, decision, class, w.Body.String())
			}
			bodies = append(bodies, w.Body.String()+"|"+w.Header().Get("Content-Type")+"|"+w.Header().Get("Cache-Control")+"|"+w.Header().Get("Content-Security-Policy"))
		}
		if bodies[0] != bodies[1] {
			t.Fatalf("the closing page depends on the entry path:\ngateway: %q\nnode:    %q", bodies[0], bodies[1])
		}
		if !strings.Contains(bodies[0], "photos is being reconfigured; try again") || strings.Contains(bodies[0], "route_closed") || strings.Contains(bodies[0], "node_closed") {
			t.Fatalf("closing page text: %q", bodies[0])
		}
	}
}

// startDirectAppNode starts photos' own node on loopback, as the daemon does,
// with every request on it identified as who. It returns the node's base URL.
func startDirectAppNode(t *testing.T, f *gatewayFixture, who *apitype.WhoIsResponse) string {
	t.Helper()
	reg, err := registry.Load(f.path)
	if err != nil {
		t.Fatal(err)
	}
	var svc registry.Service
	for _, s := range reg.Services {
		if s.Name == "photos" {
			svc = s
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	node := &accessListenerFake{fakeTSNetServer: &fakeTSNetServer{localClient: fakeWhoIsClient(t, who, nil)}, ln: ln}
	installed := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return node }
	t.Cleanup(func() { newTSNetServerFn = installed })
	if err = f.s.startNodeLocked(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.s.stopNodeLocked("photos") })
	return "http://" + ln.Addr().String()
}

// What an app's own node tells a refused caller must not depend on whether the
// gateway is running: the access log's bytes_out for that person and app would
// otherwise change when the owner turns the gateway on or off. The page is also
// the one the gateway gives the same caller.
func TestDirectNodeDenialIsTheSameWithTheGatewayRunningAndStopped(t *testing.T) {
	alice := requestMember("alice")
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		f.who.Store(alice)
		// photos is requestable and alice may use payroll only: she is admitted
		// by the gateway, refused at photos, and may ask for it.
		if _, err := registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) {
			s.PortalOnly, s.Requestable = false, true
			return s, nil
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ChangePerson(f.path, "alice", []string{"payroll"}, nil, true, true); err != nil {
			t.Fatal(err)
		}
	})
	f.waitRoute(t, "photos", "running")
	node := startDirectAppNode(t, f, alice)
	type request struct {
		name, method string
		header       map[string]string
	}
	requests := []request{
		{"browser", http.MethodGet, map[string]string{"Sec-Fetch-Mode": "navigate"}},
		{"api", http.MethodPost, nil},
	}
	fetch := func(client *http.Client, url string, r request) string {
		t.Helper()
		req, err := http.NewRequest(r.method, url, strings.NewReader("payload"))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range r.header {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status %d: %v: %q", r.name, resp.StatusCode, err, body)
		}
		return string(body)
	}
	direct := &http.Client{Timeout: testwait.Budget(t)}
	t.Cleanup(direct.CloseIdleConnections)
	gateway := f.client(t)
	running := map[string]string{}
	for _, r := range requests {
		running[r.name] = fetch(direct, node, r)
		// The gateway gives the same caller the same page for the same request.
		if got := fetch(gateway, "https://home.tailnet.ts.net:8443/", r); got != running[r.name] {
			t.Fatalf("%s: gateway and app node differ:\ngateway: %q\nnode:    %q", r.name, got, running[r.name])
		}
	}
	if !strings.Contains(running["browser"], "<h1>You do not have access to this app: photos</h1>") {
		t.Fatalf("the refused caller who can request the app is not told its name: %q", running["browser"])
	}
	if strings.Contains(running["browser"], "href") || strings.Contains(running["browser"], "home.tailnet.ts.net") {
		t.Fatalf("a refusal links to the dashboard: %q", running["browser"])
	}
	f.s.closePortal()
	for _, r := range requests {
		if got := fetch(direct, node, r); got != running[r.name] {
			t.Fatalf("%s: the node's refusal changed when the gateway stopped:\nrunning: %q\nstopped: %q", r.name, running[r.name], got)
		}
	}
	// ... and so did what the access log recorded for it.
	testwait.Until(t, "four direct refusals recorded", func() bool {
		n := 0
		for _, e := range f.events.all() {
			if e.Kind == "http" && e.App == "photos" && e.Via == "" {
				n++
			}
		}
		return n == 2*len(requests)
	})
	bytesOut := map[string]map[int64]int{}
	for _, e := range f.events.all() {
		if e.Kind == "http" && e.App == "photos" && e.Via == "" {
			if bytesOut[e.Method] == nil {
				bytesOut[e.Method] = map[int64]int{}
			}
			bytesOut[e.Method][e.BytesOut]++
		}
	}
	for method, sizes := range bytesOut {
		if len(sizes) != 1 {
			t.Fatalf("%s refusals recorded different bytes_out with the gateway running and stopped: %v", method, sizes)
		}
	}
}
