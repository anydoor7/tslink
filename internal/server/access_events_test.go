package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"tailscale.com/tailcfg"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

var accessTestTime = time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)

type accessListenerFake struct {
	*fakeTSNetServer
	ln           net.Listener
	publicFunnel bool
}

func (f *accessListenerFake) Listen(string, string) (net.Listener, error)    { return f.ln, nil }
func (f *accessListenerFake) ListenTLS(string, string) (net.Listener, error) { return f.ln, nil }
func (f *accessListenerFake) ListenFunnel(string, string, ...tsnet.FunnelOption) (net.Listener, error) {
	if f.publicFunnel {
		return &accessPublicListener{f.ln}, nil
	}
	return f.ln, nil
}

type accessPublicListener struct{ net.Listener }

func (l *accessPublicListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &ipn.FunnelConn{Conn: c}, nil
}

func setupAccessNode(t *testing.T, svc registry.Service, who *apitype.WhoIsResponse, opts accesslog.Options, edit func(string), writer accesslog.Writer, publicFunnel ...bool) (*Server, *accesslog.Store, string, string) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	dir, _ := config.Dir()
	path, _ := config.RegistryPath()
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(path)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &accessListenerFake{fakeTSNetServer: &fakeTSNetServer{localClient: fakeWhoIsClient(t, who, nil)}, ln: ln}
	if svc.Funnel {
		fake.publicFunnel = len(publicFunnel) == 0 || publicFunnel[0]
		fake.status = funnelEnabledStatus("app.tailnet.ts.net.")
	}
	originalNew, originalNow := newTSNetServerFn, serverNowFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	serverNowFn = func() time.Time { return accessTestTime }
	t.Cleanup(func() { newTSNetServerFn = originalNew; serverNowFn = originalNow })
	s, err := New("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.accessOptions = opts
	store, err := accesslog.New(dir, opts, serverNowFn)
	if err != nil {
		t.Fatal(err)
	}
	s.accessWriter = store
	if s.AccessLogWriter() != store {
		t.Fatal("writer seam unavailable")
	}
	if writer != nil {
		s.accessWriter = writer
	}
	if err = s.startNodeLocked(t.Context(), svc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.stopNodeLocked(svc.Name); drainAccess(t, store) })
	return s, store, dir, "http://" + ln.Addr().String()
}
func drainAccess(t *testing.T, store *accesslog.Store) {
	t.Helper()
	store.Close()
	select {
	case <-store.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("access writer drain stalled")
	}
}
func requestAccess(t *testing.T, url, method, body string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer header-token-secret")
	req.Header.Set("Cookie", "session=cookie-secret")
	req.Header.Set("User-Agent", "agent-header-secret")
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}
func oneAccess(t *testing.T, dir string, store *accesslog.Store) accesslog.Event {
	t.Helper()
	drainAccess(t, store)
	r, err := accesslog.Query(dir, accesslog.Filter{})
	// Query also returns committed expiry receipts; this helper inspects HTTP.
	httpEvents := []accesslog.Event{}
	for _, event := range r.Events {
		if event.Kind == "http" {
			httpEvents = append(httpEvents, event)
		}
	}
	if err != nil || len(httpEvents) != 1 {
		t.Fatalf("events %+v %v", r, err)
	}
	b, _ := json.Marshal(r)
	files, _ := filepath.Glob(filepath.Join(dir, "access-log", "*.jsonl"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		b = append(b, raw...)
	}
	for _, secret := range []string{"query-token-secret", "header-token-secret", "cookie-secret", "agent-header-secret", "body-token-secret", "file-body-secret"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("privacy leak %s", b)
		}
	}
	return httpEvents[0]
}
func TestAccessRealListenerDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, login, peopleFor, reason, grant string
		allow                                 []string
		funnel, publicFunnel                  bool
		want                                  int
	}{
		{name: "allowed proxy", login: "alice", want: 201},
		{name: "legacy allowed", login: "alice", allow: []string{"alice"}, grant: "legacy_allow", want: 201},
		{name: "acl denied", login: "mallory", allow: []string{"alice"}, reason: "acl", want: 403},
		{name: "person allowed", login: "alice", peopleFor: "1h", grant: "person", want: 201},
		{name: "person expired", login: "alice", peopleFor: "1h", grant: "person", reason: "expired", want: 403},
		{name: "people denied", login: "mallory", peopleFor: "1h", reason: "people", want: 403},
		{name: "funnel", login: "alice", funnel: true, publicFunnel: true, want: 201},
		{name: "funnel tailnet", login: "alice", funnel: true, want: 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.WriteHeader(201)
				io.WriteString(w, "payload")
			}))
			defer backend.Close()
			svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL, AllowedUsers: tc.allow, Funnel: tc.funnel, PublicAck: tc.funnel}
			var edit func(string)
			if tc.peopleFor != "" {
				svc.PeopleScoped = true
				edit = func(path string) {
					deadline := accessTestTime.Add(time.Hour)
					if tc.name == "person expired" {
						deadline = accessTestTime.Add(-time.Hour)
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var reg map[string]any
					if err = json.Unmarshal(raw, &reg); err != nil {
						t.Fatal(err)
					}
					reg["people"] = []registry.Person{{Login: "alice", Grants: []registry.PersonGrant{{App: "photos", ExpiresAt: &deadline}}}}
					raw, _ = json.Marshal(reg)
					if err = os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, store, dir, url := setupAccessNode(t, svc, whoIsUser(tc.login, tc.login+"-node"), accesslog.Options{}, edit, nil, tc.publicFunnel)
			code := requestAccess(t, url+"/private?token=query-token-secret", "POST", "body-token-secret")
			if code != tc.want {
				t.Fatalf("status %d want %d", code, tc.want)
			}
			e := oneAccess(t, dir, store)
			if e.Status != tc.want || e.Reason != tc.reason || e.Path != "/private" || e.App != "photos" || !e.Time.Equal(accessTestTime) {
				t.Fatalf("record %+v", e)
			}
			decision := "allowed"
			if tc.reason != "" {
				decision = "denied"
			}
			if e.Decision != decision {
				t.Fatalf("decision %+v", e)
			}
			login := tc.login
			if tc.publicFunnel {
				login = "public"
			}
			if e.Identity.Login != login || e.Identity.Remote != "" {
				t.Fatalf("identity %+v", e.Identity)
			}
			if tc.grant != "" && (e.Grant == nil || e.Grant.Kind != tc.grant || e.Grant.Entry != "alice") {
				t.Fatalf("grant %+v", e.Grant)
			}
			if tc.want == 201 && (e.BytesIn != int64(len("body-token-secret")) || e.BytesOut != 7) {
				t.Fatalf("byte counts %+v", e)
			}
		})
	}
}

