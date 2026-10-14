package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

// A visitor whose request is canceled while the gateway identifies it leaves
// no denied event; a WhoIs failure with the visitor still connected is still
// whois_failed (TestGatewayWhoIsTimeouts).
func TestGatewayClientCancelDuringWhoIsIsNotADenial(t *testing.T) {
	f := newGatewayFixture(t, nil)
	port := strconv.Itoa(f.waitRoute(t, "photos", "running").Port)
	c := f.client(t)
	// Admit the connection first; admission is per connection, the gate's
	// identification is per request.
	if code, _, _ := gatewayGet(t, c, port, "/first", nil); code != 200 {
		t.Fatal(code)
	}
	f.stallWho.Store(true)
	before := f.calls.Load()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://home.tailnet.ts.net:"+port+"/canceled", nil)
	result := make(chan error, 1)
	go func() {
		resp, err := c.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	testwait.Until(t, "gate WhoIs started", func() bool { return f.calls.Load() > before })
	cancel()
	if err := testwait.Recv(t, result, "client cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("request was not canceled: %v", err)
	}
	f.stallWho.Store(false)
	if code, _, _ := gatewayGet(t, c, port, "/after", nil); code != 200 {
		t.Fatal(code)
	}
	deadline := time.Now().Add(testwait.Budget(t))
	for time.Now().Before(deadline) {
		seen := map[string]string{}
		for _, e := range f.events.all() {
			if e.App == "photos" {
				seen[e.Path] = e.Decision
			}
		}
		if seen["/after"] == "allowed" {
			if decision, ok := seen["/canceled"]; ok {
				t.Fatalf("canceled identification recorded as %s", decision)
			}
			if seen["/first"] != "allowed" {
				t.Fatalf("control events %v", seen)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("control event missing")
}
