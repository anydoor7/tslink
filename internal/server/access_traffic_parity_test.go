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
	for _, scenario := range []string{"allowed", "denied", "file", "websocket", "backend_error", "upstream_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			const upload, download = "upload-body", "download-body"
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
					if scenario == "file" {
						s.Type, s.Target, s.Path = registry.TypeFile, "", files
					}
					return s, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "denied" {
					// Keep permission to another app so the TLS connection gate
					// admits this person and the request reaches HTTP denial.
					if _, err = registry.ChangePerson(f.path, "alice", []string{"payroll"}, nil, true, true); err != nil {
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
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return appNode }
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
			for _, endpoint := range []struct {
				url    string
				client *http.Client
			}{
				{"https://home.tailnet.ts.net:8443", gatewayClient},
				{"http://" + ln.Addr().String(), appClient},
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
				if scenario == "denied" {
					wantStatus = http.StatusForbidden
				}
				if scenario == "backend_error" {
					wantStatus = http.StatusInternalServerError
				}
				if scenario == "upstream_unavailable" {
					wantStatus = http.StatusBadGateway
				}
				req, err := http.NewRequest(method, endpoint.url+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := endpoint.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil || resp.StatusCode != wantStatus {
					t.Fatalf("HTTP %d: %v", resp.StatusCode, err)
				}
				if scenario != "denied" && scenario != "upstream_unavailable" && string(got) != download {
					t.Fatalf("download %q", got)
				}
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
				if scenario == "upstream_unavailable" {
					// Preserve the existing producer distinction: gateway proxy
					// failures deny upstream_down, direct proxy failures stay allowed.
					if e.Via == "portal" && (e.Decision != "denied" || e.Reason != "upstream_down") || e.Via == "" && e.Decision != "allowed" {
						t.Fatalf("existing failure decision changed: %+v", e)
					}
				}
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
			if scenario == "denied" {
				if p.Denied != 2 || p.LastAllowedAt != nil {
					t.Fatalf("denied last allowed: %+v", p)
				}
			} else if scenario == "upstream_unavailable" {
				if p.Allowed != 1 || p.Denied != 1 || p.LastAllowedAt == nil {
					t.Fatalf("transport failure decisions: %+v", p)
				}
			} else if p.Allowed != 2 || p.LastAllowedAt == nil {
				t.Fatalf("allowed decisions: %+v", p)
			}
			limited, err := accesslog.Query(dir, accesslog.Filter{App: "photos", Limit: 1})
			if err != nil || !limited.Truncated || !equalJSON(limited.Summary.Traffic, traffic) {
				t.Fatalf("limit changed traffic: %+v, %v", limited, err)
			}
		})
	}
}
