package health

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// closedAddress returns a loopback address whose listener is already closed,
// so a dial is refused instead of timing out.
func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func statusBackend(t *testing.T, status int, body string) string {
	t.Helper()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status >= 300 && status < 400 {
			w.Header().Set("Location", "/elsewhere?token=private")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(app.Close)
	return app.URL
}

// TestProbeClassificationStateAndAlerts runs four consecutive real checks per
// backend shape through Probe, ResultOf and the alert recorder.
func TestProbeClassificationStateAndAlerts(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(slow.Close)
	unreachable := []string{Degraded, Degraded, Down, Down}
	answering := []string{Degraded, Degraded, Degraded, Degraded}
	healthy := []string{Healthy, Healthy, Healthy, Healthy}
	for _, tc := range []struct {
		name   string
		svc    registry.Service
		code   string
		status int
		states []string
		down   bool
	}{
		{"307 redirect", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 307, "")}, "", 307, healthy, false},
		{"302 redirect", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 302, "")}, "", 302, healthy, false},
		{"200", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 200, "ok")}, "", 200, healthy, false},
		{"401", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 401, "")}, "health_status_mismatch", 401, answering, false},
		{"404", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 404, "")}, "health_status_mismatch", 404, answering, false},
		{"body mismatch", registry.Service{Type: registry.TypeProxy, Target: statusBackend(t, 200, "starting"), Health: &registry.HealthConfig{BodyContains: "ready"}}, "health_body_mismatch", 200, answering, false},
		{"refused", registry.Service{Type: registry.TypeProxy, Target: "http://" + closedAddress(t)}, "health_request_failed", 0, unreachable, true},
		{"timeout", registry.Service{Type: registry.TypeProxy, Target: slow.URL, Health: &registry.HealthConfig{Timeout: "100ms"}}, "health_timeout", 0, unreachable, true},
		{"tcp failure", registry.Service{Type: registry.TypeTCP, Target: closedAddress(t)}, "health_tcp_failed", 0, unreachable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{})
			var events []Event
			for i, want := range tc.states {
				ctx, status := WithResponseStatus(context.Background())
				code := Probe(ctx, tc.svc)
				if code != tc.code || status.Code() != tc.status {
					t.Fatalf("check %d: code=%q status=%d, want %q %d", i, code, status.Code(), tc.code, tc.status)
				}
				now = now.Add(time.Minute)
				h := ResultOf(r.Previous("app", "id"), tc.svc.Type, Outcome{Error: code, StatusCode: status.Code()}, now)
				if h.State != want {
					t.Fatalf("check %d: state=%s want %s (%+v)", i, h.State, want, h)
				}
				wantFailures := 0
				if code != "" {
					wantFailures = i + 1
				}
				if h.ConsecutiveFailures != wantFailures || h.LastError != code || h.StatusCode != tc.status {
					t.Fatalf("check %d: %+v", i, h)
				}
				b, err := json.Marshal(h)
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(string(b), `"status_code"`); got != (tc.status != 0) {
					t.Fatalf("check %d: status_code presence %v in %s", i, got, b)
				}
				if strings.Contains(string(b), "elsewhere") || strings.Contains(string(b), "private") || strings.Contains(string(b), "starting") {
					t.Fatalf("redirect target or body recorded: %s", b)
				}
				e := r.ObserveHealth("app", "id", h, now)
				r.Commit(context.Background(), e, now)
				events = append(events, e...)
			}
			if tc.down {
				if len(events) != 1 || events[0].Kind != "app_down" || events[0].Health.LastError != tc.code {
					t.Fatalf("unreachable backend must alert exactly once: %+v", events)
				}
			} else if len(events) != 0 {
				t.Fatalf("answering backend must not alert: %+v", events)
			}
		})
	}
}

