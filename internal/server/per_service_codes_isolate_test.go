package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/errcode"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv"
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

// TestEveryRegistryValidationCodeIsolatesItsService drives the codes the
// registry itself raises through a real registry.json: each bad entry sits
// beside a valid one, the sync succeeds, the bad entry is isolated with its
// code and next steps, and the table agrees that the code is per-service.
// The expected codes here come from registry validation, not from the table,
// so dropping any of them from the table's Service scope turns this red.
func TestEveryRegistryValidationCodeIsolatesItsService(t *testing.T) {
	cases := map[string]func(t *testing.T, cfgDir string) string{
		registry.CodeInvalidServiceName: func(*testing.T, string) string {
			return `{"name":"Bad_Name","type":"proxy","target":"http://localhost:3000"}`
		},
		registry.CodeInvalidTag: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","tags":["notatag"]}`
		},
		registry.CodeAllowUnsupportedTCP: func(*testing.T, string) string {
			return `{"name":"bad","type":"tcp","target":"localhost:5432","port":5432,"allowed_users":["alice@example.com"]}`
		},
		registry.CodePathMustBeAbsolute: func(*testing.T, string) string { return `{"name":"bad","type":"file","path":"relative"}` },
		registry.CodePathNotFound: func(t *testing.T, _ string) string {
			return `{"name":"bad","type":"file","path":` + strconv.Quote(filepath.Join(t.TempDir(), "missing")) + `}`
		},
		registry.CodePathNotDirectory: func(t *testing.T, _ string) string {
			file := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			return `{"name":"bad","type":"file","path":` + strconv.Quote(file) + `}`
		},
		registry.CodePathExposesConfigDir: func(_ *testing.T, cfgDir string) string {
			return `{"name":"bad","type":"file","path":` + strconv.Quote(cfgDir) + `}`
		},
		registry.CodeLinkLocalTargetRefused: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://169.254.169.254"}`
		},
		registry.CodeFunnelPublicAckRequired: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","funnel":true,"funnel_expires_at":"never"}`
		},
		registry.CodeFunnelExpiryRequired: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true}`
		},
		registry.CodeFunnelAllowConflict: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":"never","allowed_users":["alice@example.com"]}`
		},
		registry.CodeFunnelControlURLConflict: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"funnel_expires_at":"never","control_url":"https://headscale.example.com"}`
		},
		registry.CodeFunnelTypeConflict: func(*testing.T, string) string {
			return `{"name":"bad","type":"tcp","target":"localhost:5432","port":5432,"funnel":true,"public_ack":true,"funnel_expires_at":"never"}`
		},
		registry.CodeUnknownConfigKey: func(*testing.T, string) string {
			return `{"name":"bad","type":"proxy","target":"http://localhost:3000","bogus":true}`
		},
		registry.CodeInvalidServiceConfig: func(*testing.T, string) string { return `{"name":"bad","type":"proxy"}` },
	}
	for code, entry := range cases {
		t.Run(code, func(t *testing.T) {
			cfgDir := testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			path, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			raw := entry(t, cfgDir)
			var probe struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(raw), &probe); err != nil {
				t.Fatal(err)
			}
			doc := `{"schema_version":1,"services":[` + raw + `,{"name":"other","type":"file","path":` + strconv.Quote(t.TempDir()) + `}]}`
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			oldNew := newTSNetServerFn
			newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return &fakeTSNetServer{} }
			t.Cleanup(func() { newTSNetServerFn = oldNew })
			s, err := New("tskey-auth-synthetic", "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(s.closeAllNodes)
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v; one bad entry must not fail the sync", err)
			}
			s.mu.RLock()
			failure := s.serviceFailures[probe.Name]
			s.mu.RUnlock()
			if failure.Error == nil || failure.Error.Code != code {
				t.Fatalf("failure of %s = %+v, want %s", probe.Name, failure.Error, code)
			}
			if code != registry.CodeInvalidServiceConfig && len(failure.Error.Next) == 0 {
				t.Fatalf("failure of %s has no next steps: %+v", probe.Name, failure.Error)
			}
			if !errcode.IsServiceScoped(code) {
				t.Fatalf("registry validation raises %s for one entry, but the errcode table does not scope it to the service", code)
			}
			if !s.nodeRunning("other") {
				t.Fatal("the valid entry beside it is not running")
			}
		})
	}
}