func TestAccessFunnelTLSIdentity(t *testing.T) {
	for _, public := range []bool{false, true} {
		for _, h2 := range []bool{false, true} {
			t.Run(fmt.Sprintf("public=%t/h2=%t", public, h2), func(t *testing.T) {
				dir := t.TempDir()
				store, err := accesslog.New(dir, accesslog.Options{}, func() time.Time { return accessTestTime })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { drainAccess(t, store) })
				cert := httptest.NewTLSServer(http.NotFoundHandler())
				defer cert.Close()
				tlsConfig := cert.TLS.Clone()
				if h2 {
					tlsConfig.NextProtos = []string{"h2", "http/1.1"}
				}
				raw, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				var transport net.Listener = raw
				if public {
					transport = &accessPublicListener{transport}
				}
				limited := newLimitedListener(tls.NewListener(transport, tlsConfig), 1, "http", "photos")
				identity := NewStaticIdentityResolver(fakeWhoIsClient(t, whoIsUser("alice", "laptop"), nil))
				svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Funnel: true}
				priorKey := struct{ marker string }{"prior"}
				priorSeen := make(chan bool, 1)
				app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					priorSeen <- r.Context().Value(priorKey) == true && r.TLS != nil && r.TLS.HandshakeComplete
					w.WriteHeader(204)
				})
				handler := AccessEventMiddleware(svc, accesslog.Options{}, store, identity, func() time.Time { return accessTestTime }, app)
				completed := make(chan struct{})
				srv := newHTTPServerFn(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					handler.ServeHTTP(w, r)
					close(completed)
				}))
				srv.ConnContext = func(ctx context.Context, c net.Conn) context.Context {
					return context.WithValue(ctx, priorKey, true)
				}
				configureAccessHTTP(srv)
				ln := configureServiceHTTP(srv, svc, limited, nil)
				done := make(chan struct{})
				go func() { defer close(done); _ = srv.Serve(ln) }()
				defer func() { srv.Close(); ln.Close(); <-done }()
				clientTLS := cert.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
				tr := &http.Transport{TLSClientConfig: clientTLS, ForceAttemptHTTP2: h2}
				defer tr.CloseIdleConnections()
				req, _ := http.NewRequest("GET", "https://"+raw.Addr().String()+"/private?token=query-token-secret", nil)
				req.Header.Set("Tailscale-Ingress-Target", "spoofed-target-secret")
				req.Header.Set("X-TSLink-User", "spoofed-user-secret")
				resp, err := (&http.Client{Transport: tr, Timeout: 3 * time.Second}).Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != 204 || (resp.ProtoMajor == 2) != h2 || !<-priorSeen {
					t.Fatalf("TLS/context protocol %s status %d", resp.Proto, resp.StatusCode)
				}
				select {
				case <-completed:
				case <-time.After(time.Second):
					t.Fatal("access event did not enqueue")
				}
				e := oneAccess(t, dir, store)
				wantLogin := "alice"
				if public {
					wantLogin = "public"
				}
				if e.Identity.Login != wantLogin || e.Identity.Remote != "" || public && (e.Identity.Node != "" || len(e.Identity.Tags) != 0) {
					t.Fatalf("Funnel TLS identity %+v", e.Identity)
				}
			})
		}
	}

	if isAccessFunnelConn(nil) {
		t.Fatal("nil transport marked public")
	}
}
func TestAccessRealFileAndPathOptOut(t *testing.T) {
	for _, tc := range []struct {
		name            string
		global, service *bool
		want            string
	}{{"default", nil, nil, "/hello.txt"}, {"service off", nil, boolAccess(false), ""}, {"global off", boolAccess(false), boolAccess(true), ""}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, "hello.txt"), []byte("file-body-secret"), 0600)
			svc := registry.Service{Name: "docs", Type: registry.TypeFile, Path: root, AccessLogPath: tc.service}
			_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{RecordPath: tc.global}, nil, nil)
			if code := requestAccess(t, url+"/hello.txt?token=query-token-secret", "GET", ""); code != 200 {
				t.Fatal(code)
			}
			e := oneAccess(t, dir, store)
			if e.Path != tc.want || e.BytesOut != 16 || e.Identity.Login != "alice" {
				t.Fatalf("file %+v", e)
			}
		})
	}
}
func boolAccess(v bool) *bool { return &v }
func TestAccessRealLimitsAndPreserveHost(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		svc          registry.Service
		body         string
		want         int
	}{
		{name: "upload limit", reason: "limits", svc: registry.Service{RequestLimits: &registry.RequestLimits{MaxBody: "3"}}, body: "oversize", want: 413},
		{name: "preserve host", reason: "preserve_host_unavailable", svc: registry.Service{PreserveHost: true}, want: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("denied request reached backend") }))
			defer backend.Close()
			svc := tc.svc
			svc.Name = "app"
			svc.Type = registry.TypeProxy
			svc.Target = backend.URL
			_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
			if code := requestAccess(t, url+"/blocked", "POST", tc.body); code != tc.want {
				t.Fatal(code)
			}
			e := oneAccess(t, dir, store)
			if e.Decision != "denied" || e.Reason != tc.reason {
				t.Fatalf("denial %+v", e)
			}
		})
	}
}
func TestAccessHealthProbeExcluded(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer backend.Close()
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL}
	_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	if code := health.Probe(t.Context(), svc); code != "" {
		t.Fatal(code)
	}
	// Positive control proves the query would find a request through the gateway.
	requestAccess(t, url+"/health?token=query-token-secret", "GET", "")
	if e := oneAccess(t, dir, store); e.Path != "/health" {
		t.Fatalf("health exclusion %+v", e)
	}
}
func TestAccessTCPRealListener(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := backend.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		c.Write(b)
	}()
	svc := registry.Service{Name: "db", Type: registry.TypeTCP, Target: backend.Addr().String()}
	s, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	c, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte("tcp-body-secret"))
	c.(*net.TCPConn).CloseWrite()
	out, err := io.ReadAll(c)
	c.Close()
	if err != nil || string(out) != "tcp-body-secret" {
		t.Fatalf("tcp %q %v", out, err)
	}
	<-done
	s.stopNodeLocked("db")
	drainAccess(t, store)
	r, err := accesslog.Query(dir, accesslog.Filter{})
	if err != nil || len(r.Events) != 2 {
		t.Fatalf("tcp events %+v %v", r, err)
	}
	var open, closeEvent accesslog.Event
	for _, e := range r.Events {
		if e.Kind == "tcp_open" {
			open = e
		}
		if e.Kind == "tcp_close" {
			closeEvent = e
		}
	}
	if open.Identity.Login != "alice" || closeEvent.Identity.Login != "alice" || open.Connection == "" || open.Connection != closeEvent.Connection || closeEvent.BytesIn != 15 || closeEvent.BytesOut != 15 {
		t.Fatalf("tcp open %+v close %+v", open, closeEvent)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "tcp-body-secret") {
		t.Fatal("tcp payload logged")
	}
}

