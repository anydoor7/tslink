package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"tailscale.com/ipn/ipnstate"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestGatewayPortalOnlyPeriodicHealthAndFreshness(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()
	f := newGatewayFixture(t, func(f *gatewayFixture) {
		if _, err := registry.Add(f.path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL, PortalOnly: true, Health: &registry.HealthConfig{Interval: "10s", Timeout: "1s"}}); err != nil {
			t.Fatal(err)
		}
	})
	f.waitRoute(t, "photos", "running")
	f.waitRoute(t, "payroll", "running")
	recorder := health.NewRecorder(filepath.Join(filepath.Dir(f.path), "health-state-fixture.json"), health.NotifierConfig{})
	now := time.Unix(0, f.now.Load())
	cycle := func() {
		f.now.Store(now.UnixNano())
		f.s.healthCycle(context.Background(), recorder, now, health.Probe)
	}
	check := func(want string, offline bool) {
		t.Helper()
		for _, r := range f.s.portalRun.routeStates() {
			if r.App == "photos" {
				if r.Health != want || (r.OfflineSince != nil) != offline || r.HealthChecked == nil {
					t.Fatalf("health=%+v want=%s offline=%v", r, want, offline)
				}
				return
			}
		}
		t.Fatal("missing route")
	}
	cycle()
	check(health.Healthy, false)
	if calls.Load() != 1 {
		t.Fatal("no-traffic periodic control", calls.Load())
	}
	cycle()
	if calls.Load() != 1 {
		t.Fatal("interval ignored", calls.Load())
	}
	fail.Store(true)
	for i := 0; i < 3; i++ {
		now = now.Add(10 * time.Second)
		cycle()
	}
	check(health.Down, true)
	now = now.Add(22 * time.Second)
	f.now.Store(now.UnixNano())
	check(health.Unknown, true)
	fail.Store(false)
	cycle()
	check(health.Healthy, false)
}

func TestGatewayRemoteActivationRequiresObservedTagAndTailnet(t *testing.T) {
	for _, tt := range []struct {
		name, target, reason string
		tagged               bool
	}{
		{"untagged", "https://remote.tailnet.ts.net", "remote_upstream_requires_tag", false},
		{"outside-tailnet", "https://remote.foreign.ts.net", "remote_upstream_outside_tailnet", true},
		{"not-peer", "https://absent.tailnet.ts.net", "remote_upstream_not_peer", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var st atomic.Pointer[ipnstate.Status]
			st.Store(taggedGatewayStatus(tt.tagged))
			var dials atomic.Int64
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				original := f.node.localClient.Transport
				f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/status") {
						return remoteLocalClient(&st, nil).Transport.RoundTrip(r)
					}
					return original.RoundTrip(r)
				})
				f.node.dial = func(context.Context, string, string) (net.Conn, error) {
					dials.Add(1)
					return nil, errors.New("must not dial")
				}
				reg, err := registry.Load(f.path)
				if err != nil {
					t.Fatal(err)
				}
				svc := reg.Services[0]
				svc.Upstream = "tailnet"
				svc.Target = tt.target
				if _, err := registry.Add(f.path, svc); err != nil {
					t.Fatal(err)
				}
			})
			state := f.waitRoute(t, "photos", "failed")
			if state.Reason != tt.reason || f.node.addr("8443") != "" || dials.Load() != 0 {
				t.Fatalf("remote activation boundary: %+v dials=%d", state, dials.Load())
			}
			f.waitRoute(t, "payroll", "running")
		})
	}
}
