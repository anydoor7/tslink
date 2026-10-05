//go:build enrollmenttest

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/anydoor7/tslink/internal/server"
	"github.com/anydoor7/tslink/internal/testwait"
	"tailscale.com/ipn/ipnstate"
)

type enrollmentRoundTripper func(*http.Request) (*http.Response, error)

func (f enrollmentRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPendingOfferSurvivesOtherNodes(t *testing.T) {
	for _, kind := range []string{"single-control", "portal-first-complete", "portal-first-cancelled", "app-first-complete", "app-first-cancelled", "three-pending"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			path := filepath.Join(dir, "auth-handoff.json")
			serveSaveAuthHandoffFn, serveLoadAuthHandoffFn, serveRemoveAuthHandoffFn = saveAuthHandoff, loadAuthHandoff, removeAuthHandoff
			m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
			m.check = func(ctx context.Context, m *portalHandoffRunner) error {
				pending := make(chan string, 20)
				callback := func(ctx context.Context, e server.AuthHandoff) error {
					err := m.authHandoff(ctx, e)
					if err == nil && e.State == "pending" {
						pending <- e.Service
					}
					return err
				}
				type node struct {
					name     string
					running  atomic.Bool
					requests atomic.Int64
					done     chan error
					cancel   context.CancelFunc
				}
				start := func(name string) *node {
					n := &node{name: name, done: make(chan error, 1)}
					api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						n.requests.Add(1)
						s := ipnstate.Status{BackendState: "NeedsLogin", AuthURL: "https://login.example.invalid/" + name}
						if n.running.Load() {
							s.BackendState = "Running"
							s.TailscaleIPs = []netip.Addr{netip.MustParseAddr("100.64.0.8")}
						}
						_ = json.NewEncoder(w).Encode(s)
					}))
					endpoint, _ := url.Parse(api.URL)
					lc := &server.LocalClient{OmitAuth: true, Transport: enrollmentRoundTripper(func(r *http.Request) (*http.Response, error) {
						c := r.Clone(r.Context())
						u := *c.URL
						u.Scheme = endpoint.Scheme
						u.Host = endpoint.Host
						c.URL = &u
						c.Host = endpoint.Host
						return http.DefaultTransport.RoundTrip(c)
					})}
					nctx, cancel := context.WithCancel(ctx)
					n.cancel = cancel
					joined := make(chan struct{})
					go func() { defer close(joined); n.done <- server.WaitForEnrollmentTest(nctx, callback, name, lc) }()
					deferCleanup := func() { cancel(); <-joined; api.Close() }
					t.Cleanup(deferCleanup)
					if got := testwait.Recv(t, pending, "pending enrollment published"); got != name {
						t.Fatalf("published %s want %s", got, name)
					}
					records, err := loadAuthHandoffs(path)
					found := false
					for _, record := range records {
						if record.Service == name && record.AuthURL == "https://login.example.invalid/"+name {
							found = true
						}
					}
					if err != nil || !found {
						t.Fatalf("positive control %s records=%+v err=%v", name, records, err)
					}
					return n
				}
				first := "home"
				if kind == "app-first-complete" || kind == "app-first-cancelled" {
					first = "photos"
				}
				survivor := start(first)
				if kind == "single-control" {
					survivor.running.Store(true)
					if err := <-survivor.done; err != nil {
						return err
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						return fmt.Errorf("completed singleton remains: %v", err)
					}
					return nil
				}
				second := "photos"
				if first == "photos" {
					second = "home"
				}
				finish := func(n *node) {
					if kind == "portal-first-cancelled" || kind == "app-first-cancelled" {
						n.cancel()
					} else {
						n.running.Store(true)
					}
					if err := testwait.Recv(t, n.done, "terminal enrollment joined"); err != nil && !((kind == "portal-first-cancelled" || kind == "app-first-cancelled") && errors.Is(err, context.Canceled)) {
						t.Fatalf("terminal: %v", err)
					}
				}
				assertSurvivor := func(stage string) {
					calls := survivor.requests.Load()
					testwait.Until(t, "pending poller kept polling (3 more requests)", func() bool { return survivor.requests.Load() >= calls+3 })
					record, err := loadAuthHandoff(path)
					t.Logf("%s: pending=%s polls=%d file_service=%q load_err=%v", stage, first, survivor.requests.Load(), record.Service, err)
					if err != nil || record.Service != first {
						t.Errorf("pending %s login URL lost after %s", first, stage)
					}
				}
				if kind == "three-pending" {
					photos, docs := start("photos"), start("docs")
					records, err := loadAuthHandoffs(path)
					if err != nil || len(records) != 3 {
						t.Fatalf("three pending entries not preserved: %+v %v", records, err)
					}
					finish(docs)
					records, err = loadAuthHandoffs(path)
					if err != nil || len(records) != 2 || records[0].Service != "home" || records[1].Service != "photos" {
						t.Errorf("completing docs must preserve both pending nodes: %+v %v", records, err)
					}
					record, err := loadAuthHandoff(path)
					if err != nil || (record.Service != "home" && record.Service != "photos") {
						t.Errorf("three pending: completing docs lost both other offers: record=%+v err=%v", record, err)
					}
					finish(photos)
					assertSurvivor("two simultaneously pending apps completed")
				} else {
					finish(start(second))
					assertSurvivor(second + " terminal")
				}
				if first == "home" && kind != "three-pending" {
					finish(start("docs"))
					assertSurvivor("second app terminal")
				}
				if err := m.ready(); err != nil {
					return err
				}
				assertSurvivor("daemon readiness")
				survivor.cancel()
				if err := <-survivor.done; !errors.Is(err, context.Canceled) {
					return fmt.Errorf("survivor cancel: %v", err)
				}
				return nil
			}
			serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
			if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path, ReadyPath: filepath.Join(dir, "ready")}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
