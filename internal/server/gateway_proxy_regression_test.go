package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

func gatewayAddBackend(t *testing.T, f *gatewayFixture, name string, handler http.Handler) int {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	if _, err := registry.Add(f.path, registry.Service{Name: name, Type: registry.TypeProxy, Target: upstream.URL, PortalOnly: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ChangePerson(f.path, "alice", []string{"photos", name}, nil, true, true); err != nil {
		t.Fatal(err)
	}
	f.s.syncPortal(context.Background(), f.config)
	return f.waitRoute(t, name, "running").Port
}

func TestGatewayClientCancellationPreservesHealth(t *testing.T) {
	f := newGatewayFixture(t, nil)
	entered := make(chan struct{})
	port := gatewayAddBackend(t, f, "slow", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(entered)
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "healthy")
	}))
	c := f.client(t)
	if code, _, _ := gatewayGet(t, c, strconv.Itoa(port), "/", nil); code != 200 {
		t.Fatal(code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://home.tailnet.ts.net:"+strconv.Itoa(port)+"/slow", nil)
	finished := make(chan error, 1)
	go func() {
		resp, err := c.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		finished <- err
	}()
	testwait.Recv(t, entered, "upstream entered")
	cancel()
	if err := testwait.Recv(t, finished, "cancelled client"); err == nil {
		t.Fatal("client cancellation did not abort request")
	}
	deadline := time.Now().Add(testwait.Budget(t))
	for time.Now().Before(deadline) {
		for _, e := range f.events.all() {
			if e.App == "slow" && e.Path == "/slow" {
				if e.Reason != "client_cancelled" {
					t.Fatalf("cancel reason=%s", e.Reason)
				}
				state := f.waitRoute(t, "slow", "running")
				if state.Health != "healthy" || state.OfflineSince != nil {
					t.Fatalf("cancel marked healthy route down: %+v", state)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cancelled request event missing")
}

func TestGatewayLongPollUsesServiceLimits(t *testing.T) {
	f := newGatewayFixture(t, nil)
	port := gatewayAddBackend(t, f, "long-poll", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(31 * time.Second):
			_, _ = io.WriteString(w, "long poll complete")
		case <-r.Context().Done():
		}
	}))
	c := f.client(t)
	c.Timeout = 45 * time.Second
	code, body, _ := gatewayGet(t, c, strconv.Itoa(port), "/", nil)
	if code != 200 || body != "long poll complete" {
		t.Fatalf("long poll: %d %s", code, body)
	}
}

func TestGatewayRefreshDeclarativeSyntax(t *testing.T) {
	target, _ := url.Parse("http://127.0.0.1:3000")
	for _, value := range []string{
		`0; url=http://127.0.0.1:3000/a`, `0;url=http://127.0.0.1:3000/a`,
		`0, url=http://127.0.0.1:3000/a`, `0; http://127.0.0.1:3000/a`,
		`0;URL = 'http://127.0.0.1:3000/a'`, ` .5 , "http://127.0.0.1:3000/a" ignored`,
		`0 http://127.0.0.1:3000/a`,
	} {
		t.Run(value, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Refresh": []string{value}}}
			rewriteGatewayResponse(resp, target, "home.tailnet.ts.net:8443")
			want := strings.Replace(value, "http://127.0.0.1:3000", "https://home.tailnet.ts.net:8443", 1)
			if got := resp.Header.Get("Refresh"); got != want {
				t.Fatalf("refresh %q want %q", got, want)
			}
		})
	}
	for _, value := range []string{`bogus; http://127.0.0.1:3000/a`, `0x; http://127.0.0.1:3000/a`, `0; https://accounts.example/login`, `0`, `0; url='https://foreign.example/'`} {
		if got := rewriteGatewayRefresh(value, target, "home.tailnet.ts.net:8443"); got != value {
			t.Fatalf("foreign or invalid refresh changed: %q -> %q", value, got)
		}
	}
}
