package server

// Authorization regressions use fake nodes and scratch durable identity.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestReloadWithdrawsRevokedPrivateAccessOnIdentityFailure(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(fmt.Sprintf("damaged_identity=%v", damaged), func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("private report"), 0600); err != nil {
				t.Fatal(err)
			}
			lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{LoginName: "bob@example.com"},
				Node:        &tailcfg.Node{ComputedName: "bob-device"},
			}, nil)
			oldFactory := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
				return &fakeTSNetServer{localClient: lc}
			}
			defer func() { newTSNetServerFn = oldFactory }()
			s, err := New("test-key", "")
			if err != nil {
				t.Fatal(err)
			}
			defer s.closeAllNodes()
			svc := registry.Service{Name: "report", Type: registry.TypeFile, Path: root,
				AllowedUsers: []string{"alice@example.com", "bob@example.com"}}
			writeRegistry(t, []registry.Service{svc})
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatal(err)
			}
			request := func() int {
				node := s.nodes[svc.Name]
				if node == nil {
					return http.StatusServiceUnavailable
				}
				r := httptest.NewRequest("GET", "https://report.example/report.txt", nil)
				r.RemoteAddr = "100.64.0.10:1234"
				w := httptest.NewRecorder()
				node.httpSrv.Handler.ServeHTTP(w, r)
				return w.Code
			}
			if code := request(); code != 200 {
				t.Fatalf("positive control before revocation: %d", code)
			}
			if damaged {
				path, err := s.nodeIdentityPath(svc.Name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			svc.AllowedUsers = []string{"alice@example.com"}
			writeRegistry(t, []registry.Service{svc})
			for attempt := 1; attempt <= 2; attempt++ {
				if err := s.syncNodes(context.Background()); err != nil {
					t.Fatal(err)
				}
				code := request()
				t.Logf("reload=%d damagedIdentity=%v revoked bob HTTP=%d identityRetry=%v", attempt, damaged, code, s.identityRetryPending.Load())
				if code != http.StatusForbidden && code != http.StatusServiceUnavailable {
					t.Errorf("revoked principal still receives file after authoritative reload: HTTP %d", code)
				}
			}
		})
	}
}

func TestGlobalTagPreflightFailureWithdrawsChangedPrivateAccessOnly(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "report.txt"), []byte("private report"), 0600); err != nil {
		t.Fatal(err)
	}
	lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "bob@example.com"},
		Node:        &tailcfg.Node{ComputedName: "bob-device"},
	}, nil)
	oldFactory := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &fakeTSNetServer{localClient: lc}
	}
	defer func() { newTSNetServerFn = oldFactory }()
	s, err := New("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeAllNodes()
	report := registry.Service{Name: "report", Type: registry.TypeFile, Path: root,
		Tags: []string{"tag:tsmain"}, AllowedUsers: []string{"alice@example.com", "bob@example.com"}}
	stay := registry.Service{Name: "stay", Type: registry.TypeFile, Path: root,
		Tags: []string{"tag:tsmain"}, AllowedUsers: []string{"bob@example.com"}}
	writeRegistry(t, []registry.Service{report, stay})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	request := func(name string) int {
		node := s.nodes[name]
		if node == nil {
			return http.StatusServiceUnavailable
		}
		r := httptest.NewRequest("GET", "https://"+name+".example/report.txt", nil)
		r.RemoteAddr = "100.64.0.10:1234"
		w := httptest.NewRecorder()
		node.httpSrv.Handler.ServeHTTP(w, r)
		return w.Code
	}
	if got := request("report"); got != http.StatusOK {
		t.Fatalf("old report control HTTP %d", got)
	}
	if got := request("stay"); got != http.StatusOK {
		t.Fatalf("unchanged control HTTP %d", got)
	}
	statePath := filepath.Join(config.NodesDirIn(s.cfgDir), "report")
	report.AllowedUsers = []string{"alice@example.com"}
	writeRegistry(t, []registry.Service{report, stay})
	injected := errors.New("synthetic tag preflight outage")
	s.ensureTagsFn = func(context.Context, []string) error { return injected }
	if err := s.syncNodes(context.Background()); !errors.Is(err, injected) {
		t.Fatalf("sync error = %v, want injected tag outage", err)
	}
	if got := request("report"); got != http.StatusServiceUnavailable {
		t.Fatalf("changed private listener still serves Bob: HTTP %d", got)
	}
	if got := request("stay"); got != http.StatusOK {
		t.Fatalf("unchanged private service lost availability: HTTP %d", got)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("changed node state removed: %v", err)
	}
}
