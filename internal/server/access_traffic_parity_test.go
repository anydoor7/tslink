package server

import (
	"bytes"
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

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"github.com/coder/websocket"
)

// Exercise both production assembly paths, with loopback tsnet transports and
// fake WhoIs only. Each request passes the real gates, proxy/file handler and
// access middleware into one real, isolated durable writer.
func TestAccessTrafficHandlerParity(t *testing.T) {
	equalJSON := func(a, b any) bool {
		t.Helper()
		left, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		right, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Equal(left, right)
	}
	// Every scenario installs its own app-node fake. None may outlive its
	// subtest, which the sentinel check after the loop proves.
	sentinel := &fakeTSNetServer{}
	originalNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return sentinel }
	t.Cleanup(func() { newTSNetServerFn = originalNew })
	for _, scenario := range []string{"allowed", "denied", "denied_browser", "file", "websocket", "backend_error", "upstream_unavailable", "timeout", "closing"} {
		t.Run(scenario, func(t *testing.T) {
			const upload, download = "upload-body", "download-body"
			denied := scenario == "denied" || scenario == "denied_browser"
			// closing: the backend holds the request until TSLink cuts it, once
			// per entry path, and the client gets the page TSLink generates.
			entered := make(chan struct{}, 2)
			if scenario == "closing" {
				// Keep the connection open after the forced close, so the client
				// reads the page the handler wrote rather than a reset.
				oldShutdown, oldClose := shutdownHTTPServerFn, closeHTTPServerFn
				shutdownHTTPServerFn = func(context.Context, *http.Server) error { return context.DeadlineExceeded }
				closeHTTPServerFn = func(*http.Server) error { return nil }
				t.Cleanup(func() { shutdownHTTPServerFn, closeHTTPServerFn = oldShutdown, oldClose })
			}
			if scenario == "timeout" {
				// Name resolution of the backend host times out: a genuine
				// backend timeout on both entry paths, with no network access.
				resolver := net.DefaultResolver
				net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
					return nil, &net.DNSError{Err: "i/o timeout", IsTimeout: true}
				}}
				t.Cleanup(func() { net.DefaultResolver = resolver })
			}
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "websocket" {
					ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
					if err != nil {
						return
					}
					defer ws.CloseNow()
					typ, body, err := ws.Read(r.Context())
					if err == nil {
						_ = ws.Write(r.Context(), typ, body)
					}
					return
				}
				if scenario == "closing" {
					// Read the upload first: only then does the server notice
					// the proxy dropping the connection and end the context.
					_, _ = io.Copy(io.Discard, r.Body)
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != upload {
					t.Errorf("upload %q: %v", body, err)
				}
				if scenario == "backend_error" {
					w.WriteHeader(http.StatusInternalServerError)
				}
				_, _ = io.WriteString(w, download)
			}))
			t.Cleanup(backend.Close)
			files := t.TempDir()
			if err := os.WriteFile(filepath.Join(files, "sample.txt"), []byte(download), 0600); err != nil {
				t.Fatal(err)
			}
			var store *accesslog.Store
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				var err error
				store, err = accesslog.New(filepath.Dir(f.path), accesslog.Options{}, func() time.Time { return time.Unix(0, f.now.Load()) })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.s.closePortal(); drainAccess(t, store) })
				f.s.accessWriter = store
				_, err = registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) {
					s.PortalOnly = false
					s.Target = backend.URL
					if scenario == "timeout" {
						s.Target = "http://backend.timeout.test.:8080"
					}
					if scenario == "file" {
						s.Type, s.Target, s.Path = registry.TypeFile, "", files
					}
					return s, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if denied {
					// Keep permission to another app so the TLS connection gate
					// admits this person and the request reaches HTTP denial.
					if _, err = registry.ChangePerson(f.path, "alice", []string{"payroll"}, nil, true, true); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "denied_browser" {
					// A requestable app names itself and offers the request link
					// to a caller who may ask; the page also links the dashboard.
					if _, err = registry.MutateService(f.path, "photos", func(s registry.Service) (registry.Service, error) {
						s.Requestable = true
						return s, nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			})
			f.waitRoute(t, "photos", "running")
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
			appNode := &accessListenerFake{fakeTSNetServer: &fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("ALICE", "phone"), nil)}, ln: ln}
			installed := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return appNode }
			t.Cleanup(func() { newTSNetServerFn = installed })
			if err = f.s.startNodeLocked(t.Context(), svc); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.s.stopNodeLocked("photos") })
			if scenario == "upstream_unavailable" {
				backend.Close()
			}
			gatewayClient := f.client(t)
			appClient := &http.Client{Timeout: testwait.Budget(t)}
			t.Cleanup(appClient.CloseIdleConnections)
			received := map[string]int64{}
			pages := map[string]string{}
			for _, endpoint := range []struct {
				via    string
				url    string
				client *http.Client
			}{
				{"portal", "https://home.tailnet.ts.net:8443", gatewayClient},
				{"", "http://" + ln.Addr().String(), appClient},
			} {
				if scenario == "websocket" {
					ctx, cancel := context.WithTimeout(t.Context(), testwait.Budget(t))
					ws, _, err := websocket.Dial(ctx, strings.Replace(endpoint.url, "http", "ws", 1)+"/ws", &websocket.DialOptions{HTTPClient: endpoint.client})
					if err != nil {
						cancel()
						t.Fatal(err)
					}
					if err = ws.Write(ctx, websocket.MessageText, []byte(upload)); err != nil {
						t.Fatal(err)
					}
					_, body, err := ws.Read(ctx)
					if err != nil || string(body) != upload {
						t.Fatalf("websocket echo %q: %v", body, err)
					}
					_ = ws.CloseNow()
					cancel()
					continue
				}
				method, path, body := "POST", "/", upload
				wantStatus := http.StatusOK
				if scenario == "file" {
					method, path, body = "GET", "/sample.txt", ""
				}
				if denied {
					wantStatus = http.StatusForbidden
				}
				if scenario == "denied_browser" {
					method, path, body = "GET", "/", ""
				}
				if scenario == "closing" {
					wantStatus = http.StatusServiceUnavailable
				}
				if scenario == "backend_error" {
					wantStatus = http.StatusInternalServerError
				}
				if scenario == "upstream_unavailable" {
					wantStatus = http.StatusBadGateway
				}
				if scenario == "timeout" {
					wantStatus = http.StatusGatewayTimeout
				}
				req, err := http.NewRequest(method, endpoint.url+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "denied_browser" {
					req.Header.Set("Sec-Fetch-Mode", "navigate")
				}
				type reply struct {
					status int
					body   []byte
					err    error
				}
				replies := make(chan reply, 1)
				go func() {
					resp, err := endpoint.client.Do(req)
					if err != nil {
						replies <- reply{err: err}
						return
					}
					got, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					replies <- reply{resp.StatusCode, got, err}
				}()
				if scenario == "closing" {
					testwait.Recv(t, entered, "backend entered")
					if endpoint.via == "portal" {
						// TSLink tears the gateway route down.
						f.s.portalRun.routesMu.Lock()
						route := f.s.portalRun.routes["photos"]
						f.s.portalRun.routesMu.Unlock()
						route.cancel()
					} else {
						// TSLink stops the app node while the request runs.
						f.s.mu.Lock()
						f.s.stopNodeLocked("photos")
						f.s.mu.Unlock()
					}
				}
				answer := testwait.Recv(t, replies, "response")
				got, err := answer.body, answer.err
				if err != nil || answer.status != wantStatus {
					t.Fatalf("HTTP %d: %v", answer.status, err)
				}
				if !denied && scenario != "upstream_unavailable" && scenario != "timeout" && scenario != "closing" && string(got) != download {
					t.Fatalf("download %q", got)
				}
				pages[endpoint.via] = string(got)
				// What each client actually received: the recorded bytes_out of
				// its entry path must equal it, not merely agree with the other.
				received[endpoint.via] = int64(len(got))
			}
			dir := filepath.Dir(f.path)
			testwait.Until(t, "both handler chains recorded", func() bool {
				r, err := accesslog.Query(dir, accesslog.Filter{App: "photos"})
				return err == nil && r.Summary.Traffic.HTTP.Requests == 2
			})
			f.s.stopNodeLocked("photos")
			f.s.closePortal()
			drainAccess(t, store)
			r, err := accesslog.Query(dir, accesslog.Filter{App: "photos", Limit: 10000})
			if err != nil {
				t.Fatal(err)
			}
			traffic := r.Summary.Traffic
			if traffic.HTTP.Requests != 2 || len(traffic.PeopleApps) != 1 || len(traffic.Apps) != 1 {
				t.Fatalf("double-entry grouping: %+v", traffic)
			}
			p := traffic.PeopleApps[0]
			if p.SubjectKind != "human" || p.Login != "alice" || p.App != "photos" || traffic.Apps[0].App != "photos" {
				t.Fatalf("wrong business key: %+v", traffic)
			}
			var in, out int64
			vias := map[string]int{}
			type outcome struct {
				Status                       int
				Decision, Reason, ErrorClass string
				BytesOut                     int64
			}
			outcomes := map[string]outcome{}
			for _, e := range r.Events {
				if e.Kind != "http" {
					continue
				}
				if e.SchemaVersion != 1 || e.App != "photos" {
					t.Fatalf("event changed: %+v", e)
				}
				if scenario == "upstream_unavailable" && (e.Status != 502 || e.ErrorClass != "connection_refused") {
					t.Fatalf("backend failure classification: %+v", e)
				}
				if scenario == "timeout" && (e.Status != 504 || e.ErrorClass != "timeout") {
					t.Fatalf("backend timeout classification: %+v", e)
				}
				outcomes[e.Via] = outcome{e.Status, e.Decision, e.Reason, e.ErrorClass, e.BytesOut}
				in += e.BytesIn
				out += e.BytesOut
				vias[e.Via]++
				if scenario == "allowed" || scenario == "backend_error" {
					if e.BytesIn != int64(len(upload)) || e.BytesOut != int64(len(download)) {
						t.Fatalf("payload accounting: %+v", e)
					}
				}
				if scenario == "file" && (e.BytesIn != 0 || e.BytesOut != int64(len(download))) {
					t.Fatalf("file accounting: %+v", e)
				}
				if scenario == "websocket" && (e.Status != 101 || e.BytesIn < int64(len(upload)) || e.BytesOut < int64(len(upload))) {
					t.Fatalf("upgrade accounting: %+v", e)
				}
			}
			if vias[""] != 1 || vias["portal"] != 1 || traffic.HTTP.BytesIn != in || traffic.HTTP.BytesOut != out || !equalJSON(p.HTTPTraffic, traffic.HTTP) || !equalJSON(traffic.Apps[0].HTTPTraffic, traffic.HTTP) {
				t.Fatalf("two-entry totals: %+v, vias=%v in=%d out=%d", traffic, vias, in, out)
			}
			// The same request must yield the same authorization decision, status,
			// backend classification and response size whichever entry carried
			// it. Totals prove aggregation; only per-entry equality proves the two
			// producers generate the same response.
			direct, gateway := outcomes[""], outcomes["portal"]
			if direct.Status != gateway.Status || direct.Decision != gateway.Decision || direct.ErrorClass != gateway.ErrorClass {
				t.Fatalf("entry paths disagree: direct=%+v gateway=%+v", direct, gateway)
			}
			if scenario != "websocket" {
				for via, o := range outcomes {
					if received[via] != o.BytesOut {
						t.Fatalf("recorded bytes_out differs from the body %q received: %d != %d", via, o.BytesOut, received[via])
					}
				}
			}
			// bytes_out per person and app must not depend on the entry path.
			// Every scenario except the websocket stream (frame timing) is a
			// complete response, so the two entries must record the same size.
			if scenario != "websocket" && (direct.BytesOut != gateway.BytesOut || direct.BytesOut == 0) {
				t.Fatalf("entry paths record different bytes_out: direct=%+v gateway=%+v", direct, gateway)
			}
			if scenario != "websocket" && pages[""] != pages["portal"] {
				t.Fatalf("entry paths answered differently:\ndirect:  %q\ngateway: %q", pages[""], pages["portal"])
			}
			switch scenario {
			case "denied", "denied_browser":
				// The access-log reason is each path's own; what the visitor is
				// told is not.
				if direct.Reason != "people" || gateway.Reason != "no_grant" {
					t.Fatalf("denial reasons: direct=%+v gateway=%+v", direct, gateway)
				}
			case "closing":
				if direct != (outcome{http.StatusServiceUnavailable, "allowed", "node_closed", "", direct.BytesOut}) || gateway != (outcome{http.StatusServiceUnavailable, "allowed", "route_closed", "", gateway.BytesOut}) {
					t.Fatalf("closing outcome: direct=%+v gateway=%+v", direct, gateway)
				}
				if strings.Contains(pages[""], "_closed") {
					t.Fatalf("the visitor is told which path closed: %q", pages[""])
				}
			}
			if scenario == "denied_browser" {
				for _, want := range []string{"You do not have access to this app: photos", "Back to your apps", "Request access", "https://home.tailnet.ts.net"} {
					if !strings.Contains(pages[""], want) {
						t.Fatalf("the app node's page lacks %q: %q", want, pages[""])
					}
				}
			}
			switch scenario {
			case "upstream_unavailable", "timeout":
				// A backend that cannot answer is an authorized request that
				// failed downstream: 502 (504 for a timeout) and a backend class,
				// never a denial or a failure reason. The gateway keeps only the
				// grant reason of any allowed request, never upstream_down.
				status, class := http.StatusBadGateway, "connection_refused"
				if scenario == "timeout" {
					status, class = http.StatusGatewayTimeout, "timeout"
				}
				if direct != (outcome{status, "allowed", "", class, direct.BytesOut}) || gateway.Reason != "granted" || direct.BytesOut == 0 {
					t.Fatalf("backend failure outcome: direct=%+v gateway=%+v", direct, gateway)
				}
			}
			if denied {
				if p.Denied != 2 || p.LastAllowedAt != nil {
					t.Fatalf("denied last allowed: %+v", p)
				}
			} else if p.Allowed != 2 || p.Denied != 0 || p.LastAllowedAt == nil {
				t.Fatalf("allowed decisions: %+v", p)
			}
			limited, err := accesslog.Query(dir, accesslog.Filter{App: "photos", Limit: 1})
			if err != nil || !limited.Truncated || !equalJSON(limited.Summary.Traffic, traffic) {
				t.Fatalf("limit changed traffic: %+v, %v", limited, err)
			}
		})
	}
	if got := newTSNetServerFn(registry.Service{}, "", "", ""); got != tsnetServer(sentinel) {
		t.Fatalf("a scenario left its tsnet fake installed: %T", got)
	}
}