func TestAccessTCPRealConnectionCap(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	accepted := make(chan net.Conn, tcpMaxActiveConnections)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	svc := registry.Service{Name: "db", Type: registry.TypeTCP, Target: backend.Addr().String()}
	s, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	var clients, peers []net.Conn
	t.Cleanup(func() {
		for _, c := range clients {
			c.Close()
		}
		for _, c := range peers {
			c.Close()
		}
		backend.Close()
		<-acceptDone
	})
	for range tcpMaxActiveConnections {
		c, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
		select {
		case peer := <-accepted:
			peers = append(peers, peer)
		case <-time.After(3 * time.Second):
			t.Fatal("allowed connection did not reach the backend")
		}
	}
	refused, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer refused.Close()
	refused.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	if n, err := refused.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("connection cap did not close the socket: %d %v", n, err)
	}
	s.stopNodeLocked("db")
	drainAccess(t, store)
	r, err := accesslog.Query(dir, accesslog.Filter{Decision: "denied", Limit: 10000})
	if err != nil || len(r.Events) != 2 {
		t.Fatalf("connection-cap events %+v %v", r, err)
	}
	kinds := map[string]bool{}
	for _, e := range r.Events {
		kinds[e.Kind] = true
		if e.Reason != "limits" || e.Identity.Login != "alice" || e.BytesIn != 0 || e.BytesOut != 0 || e.Connection == "" || e.Connection != r.Events[0].Connection {
			t.Fatalf("connection-cap attribution %+v", e)
		}
	}
	if !kinds["tcp_open"] || !kinds["tcp_close"] {
		t.Fatalf("connection-cap lifecycle %+v", r.Events)
	}
	control, err := accesslog.Query(dir, accesslog.Filter{Decision: "allowed", Limit: 10000})
	if err != nil || len(control.Events) != 2*tcpMaxActiveConnections {
		t.Fatalf("allowed connection control count %d: %v", len(control.Events), err)
	}
}
func TestAccessServingWithSaturatedWriter(t *testing.T) {
	// Block the real store's asynchronous enrichment worker, then saturate it.
	svc := registry.Service{Name: "docs", Type: registry.TypeFile, Path: t.TempDir()}
	_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{QueueSize: 1}, nil, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store.RecordResolved(accesslog.Event{App: "control", Kind: "mcp", Decision: "allowed"}, func() accesslog.Identity {
		once.Do(func() { close(entered); <-release })
		return accesslog.Identity{Login: "alice"}
	})
	<-entered
	store.Record(accesslog.Event{App: "queued", Kind: "guest", Decision: "denied", Reason: "people"})
	start := time.Now()
	code := requestAccess(t, url+"/", "GET", "")
	close(release)
	if code != 200 || time.Since(start) > time.Second {
		t.Fatalf("saturation slowed serving status %d", code)
	}
	drainAccess(t, store)
	if store.Health().Drops != 1 {
		t.Fatalf("queue drop %+v", store.Health())
	}
	r, err := accesslog.Query(dir, accesslog.Filter{})
	if err != nil || r.Summary.Count != 2 {
		t.Fatalf("saturation %+v %v", r, err)
	}
}

