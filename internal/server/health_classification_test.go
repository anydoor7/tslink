package server

import (
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

// The monitor records what each real backend answered, never escalates an
// answering backend to down, and never rewrites a registry that has no health
// settings.
func TestHealthCycleRecordsStatusAndClassifiesRealBackends(t *testing.T) {
	s, dir := healthTestServer(t)
	answer := func(status int) string {
		app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if status/100 == 3 {
				w.Header().Set("Location", "/panel")
			}
			w.WriteHeader(status)
		}))
		t.Cleanup(app.Close)
		return app.URL
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + ln.Addr().String()
	ln.Close()
	path := filepath.Join(dir, "registry.json")
	services := []registry.Service{
		{Name: "redirects", Type: registry.TypeProxy, Target: answer(307)},
		{Name: "rootless", Type: registry.TypeProxy, Target: answer(404)},
		{Name: "offline", Type: registry.TypeProxy, Target: refused},
	}
	for _, svc := range services {
		if _, err := registry.Add(path, svc); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	identities := map[string]string{}
	for _, svc := range reg.Services {
		if svc.Health != nil {
			t.Fatalf("fixture must have no health settings: %+v", svc)
		}
		identities[svc.Name] = healthIdentity(svc)
		s.nodes[svc.Name] = &ServiceNode{service: svc}
	}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i)*time.Minute), health.Probe)
	}
	snapshot, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]health.State{
		"redirects": {State: health.Healthy, StatusCode: 307},
		"rootless":  {State: health.Degraded, LastError: "health_status_mismatch", ConsecutiveFailures: 4, StatusCode: 404},
		"offline":   {State: health.Down, LastError: "health_request_failed", ConsecutiveFailures: 4},
	}
	for _, svc := range snapshot.Services {
		w := want[svc.Name]
		h := svc.Health
		if h.State != w.State || h.LastError != w.LastError || h.ConsecutiveFailures != w.ConsecutiveFailures || h.StatusCode != w.StatusCode {
			t.Errorf("%s: %+v want %+v", svc.Name, h, w)
		}
		delete(want, svc.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing services %v", want)
	}
	var downs []string
	for _, e := range r.State.Events {
		if e.Kind == "app_down" {
			downs = append(downs, e.Service)
		}
	}
	if len(downs) != 1 || downs[0] != "offline" {
		t.Fatalf("app_down must be reserved for the unreachable backend: %v", downs)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("health monitoring rewrote the registry")
	}
	reg, err = registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if identities[svc.Name] != healthIdentity(svc) {
			t.Fatalf("%s health identity changed", svc.Name)
		}
	}
}
