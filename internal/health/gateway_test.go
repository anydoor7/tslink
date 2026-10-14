package health

import (
	"context"
	"github.com/anydoor7/tslink/internal/registry"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRemoteProbeWithoutGatewayNeverUsesHostNetwork(t *testing.T) {
	var hits atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer backend.Close()
	svc := registry.Service{Name: "remote", Type: registry.TypeProxy, Target: backend.URL, Upstream: "tailnet"}
	if code := Probe(context.Background(), svc); code != "remote_upstream_unavailable" || hits.Load() != 0 {
		t.Fatal(code, hits.Load())
	}
	svc.Upstream = ""
	if code := Probe(context.Background(), svc); code != "" || hits.Load() != 1 {
		t.Fatal("control", code, hits.Load())
	}
}