func TestAccessIdentityUnknownTaggedAndTCPRefusal(t *testing.T) {
	for _, tc := range []struct {
		name        string
		who         *apitype.WhoIsResponse
		login, node string
		tags        int
	}{
		{name: "unknown", who: nil},
		{name: "tagged", who: &apitype.WhoIsResponse{Node: &tailcfg.Node{ComputedName: "machine", Tags: []string{"tag:reader"}}}, node: "machine", tags: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := registry.Service{Name: "files", Type: registry.TypeFile, Path: t.TempDir()}
			_, store, dir, url := setupAccessNode(t, svc, tc.who, accesslog.Options{}, nil, nil)
			requestAccess(t, url+"/", "GET", "")
			e := oneAccess(t, dir, store)
			if e.Identity.Login != tc.login || e.Identity.Node != tc.node || len(e.Identity.Tags) != tc.tags {
				t.Fatalf("attestation %+v", e.Identity)
			}
			if tc.name == "unknown" && e.Identity.Remote != "127.0.0.0/24" {
				t.Fatalf("unknown remote %+v", e.Identity)
			}
		})
	}
	// A simple metadata writer has no lookup callback and does not perform WhoIs.
	recordAccess(nil, accesslog.Event{App: "test"}, nil, "192.168.1.7:80", false)
	capture := &accessEventCapture{}
	recordAccess(capture, accesslog.Event{App: "test"}, nil, "192.168.1.7:80", false)
	if capture.events[0].Identity.Remote != "192.168.1.0/24" {
		t.Fatal("simple writer address not coarse")
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	unlogged, noEvent := tcpAccessBegin(t.Context(), left, "db", "")
	if unlogged != left {
		t.Fatal("absent writer changed the TCP connection")
	}
	noEvent()
	counted := &accessTCPConn{Conn: left}
	if e := counted.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	opts := &tcpAccessOptions{writer: capture, now: func() time.Time { return accessTestTime }}
	ctx := context.WithValue(t.Context(), tcpAccessKey{}, opts)
	_, finish := tcpAccessBegin(ctx, left, "db", "limits")
	finish()
	if len(capture.events) != 3 || capture.events[1].Decision != "denied" || capture.events[2].Reason != "limits" || capture.events[2].Kind != "tcp_close" {
		t.Fatalf("tcp refusal %+v", capture.events)
	}
}

