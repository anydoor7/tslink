package server

import (
	"context"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/errcode"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
)

func serviceScopedCodes(t *testing.T) []string {
	t.Helper()
	var codes []string
	for _, row := range errcode.All() {
		if row.Scope == errcode.Service {
			codes = append(codes, row.Code)
		}
	}
	// Control: the two per-service codes B2 added and the hand list missed.
	joined := "," + strings.Join(codes, ",") + ","
	for _, want := range []string{registry.CodeFunnelExpiryRequired, registry.CodePathExposesConfigDir} {
		if !strings.Contains(joined, ","+want+",") {
			t.Fatalf("errcode table has no Service scope for %s: %v", want, codes)
		}
	}
	return codes
}

// TestEveryServiceScopedCodeIsolatesItsService: the daemon isolated a failing
// service only when its code was in the hand list of
// recoverableServiceFailure. A per-service code raised in a start path and
// missing from that list failed the whole sync, and Run treats a failing
// initial sync as fatal. The table's Service scope is now the one source; each
// such code is driven through the registry-issue path and the start path.
func TestEveryServiceScopedCodeIsolatesItsService(t *testing.T) {
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	for _, code := range serviceScopedCodes(t) {
		next := []string{"next step for " + code}
		coded := registry.CodedError{Code: code, Message: "synthetic " + code, Next: next}

		t.Run(code+"/registry issue", func(t *testing.T) {
			failure := serviceIssueFailure(registry.ServiceIssue{Name: svc.Name, Service: svc, Err: coded})
			assertIsolatedFailure(t, failure, code, next)
		})

		t.Run(code+"/start", func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			other := registry.Service{Name: "other", Type: registry.TypeFile, Path: t.TempDir(), Tags: []string{"tag:tsmain"}}
			writeRegistry(t, []registry.Service{svc, other})
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			s, err := New("", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.closeAllNodes)
			s.SetAuthKeyProvider(func(_ context.Context, node registry.Service) (string, error) {
				if node.Name == svc.Name {
					return "", coded
				}
				return "tskey-auth-synthetic", nil
			})
			s.SetUserSuppliedAuthKey(true)
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v; a %s failure of one service must not fail the sync", err, code)
			}
			s.mu.RLock()
			failure := s.serviceFailures[svc.Name]
			s.mu.RUnlock()
			assertIsolatedFailure(t, failure, code, next)
			if s.nodeRunning(svc.Name) || !s.nodeRunning(other.Name) {
				t.Fatalf("running: app=%v other=%v; want only other", s.nodeRunning(svc.Name), s.nodeRunning(other.Name))
			}
		})
	}
}

func assertIsolatedFailure(t *testing.T, failure runtimesnapshot.ServiceState, code string, next []string) {
	t.Helper()
	if failure.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || failure.Error == nil {
		t.Fatalf("failure = %+v, want an isolated failed service", failure)
	}
	if failure.Error.Code != code {
		t.Fatalf("failure code = %q, want %s", failure.Error.Code, code)
	}
	if strings.Join(failure.Error.Next, "|") != strings.Join(next, "|") {
		t.Fatalf("failure next = %q, want %q", failure.Error.Next, next)
	}
}