func TestResultClassTransitions(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{})
	step := func(code string, status int) (State, []Event) {
		t.Helper()
		now = now.Add(time.Minute)
		h := ResultOf(r.Previous("app", "id"), registry.TypeProxy, Outcome{Error: code, StatusCode: status}, now)
		e := r.ObserveHealth("app", "id", h, now)
		r.Commit(context.Background(), e, now)
		return h, e
	}
	for i := 0; i < 3; i++ {
		step("health_request_failed", 0)
	}
	if h := r.Previous("app", "id"); h.State != Down {
		t.Fatalf("precondition: %+v", h)
	}
	// Down, then the backend answers 401: degraded, a new run, no recovery.
	h, e := step("health_status_mismatch", 401)
	if h.State != Degraded || h.ConsecutiveFailures != 1 || h.StatusCode != 401 || len(e) != 0 {
		t.Fatalf("down to mismatch: %+v %+v", h, e)
	}
	for i := 0; i < 5; i++ {
		h, e = step("health_status_mismatch", 401)
	}
	if h.State != Degraded || h.ConsecutiveFailures != 6 || len(e) != 0 {
		t.Fatalf("long mismatch run: %+v %+v", h, e)
	}
	// A mismatch run never shortens the unreachable path.
	for i, want := range []string{Degraded, Degraded, Down} {
		h, e = step("health_timeout", 0)
		if h.State != want || h.ConsecutiveFailures != i+1 || h.StatusCode != 0 {
			t.Fatalf("unreachable after mismatch %d: %+v", i, h)
		}
		// The service never recovered, so the first down is not repeated.
		if len(e) != 0 {
			t.Fatalf("unexpected event %+v", e)
		}
	}
	h, e = step("", 200)
	if h.State != Healthy || h.ConsecutiveFailures != 0 || len(e) != 1 || e[0].Kind != "app_recovered" {
		t.Fatalf("recovery: %+v %+v", h, e)
	}
	// Mismatch then unreachable from healthy: three unreachable checks alert.
	step("health_body_mismatch", 200)
	step("health_body_mismatch", 200)
	var downs []Event
	for i := 0; i < 3; i++ {
		_, e = step("health_request_failed", 0)
		downs = append(downs, e...)
	}
	if len(downs) != 1 || downs[0].Kind != "app_down" {
		t.Fatalf("unreachable after mismatch must alert on the third check: %+v", downs)
	}
}

// A state persisted by an earlier build (down on a mismatch) continues as a
// degraded mismatch run and emits nothing.
func TestResultContinuesPersistedMismatchRun(t *testing.T) {
	checked := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	previous := State{State: Down, Kind: registry.TypeProxy, LastChecked: &checked, LastError: "health_status_mismatch", ConsecutiveFailures: 2640}
	h := ResultOf(previous, registry.TypeProxy, Outcome{Error: "health_status_mismatch", StatusCode: 404}, checked.Add(time.Minute))
	if h.State != Degraded || h.ConsecutiveFailures != 2641 || h.StatusCode != 404 {
		t.Fatalf("%+v", h)
	}
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{})
	r.ObserveHealth("app", "id", previous, checked)
	if e := r.ObserveHealth("app", "id", h, checked.Add(time.Minute)); len(e) != 0 {
		t.Fatalf("degraded mismatch after a persisted down emitted %+v", e)
	}
}

func TestUnexpectedResponseClasses(t *testing.T) {
	for _, code := range []string{"health_status_mismatch", "health_body_mismatch"} {
		if !UnexpectedResponse(code) {
			t.Fatal(code)
		}
	}
	for _, code := range []string{"", "health_request_failed", "health_timeout", "health_tcp_failed", "health_body_read_failed", "health_file_unavailable", "health_file_invalid", "health_config_invalid", "remote_upstream_unavailable"} {
		if UnexpectedResponse(code) {
			t.Fatal(code)
		}
	}
	// A timed-out outcome never carries a late status.
	if h := ResultOf(State{}, registry.TypeProxy, Outcome{Error: "health_timeout", StatusCode: 200}, time.Now()); h.StatusCode != 0 {
		t.Fatalf("%+v", h)
	}
}

func TestProbeDefaultAndExplicitStatusRanges(t *testing.T) {
	redirect := statusBackend(t, 302, "")
	forbidden := statusBackend(t, 403, "")
	for _, tc := range []struct {
		target string
		health *registry.HealthConfig
		code   string
	}{
		{redirect, nil, ""},
		{redirect, &registry.HealthConfig{Path: "/"}, ""},
		{redirect, &registry.HealthConfig{StatusMin: 200, StatusMax: 299}, "health_status_mismatch"},
		{redirect, &registry.HealthConfig{StatusMin: 200}, "health_status_mismatch"},
		{redirect, &registry.HealthConfig{StatusMax: 401}, ""},
		{forbidden, nil, "health_status_mismatch"},
		{forbidden, &registry.HealthConfig{StatusMax: 403}, ""},
	} {
		if code := Probe(context.Background(), registry.Service{Type: registry.TypeProxy, Target: tc.target, Health: tc.health}); code != tc.code {
			t.Fatalf("%s %+v: %q want %q", tc.target, tc.health, code, tc.code)
		}
	}
}