type accessEventCapture struct{ events []accesslog.Event }

func (w *accessEventCapture) Record(e accesslog.Event) bool {
	w.events = append(w.events, e)
	return true
}

func TestAccessPathReloadFingerprintAndBodyFailure(t *testing.T) {
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}
	changed := svc
	changed.AccessLogPath = boolAccess(false)
	if !serviceChanged(svc, changed) {
		t.Fatal("per-service path policy does not reconcile")
	}
	reg := &registry.Registry{SchemaVersion: 2, Services: []registry.Service{svc}}
	first, err := runtimesnapshot.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services[0] = changed
	second, err := runtimesnapshot.RegistryFingerprint(reg, nil)
	if err != nil || first == second {
		t.Fatal("path policy omitted from watcher fingerprint")
	}
	// A streaming upload exceeds the limit after proxy dispatch, exercising the
	// progressBody/ErrorHandler path instead of Content-Length early refusal.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			io.WriteString(w, "ok")
		}
	}))
	defer backend.Close()
	svc.Target = backend.URL
	svc.RequestLimits = &registry.RequestLimits{MaxBody: "3"}
	_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	req, err := http.NewRequest("POST", url+"/upload", io.NopCloser(strings.NewReader("chunked-body-token-secret")))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatal(resp.StatusCode)
	}
	e := oneAccess(t, dir, store)
	if e.Decision != "denied" || e.Reason != "limits" {
		t.Fatalf("chunked denial %+v", e)
	}
}

