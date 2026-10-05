package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestCatalog(t *testing.T) {
	c := List()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	want := strings.Fields("home-assistant jellyfin plex immich nextcloud open-webui ollama comfyui grafana jupyter uptime-kuma paperless-ngx vaultwarden syncthing portainer generic-web")
	if len(c.Recipes) != len(want) {
		t.Fatalf("catalog: %d recipes", len(c.Recipes))
	}
	for _, id := range want {
		t.Run(id, func(t *testing.T) {
			r, ok := Lookup(id)
			if !ok || r.HealthPath == "" || len(r.Notes[0].Sources) == 0 {
				t.Fatalf("missing recipe contract: %s", id)
			}
		})
	}
	c.Recipes[0].ID = "changed"
	c.Recipes[0].Notes[0].Text = "changed"
	r, ok := Lookup("home-assistant")
	if !ok || strings.Contains(r.Notes[0].Text, "changed") {
		t.Fatal("catalog copy modified global data")
	}
	if _, ok := Lookup("missing"); ok {
		t.Fatal("unknown recipe found")
	}
}
func TestCatalogInvalidData(t *testing.T) {
	mutations := map[string]func(*Catalog){
		"schema": func(c *Catalog) { c.SchemaVersion = 2 }, "identity": func(c *Catalog) { c.Recipes[0].ID = "" }, "duplicate": func(c *Catalog) { c.Recipes[1].ID = c.Recipes[0].ID },
		"ports": func(c *Catalog) { c.Recipes[0].DefaultPorts = nil }, "port": func(c *Catalog) { c.Recipes[0].DefaultPorts[0] = 0 }, "target": func(c *Catalog) { c.Recipes[0].DefaultTarget = "http://example.com:80" },
		"health": func(c *Catalog) { c.Recipes[0].HealthPath = "//example.com" }, "safety": func(c *Catalog) { c.Recipes[0].SafetyLevel = "unknown" }, "safety-note": func(c *Catalog) { c.Recipes[0].SafetyNote = "" },
		"path": func(c *Catalog) { c.Recipes[0].Fingerprints[0].Path = "/x?token=abc" }, "confidence": func(c *Catalog) { c.Recipes[0].Fingerprints[0].Confidence = "sure" },
		"title": func(c *Catalog) { c.Recipes[0].Fingerprints[0].Value = "" }, "body": func(c *Catalog) {
			c.Recipes[0].Fingerprints[0] = Fingerprint{Path: "/", Kind: "body", Confidence: "high"}
		},
		"header": func(c *Catalog) {
			c.Recipes[0].Fingerprints[0] = Fingerprint{Path: "/", Kind: "header", Confidence: "high"}
		}, "json": func(c *Catalog) {
			c.Recipes[0].Fingerprints[0] = Fingerprint{Path: "/", Kind: "json", Confidence: "high"}
		},
		"kind": func(c *Catalog) { c.Recipes[0].Fingerprints[0].Kind = "unknown" }, "advice": func(c *Catalog) { c.Recipes[0].Notes[0].Snippet = "" }, "docs": func(c *Catalog) { c.Recipes[0].Notes[0].Sources[0].Accessed = "" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			c := List()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}
func TestListenerParsers(t *testing.T) {
	fixtures := []struct {
		name, raw string
		parse     func(string) []Listener
		want      []Listener
	}{
		{"lsof", "p123\nn127.0.0.1:8123\nn[::1]:8888\nn*:3000\nn192.0.2.4:80\nninvalid\n", parseLsof, []Listener{{"127.0.0.1", 8123}, {"::1", 8888}, {"127.0.0.1", 3000}, {"::1", 3000}}},
		{"linux", " sl local_address rem_address st\n0: 0100007F:1FBB 00000000:0000 0A\n1: 00000000000000000000000001000000:22B8 0:0 0A\n2: 00000000:1F90 0:0 0A\n3: 010200C0:0050 0:0 0A\n4: 0100007F:0050 0:0 01\n5: bad:xx 0:0 0A\n6: 01:0001 0:0 0A\n7: 0100007F:FFFFF 0:0 0A\n8: bad 0:0 0A", parseProcTCP, []Listener{{"127.0.0.1", 8123}, {"::1", 8888}, {"127.0.0.1", 8080}}},
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.parse(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("listeners=%v want %v", got, tc.want)
			}
		})
	}
	for _, a := range []string{"localhost:80", "192.0.2.1:80", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:abc", "bad"} {
		if got := loopbackListeners(a); len(got) != 0 {
			t.Fatalf("unsafe/invalid address admitted: %s %v", a, got)
		}
	}
	if got := loopbackListeners(":80"); len(got) != 2 {
		t.Fatalf("empty wildcard: %v", got)
	}
}
func TestFingerprintKinds(t *testing.T) {
	cases := []struct {
		name string
		f    Fingerprint
		r    response
		want bool
	}{
		{"title", Fingerprint{Kind: "title", Value: "Jupyter"}, response{body: "<TITLE class=x>Jupyter Server</TITLE>", status: 200}, true},
		{"empty", Fingerprint{Kind: "title"}, response{body: "<title> </title>", status: 200}, false},
		{"body", Fingerprint{Kind: "body", Value: "ollama is running"}, response{body: "Ollama is running", status: 200}, true},
		{"header", Fingerprint{Kind: "header", Header: "X-Syncthing-Version"}, response{header: http.Header{"X-Syncthing-Version": []string{"v2"}}, status: 200}, true},
		{"json", Fingerprint{Kind: "json", JSONMatch: map[string]string{"system.comfyui_version": "*"}}, response{body: `{"system":{"comfyui_version":"1.0"}}`, status: 200}, true},
		{"wrong-json", Fingerprint{Kind: "json", JSONMatch: map[string]string{"ProductName": "Jellyfin"}}, response{body: `{"ProductName":"Emby"}`, status: 200}, false},
		{"missing-json", Fingerprint{Kind: "json", JSONMatch: map[string]string{"a.b": "*"}}, response{body: `{"a":1}`, status: 200}, false},
		{"null-json", Fingerprint{Kind: "json", JSONMatch: map[string]string{"a": "*"}}, response{body: `{"a":null}`, status: 200}, false},
		{"invalid-json", Fingerprint{Kind: "json", JSONMatch: map[string]string{"a": "*"}}, response{body: `not json`, status: 200}, false},
		{"error", Fingerprint{Kind: "body", Value: "Ollama"}, response{body: "Ollama", status: 500}, false},
		{"unknown", Fingerprint{Kind: "unknown"}, response{status: 200}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fingerprintMatches(tc.f, tc.r); got != tc.want {
				t.Fatalf("match=%t want %t", got, tc.want)
			}
		})
	}
}
func listenerFor(t *testing.T, server *httptest.Server) Listener {
	t.Helper()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	return Listener{u.Hostname(), port}
}
func TestDetectionHTTPBoundary(t *testing.T) {
	var requests, trapHits atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trapHits.Add(1) }))
	defer trap.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("credentials or write request: %v", r.Header)
		}
		switch r.URL.Path {
		case "/":
			http.Redirect(w, r, trap.URL, http.StatusFound)
		case "/System/Info/Public":
			fmt.Fprint(w, `{"ProductName":"Jellyfin","Secret":"do-not-emit"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	l := listenerFor(t, server)
	t.Setenv("HTTP_PROXY", trap.URL)
	t.Setenv("ALL_PROXY", trap.URL)
	listeners := []Listener{l, l, {"example.com", l.Port}, {"192.0.2.1", l.Port}, {l.Host, 0}}
	result := detectListeners(context.Background(), listeners, []RegisteredService{{"existing", server.URL}, {"same-port-lan", fmt.Sprintf("http://192.0.2.1:%d", l.Port)}})
	if len(result.Listeners) != 1 || len(result.Matches) != 1 || result.Matches[0].RecipeID != "jellyfin" || result.Matches[0].Confidence != "high" {
		t.Fatalf("detection=%+v", result)
	}
	if !reflect.DeepEqual(result.Matches[0].Registered, []string{"existing"}) {
		t.Fatalf("registration=%v", result.Matches[0].Registered)
	}
	if trapHits.Load() != 0 {
		t.Fatalf("redirect/proxy received %d requests", trapHits.Load())
	}
	if requests.Load() < 2 {
		t.Fatalf("probe control did not run: %d requests", requests.Load())
	}
	b, _ := json.Marshal(result)
	if strings.Contains(string(b), "do-not-emit") {
		t.Fatal("response body leaked")
	}
	for _, addr := range []string{"example.com:80", "192.0.2.1:80", "invalid"} {
		if _, err := loopbackDial(context.Background(), "tcp", addr); err == nil || !strings.Contains(err.Error(), "non-loopback") {
			t.Fatalf("unsafe dial: %s %v", addr, err)
		}
	}
}
func TestDetectionEveryRecipe(t *testing.T) {
	for _, r := range List().Recipes {
		t.Run(r.ID, func(t *testing.T) {
			f := r.Fingerprints[0]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != f.Path {
					if req.URL.Path == "/" {
						fmt.Fprint(w, "OK")
					} else {
						http.NotFound(w, req)
					}
					return
				}
				switch f.Kind {
				case "title":
					name := f.Value
					if name == "" {
						name = "Unknown site"
					}
					fmt.Fprintf(w, "<title>%s</title>", name)
				case "body":
					fmt.Fprint(w, f.Value)
				case "json":
					values := map[string]any{}
					for k, v := range f.JSONMatch {
						parts := strings.Split(k, ".")
						m := values
						for _, part := range parts[:len(parts)-1] {
							child := map[string]any{}
							m[part] = child
							m = child
						}
						m[parts[len(parts)-1]] = v
					}
					b, _ := json.Marshal(values)
					w.Write(b)
				}
			}))
			defer server.Close()
			result := detectListeners(context.Background(), []Listener{listenerFor(t, server)}, nil)
			found := false
			for _, m := range result.Matches {
				if m.RecipeID == r.ID {
					found = true
				}
			}
			if !found {
				t.Fatalf("recipe not detected: %s %+v", r.ID, result)
			}
		})
	}
}
func TestDetectionTimeoutAndBodyBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := detectListeners(ctx, []Listener{{"127.0.0.1", 8123}}, nil)
	if got.Complete || len(got.Warnings) != 1 {
		t.Fatalf("cancelled detection: %+v", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 64<<10)+"<title>ComfyUI</title>")
	}))
	defer server.Close()
	got = detectListeners(context.Background(), []Listener{listenerFor(t, server)}, nil)
	if len(got.Matches) != 0 {
		t.Fatalf("matched beyond body bound: %v", got.Matches)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	if got := probeClient().Timeout; got != 700*time.Millisecond {
		t.Fatalf("probe request timeout = %s, want 700ms", got)
	}
	// The caller's context has no deadline and the backend never answers, so
	// only the probe's own 700ms timeout can end detection.
	detected := make(chan Detection, 1)
	go func() { detected <- detectListeners(context.Background(), []Listener{listenerFor(t, slow)}, nil) }()
	got = testwait.Recv(t, detected, "detection ended by the probe's own timeout")
	if len(got.Matches) != 0 || !got.Complete {
		t.Fatalf("short timeout failed: %v", got)
	}
	// A known closed numeric loopback port proves connection failure is tolerated.
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := closed.Addr().(*net.TCPAddr)
	closed.Close()
	got = detectListeners(context.Background(), []Listener{{"127.0.0.1", addr.Port}}, nil)
	if len(got.Matches) != 0 {
		t.Fatal("closed port matched")
	}
}
func TestRegisteredLoopbackEquivalence(t *testing.T) {
	got := registeredNames(Listener{"::1", 80}, []RegisteredService{{"z", "http://localhost"}, {"a", "http://[::1]:80/base"}, {"other-family", "http://127.0.0.1:80"}, {"tls", "https://[::1]:80"}, {"bad", "://"}})
	if !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("registered=%v", got)
	}
}

func TestDetectInventoryAndFailure(t *testing.T) {
	old := listenTCPFn
	t.Cleanup(func() { listenTCPFn = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "Ollama is running") }))
	defer server.Close()
	listenTCPFn = func(context.Context) ([]Listener, error) { return []Listener{listenerFor(t, server)}, nil }
	got, err := Detect(context.Background(), nil)
	if err != nil || len(got.Matches) != 1 || got.Matches[0].RecipeID != "ollama" {
		t.Fatalf("inventory: %+v %v", got, err)
	}
	listenTCPFn = func(context.Context) ([]Listener, error) { return nil, fmt.Errorf("fixture inventory unavailable") }
	if _, err := Detect(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "inventory unavailable") {
		t.Fatalf("inventory error lost: %v", err)
	}
}
func TestMalformedCatalogPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("corrupt catalog silently accepted")
		}
	}()
	decodeCatalog([]byte(`not json`))
}
func TestDetectionOrdering(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<title>Home Assistant ComfyUI</title>") }))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "Ollama is running") }))
	defer b.Close()
	l := listenerFor(t, a)
	m := listenerFor(t, b)
	got := detectListeners(context.Background(), []Listener{m, l, {"::1", l.Port}}, nil)
	if len(got.Matches) != 3 {
		t.Fatalf("match ordering fixture failed: %+v", got)
	}
	for i := 1; i < len(got.Matches); i++ {
		a, b := got.Matches[i-1], got.Matches[i]
		if a.Target > b.Target || a.Target == b.Target && a.RecipeID > b.RecipeID {
			t.Fatalf("not sorted: %+v", got)
		}
	}
	bad := Catalog{Recipes: []Recipe{{ID: "bad", Fingerprints: []Fingerprint{{Path: "//example.com", Kind: "title"}}}}}
	if got := probeListener(context.Background(), probeClient(), l, bad, nil); len(got) != 1 || got[0].RecipeID != "generic-web" {
		t.Fatalf("invalid catalog path: %v", got)
	}
}

// Read-only OS inventory must find a socket this test actually owns. No HTTP
// requests are sent to any other listener, and no daemon config is accessed.
func TestOSInventoryFindsFixture(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	// Inventory runs real OS tooling; its speed on a loaded runner is not the
	// property. The context only stops it after the test ends.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type inventory struct {
		listeners []Listener
		err       error
	}
	listed := make(chan inventory, 1)
	go func() { got, err := listeningTCP(ctx); listed <- inventory{got, err} }()
	res := testwait.Recv(t, listed, "OS listener inventory returned")
	got, err := res.listeners, res.err
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range got {
		if l.Host == "127.0.0.1" && l.Port == port {
			return
		}
	}
	t.Fatalf("OS inventory missed controlled loopback listener on port %d", port)
}

func TestLoopbackRefusalWithFakeDial(t *testing.T) {
	calls := 0
	dial := func(context.Context, string, string) (net.Conn, error) { calls++; return nil, nil }
	for _, addr := range []string{"example.com:80", "192.0.2.1:80", "invalid"} {
		_, err := dialLoopback(context.Background(), "tcp", addr, dial)
		if err == nil || !strings.Contains(err.Error(), "non-loopback") {
			t.Fatalf("unsafe address admitted: %s %v", addr, err)
		}
	}
	if calls != 0 {
		t.Fatalf("denied addresses reached dial: %d", calls)
	}
	_, err := dialLoopback(context.Background(), "tcp", "127.0.0.1:80", dial)
	if err != nil || calls != 1 {
		t.Fatalf("positive dial control: %d %v", calls, err)
	}
}
