package health

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestWave1HealthRequestLimits(t *testing.T) {
	var calls atomic.Int32
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.ContentLength > 0 {
			t.Errorf("probe unexpectedly uploaded a body: %s %d", r.Method, r.ContentLength)
		}
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, "ready")
	}))
	defer app.Close()
	svc := registry.Service{Type: registry.TypeProxy, Target: app.URL, Health: &registry.HealthConfig{Timeout: "200ms", BodyContains: "ready"}}
	// F8 limits incoming request bodies and reads, never backend responses.
	for _, limits := range []*registry.RequestLimits{nil, {MaxBody: "1B", HeaderTimeout: "1ms", ReadTimeout: "1ms", IdleTimeout: "1ms"}, {MaxBody: "unlimited", UnlimitedAck: true}} {
		svc.RequestLimits = limits
		if code := Probe(context.Background(), svc); code != "" {
			t.Fatalf("valid empty GET rejected: %s", code)
		}
	}
	for _, limits := range []*registry.RequestLimits{{MaxBody: "unlimited"}, {MaxBody: "0B"}, {ReadTimeout: "0"}} {
		svc.RequestLimits = limits
		before := calls.Load()
		if code := Probe(context.Background(), svc); code != "health_request_limits_invalid" {
			t.Errorf("invalid limits probed: %s", code)
		}
		if calls.Load() != before {
			t.Error("invalid limit configuration contacted the backend")
		}
	}
}