func TestAccessTCPShutdownDrainsClose(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	received, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		c, e := backend.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		b := make([]byte, 7)
		_, _ = io.ReadFull(c, b)
		close(received)
		io.Copy(io.Discard, c)
	}()
	svc := registry.Service{Name: "db", Type: registry.TypeTCP, Target: backend.Addr().String()}
	s, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	c, e := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.Write([]byte("payload"))
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("backend did not receive payload")
	}
	s.stopNodeLocked("db")
	drainAccess(t, store)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("backend did not close")
	}
	r, e := accesslog.Query(dir, accesslog.Filter{})
	if e != nil || len(r.Events) != 2 || store.Health().Drops != 0 {
		t.Fatalf("shutdown %+v %v %+v", r, e, store.Health())
	}
	found := false
	for _, e := range r.Events {
		found = found || e.Kind == "tcp_close" && e.BytesIn == 7
	}
	if !found {
		t.Fatalf("close event missing %+v", r)
	}
}
func TestAccessUpgradeRealListener(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, b, e := http.NewResponseController(w).Hijack()
		if e != nil {
			return
		}
		defer c.Close()
		b.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		b.Flush()
		io.Copy(c, b)
	}))
	defer backend.Close()
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: backend.URL}
	_, store, dir, url := setupAccessNode(t, svc, whoIsUser("alice", "laptop"), accesslog.Options{}, nil, nil)
	c, e := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	io.WriteString(c, "GET /socket?token=query-token-secret HTTP/1.1\r\nHost: app\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
	reader := bufio.NewReader(c)
	resp, e := http.ReadResponse(reader, nil)
	if e != nil || resp.StatusCode != 101 {
		c.Close()
		t.Fatalf("upgrade %v %v", resp, e)
	}
	c.Write([]byte("body-token-secret"))
	buf := make([]byte, len("body-token-secret"))
	if _, e = io.ReadFull(reader, buf); e != nil {
		c.Close()
		t.Fatal(e)
	}
	c.Close()
	// The reverse proxy completes once the upgraded connection closes.
	deadline := time.Now().Add(3 * time.Second)
	for store.Health().LastWrite == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ev := oneAccess(t, dir, store)
	if ev.Status != 101 || ev.BytesIn != int64(len(buf)) || ev.BytesOut <= int64(len(buf)) {
		t.Fatalf("upgrade count %+v", ev)
	}
}

func TestAccessLegacyPersonAttribution(t *testing.T) {
	legacy := "alice\u2003@example.com"
	p := registry.Person{Login: legacy, Grants: []registry.PersonGrant{{App: "app"}}}
	grant, reason := personDecision(&registry.Registry{People: []registry.Person{p}}, registry.Service{Name: "app"}, strings.ToUpper(legacy), nil, accessTestTime)
	if grant == nil || grant.Entry != legacy || reason != "people" {
		t.Fatalf("legacy match %+v %s", grant, reason)
	}
	grant, _ = personDecision(&registry.Registry{People: []registry.Person{p}}, registry.Service{Name: "app"}, legacy, []string{"tag:reader"}, accessTestTime)
	if grant != nil {
		t.Fatal("machine recorded as a person grant")
	}
	s := &Server{}
	if s.AccessLogWriter() != nil {
		t.Fatal("uninitialized writer should be absent")
	}
}
