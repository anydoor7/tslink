package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

type gatewayBlockedBody struct {
	io.Reader
	entered, release chan struct{}
	once             atomic.Bool
}

func (b *gatewayBlockedBody) Read(p []byte) (int, error) {
	if b.once.CompareAndSwap(false, true) {
		close(b.entered)
		<-b.release
	}
	return b.Reader.Read(p)
}
func (b *gatewayBlockedBody) Close() error { return nil }

// Isolate a regressed deadlock so the test reports the lock-order failure,
// rather than hanging the entire suite in fixture cleanup. The slow response
// is Status on the old admission path, WhoIs after the redundant check is gone.
func TestGatewayRouteCloseLockOrder(t *testing.T) {
	if os.Getenv("TSLINK_TEST_ROUTE_LOCK_CHILD") != "1" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestGatewayRouteCloseLockOrder$", "-test.v", "-test.timeout=20s")
		cmd.Env = append(os.Environ(), "TSLINK_TEST_ROUTE_LOCK_CHILD=1")
		out, err := cmd.CombinedOutput()
		t.Log(string(out))
		if err != nil {
			t.Fatalf("route teardown/snapshot regression: %v", err)
		}
		return
	}
	for _, mode := range []string{"reconfigure", "close-all"} {
		t.Run(mode, func(t *testing.T) {
			var armed atomic.Pointer[gatewayBlockedBody]
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				original := f.node.localClient.Transport
				f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					var res *http.Response
					var err error
					if strings.HasSuffix(r.URL.Path, "/status") {
						data, _ := json.Marshal(taggedGatewayStatus(true))
						res = &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}
					} else {
						res, err = original.RoundTrip(r)
					}
					if res != nil && err == nil {
						if b := armed.Swap(nil); b != nil {
							data, e := io.ReadAll(res.Body)
							res.Body.Close()
							if e != nil {
								return nil, e
							}
							b.Reader = strings.NewReader(string(data))
							res.Body = b
						}
					}
					return res, err
				})
				f.node.dial = func(context.Context, string, string) (net.Conn, error) { return nil, io.EOF }
				reg, err := registry.Load(f.path)
				if err != nil {
					t.Fatal(err)
				}
				svc := reg.Services[0]
				svc.Upstream, svc.Target = "tailnet", "https://home.tailnet.ts.net"
				if _, err := registry.Add(f.path, svc); err != nil {
					t.Fatal(err)
				}
			})
			f.waitRoute(t, "photos", "running")
			run := f.s.portalRun
			run.routesMu.Lock()
			route := run.routes["photos"]
			run.routesMu.Unlock()
			body := &gatewayBlockedBody{entered: make(chan struct{}), release: make(chan struct{})}
			armed.Store(body)
			conn, err := net.Dial("tcp", f.node.addr("8443"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			testwait.Recv(t, body.entered, "admission inside LocalAPI response")
			if err := registry.SetPortalRoute(context.Background(), f.path, "photos", &registry.PortalRoute{Port: 8443, AllowCrossOrigin: true}); err != nil {
				t.Fatal(err)
			}
			closed := make(chan struct{})
			go func() {
				if mode == "close-all" {
					run.closeRoutes()
				} else {
					f.s.syncGatewayRoutes(run)
				}
				close(closed)
			}()
			testwait.Recv(t, route.ctx.Done(), "teardown started")
			snapshot := make(chan struct{})
			go func() {
				f.s.mu.Lock()
				f.s.writeRuntimeSnapshotLocked(f.s.lastRegistryFingerprint, f.s.lastSnapshotComplete)
				f.s.mu.Unlock()
				close(snapshot)
			}()
			select {
			case <-snapshot:
			case <-time.After(3 * time.Second):
				fmt.Fprintln(os.Stderr, "snapshot blocked behind route teardown while admission waits on LocalAPI")
				os.Exit(2)
			}
			close(body.release)
			testwait.Recv(t, closed, "route teardown completed")
		})
	}
}

func TestGatewayAdmittedCloseDoesNotLogDrop(t *testing.T) {
	f := newGatewayFixture(t, nil)
	f.who.Store(whoIsUser("owner", "owner"))
	entered, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	gate := &gatewayGate{path: f.path, svc: registry.Service{Name: "home", PeopleScoped: true}, lc: f.node.localClient, now: func() time.Time {
		if once.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return time.Unix(0, f.now.Load())
	}, writer: f.s.accessWriter}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener := newGatewayListener(context.Background(), ln, gate)
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	testwait.Recv(t, entered, "admitted owner policy")
	closed := make(chan struct{})
	go func() { listener.Close(); close(closed) }()
	testwait.Recv(t, listener.ctx.Done(), "listener cancellation")
	close(release)
	testwait.Recv(t, closed, "admitted connection closed")
	if gate.dropped.Load() != 0 {
		t.Fatalf("admitted teardown logged conn_gate_drop: %d", gate.dropped.Load())
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if n, err := c.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("connection not closed cleanly: %d %v", n, err)
	}
}
