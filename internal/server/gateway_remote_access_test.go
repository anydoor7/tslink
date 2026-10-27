package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/ipn/ipnstate"
)

// The earlier gate and the upstream dialer both enforce the remote-route
// policy, each from its own LocalAPI status read. A request can pass the gate
// (lean status: tagged, in the tailnet) and be refused by the dial a moment
// later (full status: tag removed, foreign suffix, peer gone, status failing).
// That refusal is the same restriction the gate records as denied, and it must
// not turn into an allowed request that advances last_allowed_at. A transport
// failure of the remote app itself, by contrast, is an authorized request that
// failed downstream and stays allowed with a 502 and an error class.
func TestGatewayRemotePolicyRefusalAfterGateStaysDenied(t *testing.T) {
	for _, tc := range []struct {
		name string
		// full is the status the dial sees; the gate always sees a tagged,
		// in-tailnet gateway.
		full       func(*ipnstate.Status)
		statusFail bool
		dialErr    error
		// Expected access event and visitor response.
		decision, reason, errorClass string
		status                       int
	}{
		{name: "requires_tag", full: func(s *ipnstate.Status) { *s = *taggedGatewayStatus(false) }, decision: "denied", reason: "remote_upstream_requires_tag", status: 503},
		{name: "outside_tailnet", full: func(s *ipnstate.Status) { s.CurrentTailnet.MagicDNSSuffix = "foreign.ts.net" }, decision: "denied", reason: "remote_upstream_outside_tailnet", status: 503},
		{name: "not_peer", full: func(s *ipnstate.Status) { s.Peer = nil }, decision: "denied", reason: "remote_upstream_not_peer", status: 503},
		{name: "status_unavailable", statusFail: true, decision: "denied", reason: "remote_upstream_unavailable", status: 503},
		// Positive control: the gate and the dial both pass, then the remote
		// app's transport fails. Same route, same request, opposite outcome.
		{name: "transport_failure", dialErr: errors.New("netstack offline"), decision: "allowed", reason: "granted", errorClass: "other", status: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lean, full atomic.Pointer[ipnstate.Status]
			lean.Store(taggedGatewayStatus(true))
			full.Store(taggedGatewayStatus(true))
			var failFull atomic.Bool
			var leanReads, fullReads, dials atomic.Int64
			var store *accesslog.Store
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				var err error
				store, err = accesslog.New(filepath.Dir(f.path), accesslog.Options{}, func() time.Time { return time.Unix(0, f.now.Load()) })
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.s.closePortal(); drainAccess(t, store) })
				f.s.accessWriter = store
				original := f.node.localClient.Transport
				f.node.localClient.Transport = roundTripperFunc(func(r *http.Request) (*http.Response, error) {
					if !strings.HasSuffix(r.URL.Path, "/status") {
						return original.RoundTrip(r)
					}
					if r.URL.Query().Get("peers") == "false" {
						leanReads.Add(1)
						return remoteLocalClient(&lean, nil).Transport.RoundTrip(r)
					}
					fullReads.Add(1)
					if failFull.Load() {
						return nil, errors.New("status unavailable")
					}
					return remoteLocalClient(&full, nil).Transport.RoundTrip(r)
				})
				f.node.dial = func(context.Context, string, string) (net.Conn, error) {
					dials.Add(1)
					return nil, tc.dialErr
				}
				reg, err := registry.Load(f.path)
				if err != nil {
					t.Fatal(err)
				}
				svc := reg.Services[0]
				svc.Upstream = "tailnet"
				svc.Target = "https://home.tailnet.ts.net"
				if _, err := registry.Add(f.path, svc); err != nil {
					t.Fatal(err)
				}
			})
			f.waitRoute(t, "photos", "running")
			// The route is active. From here the dial sees the refusal.
			refused := taggedGatewayStatus(true)
			if tc.full != nil {
				tc.full(refused)
			}
			full.Store(refused)
			failFull.Store(tc.statusFail)
			beforeLean, beforeFull, beforeDials := leanReads.Load(), fullReads.Load(), dials.Load()

			code, body, _ := gatewayGet(t, f.client(t), "8443", "/", nil)
			if code != tc.status {
				t.Fatalf("visitor status %d, want %d: %s", code, tc.status, body)
			}
			if tc.decision == "denied" && !strings.Contains(body, tc.reason) {
				t.Fatalf("visitor body does not carry %s: %s", tc.reason, body)
			}
			if leanReads.Load() == beforeLean || fullReads.Load() == beforeFull {
				t.Fatalf("the request did not pass the gate and reach the dial: lean %d->%d full %d->%d", beforeLean, leanReads.Load(), beforeFull, fullReads.Load())
			}
			wantDials := beforeDials
			if tc.dialErr != nil {
				wantDials++
			}
			if dials.Load() != wantDials {
				t.Fatalf("netstack dials %d->%d, want %d", beforeDials, dials.Load(), wantDials)
			}

			dir := filepath.Dir(f.path)
			testwait.Until(t, "request recorded", func() bool {
				r, err := accesslog.Query(dir, accesslog.Filter{App: "photos"})
				return err == nil && r.Summary.Traffic.HTTP.Requests == 1
			})
			f.s.closePortal()
			drainAccess(t, store)
			r, err := accesslog.Query(dir, accesslog.Filter{App: "photos", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			var httpEvents []accesslog.Event
			for _, e := range r.Events {
				if e.Kind == "http" {
					httpEvents = append(httpEvents, e)
				}
			}
			if len(httpEvents) != 1 {
				t.Fatalf("events: %+v", r.Events)
			}
			e := httpEvents[0]
			if e.Via != "portal" || e.Status != tc.status || e.Decision != tc.decision || e.Reason != tc.reason || e.ErrorClass != tc.errorClass {
				t.Fatalf("event %+v, want status=%d decision=%s reason=%s class=%q", e, tc.status, tc.decision, tc.reason, tc.errorClass)
			}
			traffic := r.Summary.Traffic
			if len(traffic.PeopleApps) != 1 {
				t.Fatalf("traffic: %+v", traffic)
			}
			p := traffic.PeopleApps[0]
			if tc.decision == "denied" {
				if p.Denied != 1 || p.Allowed != 0 || p.LastAllowedAt != nil {
					t.Fatalf("a policy refusal must not count as allowed or advance last_allowed_at: %+v", p)
				}
				return
			}
			if p.Allowed != 1 || p.Denied != 0 || p.LastAllowedAt == nil {
				t.Fatalf("a transport failure of an authorized request stays allowed: %+v", p)
			}
		})
	}
}
