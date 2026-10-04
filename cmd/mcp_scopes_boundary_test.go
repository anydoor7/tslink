package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/server"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func scopedHTTPRequest(t *testing.T, h *httptest.Server, tool, args string) (int, string, error) {
	t.Helper()
	fixture := mcpHTTPToolCallRequest(tool)
	body, err := io.ReadAll(fixture.Body)
	if err != nil {
		return 0, "", err
	}
	raw := strings.Replace(string(body), `"arguments":{}`, `"arguments":`+args, 1)
	req, err := http.NewRequest(http.MethodPost, h.URL+server.MCPControlPlanePath, strings.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header = fixture.Header
	response, err := h.Client().Do(req)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	output, err := io.ReadAll(response.Body)
	return response.StatusCode, string(output), err
}

func TestMCPOwnerExpiryAfterRegistryWait(t *testing.T) {
	for _, expire := range []bool{false, true} {
		name := "active-control"
		if expire {
			name = "expired"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:original"}}); err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := filelock.Lock(lock); err != nil {
				t.Fatal(err)
			}
			defer filelock.Unlock(lock)
			// Policy time must not expire during HTTP/SDK/audit fixture setup.
			// The real transport and OS locks stay outside virtual time.
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			var policyClock atomic.Int64
			policyClock.Store(now.UnixNano())
			a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
			a.nowFn = func() time.Time { return time.Unix(0, policyClock.Load()) }
			locked, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			mutate := tagsMutateServiceFn
			t.Cleanup(func() { tagsMutateServiceFn = mutate })
			tagsMutateServiceFn = func(path, name string, fn func(registry.Service) (registry.Service, error)) (registry.Service, error) {
				return mutate(path, name, func(svc registry.Service) (registry.Service, error) {
					close(locked)
					<-release
					return fn(svc)
				})
			}
			entered, completed := make(chan struct{}), make(chan struct{})
			original := a.tagsSet
			a.tagsSet = func(ctx context.Context, service, tag string) (any, error) {
				close(entered)
				result, err := original(ctx, service, tag)
				close(completed)
				return result, err
			}
			cp := &server.MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}}, Handler: newMCPStreamableHandler(a)}
			h := httptest.NewServer(server.NewMCPControlPlaneHandler(cp, mcpHTTPWhoIsClient(t, "owner")))
			defer func() { unblock(); filelock.Unlock(lock); h.Close() }()
			response := make(chan string, 1)
			go func() {
				status, body, err := scopedHTTPRequest(t, h, "tags_set", `{"service":"photos","tag":"tag:updated"}`)
				response <- stringMustJSON(map[string]any{"status": status, "body": body, "transport_error": err != nil})
			}()
			select {
			case <-entered:
			case body := <-response:
				t.Fatalf("request ended before registry writer: %s", body)
			}
			if err := filelock.Unlock(lock); err != nil {
				t.Fatal(err)
			}
			select {
			case <-locked:
			case body := <-response:
				t.Fatalf("request ended before locked mutation: %s", body)
			}
			// A second descriptor cannot acquire the actual writer's lock.
			if acquired, err := filelock.TryLock(lock); err != nil || acquired {
				if acquired {
					filelock.Unlock(lock)
				}
				t.Fatalf("mutation does not own registry lock: acquired=%v err=%v", acquired, err)
			}
			if expire {
				policyClock.Store(expiry.UnixNano())
			}
			unblock()
			<-completed
			body := <-response
			t.Logf("HTTP result: %s", body)
			if !strings.Contains(body, `"status":200`) || !strings.Contains(body, `"transport_error":false`) {
				t.Fatalf("RPC transport failed: %s", body)
			}
			if expire && !strings.Contains(body, "mcp_scope_denied") {
				t.Fatalf("RPC did not report scope denial: %s", body)
			}
			reg, err := registry.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			got := reg.Services[0].Tags
			want := []string{"tag:updated"}
			if expire {
				want = []string{"tag:original"}
			}
			t.Logf("expired=%v persisted tags=%v", expire, got)
			entries, err := a.auditRead()
			if err != nil || len(entries) != 2 {
				t.Fatalf("audit=%v err=%v", entries, err)
			}
			code := "ok"
			if expire {
				code = "mcp_scope_denied"
			}
			if entries[1].Phase != "completion" || entries[1].Result != code {
				t.Errorf("completion=%+v want=%s", entries[1], code)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("expired binding changed registry after lock wait: tags=%v want=%v", got, want)
			}
		})
	}
}

func stringMustJSON(value any) string { b, _ := json.Marshal(value); return string(b) }

func TestMCPScopedReadEndsAtBindingExpiry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"active-control", "expired"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Use the real HTTP/RPC handler without a TCP accept goroutine:
				// OS network waits are not durably blocked in a synctest bubble.
				a := defaultMCPActions(sharePaths{Registry: path, PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json")}, io.Discard)
				expiry := time.Now().Add(300 * time.Millisecond)
				entered, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				defer unblock()
				observed := make(chan error, 1)
				original := a.list
				a.list = func(ctx context.Context) (any, error) {
					close(entered)
					<-release
					observed <- ctx.Err()
					return original(ctx)
				}
				cp := &server.MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "reader", Scope: testRoleScope("viewer"), ExpiresAt: &expiry}}, Handler: newMCPStreamableHandler(a)}
				h := server.NewMCPControlPlaneHandler(cp, mcpHTTPWhoIsClient(t, "reader"))
				rr := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); h.ServeHTTP(rr, mcpHTTPToolCallRequest("list")) }()
				select {
				case <-entered:
				case <-done:
					t.Fatalf("read ended before dispatch: %d %s", rr.Code, rr.Body.String())
				}
				synctest.Wait()
				if state == "expired" {
					time.Sleep(time.Until(expiry))
					synctest.Wait()
				}
				unblock()
				<-done
				ctxErr := <-observed
				body := rr.Body.String()
				if state == "expired" {
					if !errors.Is(ctxErr, context.DeadlineExceeded) || strings.Contains(body, `"name":"photos"`) {
						t.Fatalf("expired read: context=%v body=%s", ctxErr, body)
					}
				} else if ctxErr != nil || rr.Code != http.StatusOK || !strings.Contains(body, `"name":"photos"`) {
					t.Fatalf("active read: context=%v status=%d body=%s", ctxErr, rr.Code, body)
				}
			})
		})
	}
}

func TestMCPAuditGeneratedShareApp(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth.json")}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "finance", Type: registry.TypeProxy, Target: "http://localhost:4000"}); err != nil {
		t.Fatal(err)
	}
	// The daemon/enrollment boundary is faked; real share allocation, registry
	// mutation, and both durable audit writes are kept.
	shareIsRunningFn = func(string) bool { return false }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.example.invalid/test"}, nil
	}
	a := defaultMCPActions(paths, io.Discard)
	for _, tc := range []struct{ target, name, raw string }{
		{"3001", "named-app", `{"target":"3001","name":"named-app"}`},
		{"3002", "port-3002", `{"target":"3002"}`},
		{"3002", "port-3002", `{"target":"3002"}`},
		{"127.0.0.1:3002", "port-3002-2", `{"target":"127.0.0.1:3002"}`},
	} {
		target, name, raw := tc.target, tc.name, tc.raw
		result, err := callMCPTool(context.Background(), a, "share", json.RawMessage(raw))
		if err != nil || result.IsError {
			t.Fatalf("share %s: %v %v", target, result, err)
		}
		reg, err := registry.Load(paths.Registry)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, svc := range reg.Services {
			if svc.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("share did not persist app %s", name)
		}
		entries, err := a.auditRead()
		if err != nil {
			t.Fatal(err)
		}
		last := entries[len(entries)-1]
		intent := entries[len(entries)-2]
		if intent.Phase != "intent" || last.Phase != "completion" || intent.ID != last.ID {
			t.Fatal("audit phases do not describe one call")
		}
		if !strings.Contains(raw, `"name"`) && len(intent.Apps) != 0 {
			t.Errorf("allocation intent apps=%v want empty", intent.Apps)
		}
		t.Logf("created=%s audit.apps=%v result=%s", name, last.Apps, last.Result)
		if !reflect.DeepEqual(last.Apps, []string{name}) {
			t.Errorf("share app attribution=%v want=[%s]", last.Apps, name)
		}
	}
	results := make(chan *mcp.CallToolResult, 2)
	for _, target := range []string{"localhost:3003", "127.0.0.1:3003"} {
		go func(target string) {
			raw, _ := json.Marshal(map[string]any{"target": target})
			r, err := callMCPTool(context.Background(), a, "share", raw)
			if err != nil {
				results <- nil
			} else {
				results <- r
			}
		}(target)
	}
	for range 2 {
		if r := <-results; r == nil || r.IsError {
			t.Fatalf("concurrent share result=%v", r)
		}
	}
	entries, err := a.auditRead()
	if err != nil {
		t.Fatal(err)
	}
	completed := map[string]int{}
	for _, e := range entries {
		if e.Phase == "completion" && len(e.Apps) == 1 && strings.HasPrefix(e.Apps[0], "port-3003") {
			completed[e.Apps[0]]++
		}
	}
	if !reflect.DeepEqual(completed, map[string]int{"port-3003": 1, "port-3003-2": 1}) {
		t.Errorf("concurrent app attribution=%v", completed)
	}

}

func TestMCPAuditTagCallerAttribution(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	who := &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"}, Node: &tailcfg.Node{Name: "alice-agent", ID: 1, Tags: []string{"tag:helpers"}}}
	lc := &server.LocalClient{OmitAuth: true, Transport: mcpHTTPRoundTripper(func(*http.Request) (*http.Response, error) {
		b, _ := json.Marshal(who)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	cp := &server.MCPControlPlane{Bindings: []mcpscope.Binding{{Principal: "tag:helpers", Scope: testRoleScope("app-operator")}}, Handler: newMCPStreamableHandler(a)}
	h := httptest.NewServer(server.NewMCPControlPlaneHandler(cp, lc))
	defer h.Close()
	status, body, err := scopedHTTPRequest(t, h, "app_restart", `{"app":"photos"}`)
	if err != nil || status != 200 || strings.Contains(body, `"isError":true`) {
		t.Fatalf("restart failed %d %s %v", status, body, err)
	}
	entries, err := a.auditRead()
	if err != nil || len(entries) != 2 {
		t.Fatalf("audit=%v error=%v", entries, err)
	}
	t.Logf("WhoIs login=%s node=%s audit.identity.login=%s", who.UserProfile.LoginName, who.Node.Name, entries[1].Identity.Login)
	if entries[1].Identity.Login != who.UserProfile.LoginName {
		t.Errorf("audit records matching principal instead of caller identity: who=%q", entries[1].Identity.Login)
	}
	for _, e := range entries {
		if e.Kind != "mcp" || e.Identity.Node != "alice-agent" || e.Identity.Login != "alice@example.com" || e.Principal != "tag:helpers" || e.Role != "app-operator" {
			t.Errorf("caller attribution=%+v", e)
		}
	}
}

func TestMCPExpiryAfterRealAuditWait(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	var policyClock atomic.Int64
	policyClock.Store(now.UnixNano())
	a.nowFn = func() time.Time { return time.Unix(0, policyClock.Load()) }
	s := mcpscope.Session{Who: "operator", Scope: testRoleScope("app-operator"), ExpiresAt: &expiry}
	a.session = &s
	j := mcpaudit.Journal{Path: mcpAuditPath(path)}
	lock, err := os.OpenFile(j.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(lock)
	entered := make(chan struct{})
	first := true
	a.audit = func(ctx context.Context, e mcpaudit.Entry) error {
		if first {
			first = false
			close(entered)
		}
		return j.Record(ctx, e)
	}
	result := make(chan string, 1)
	go func() {
		r, err := callMCPTool(context.Background(), a, "app_restart", json.RawMessage(`{"app":"photos"}`))
		result <- mcpResultCode(r, err)
	}()
	<-entered
	// While Record is blocked on the OS lock, advance the injected policy
	// clock; releasing the lock synchronizes the subsequent decision.
	time.Sleep(30 * time.Millisecond)
	policyClock.Store(expiry.UnixNano())
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != "mcp_scope_denied" {
		t.Fatalf("post-audit code=%s", code)
	}
	reg, err := registry.Load(path)
	if err != nil || reg.Services[0].RestartGeneration != 0 {
		t.Fatalf("registry=%v error=%v", reg, err)
	}
	entries, err := j.Read()
	if err != nil || len(entries) != 2 || entries[1].Result != "denied" {
		t.Fatalf("receipts=%v error=%v", entries, err)
	}
}

func TestMCPPersonCreationHintAndUnknownRevoke(t *testing.T) {
	for _, role := range []string{"people-manager", "app-operator", "owner"} {
		t.Run(role, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
				t.Fatal(err)
			}
			a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
			s := mcpscope.Session{Who: "agent", Scope: testRoleScope(role)}
			a.session = &s
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			r, err := callMCPTool(context.Background(), a, "people_revoke", json.RawMessage(`{"who":"unknown","app":"photos"}`))
			if err != nil || r.IsError {
				t.Fatalf("unknown revoke=%v err=%v", r, err)
			}
			after, _ := os.ReadFile(path)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("unknown revoke altered registry")
			}
			r, err = callMCPTool(context.Background(), a, "people_grant", json.RawMessage(`{"who":"alice","app":"photos","for":"1h"}`))
			if role == "owner" {
				if err != nil || r.IsError {
					t.Fatal(r, err)
				}
			} else {
				if code := mcpResultCode(r, err); code != "mcp_person_owner_required" {
					t.Fatalf("code=%s result=%v", code, r)
				}
				content, _ := json.Marshal(r.Content)
				if !strings.Contains(string(content), "owner must add the person first") {
					t.Fatalf("missing owner-first hint: %s", content)
				}
				after, _ := os.ReadFile(path)
				if !reflect.DeepEqual(before, after) {
					t.Fatal("reduced person creation altered registry")
				}
				if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				r, err = callMCPTool(context.Background(), a, "people_grant", json.RawMessage(`{"who":"alice","app":"photos","for":"1h"}`))
				if err != nil || r.IsError {
					t.Fatalf("existing person control=%v err=%v", r, err)
				}
			}
		})
	}
}

func TestMCPCancelledRegistryCallKeepsCompletionAudit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(path, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:original"}}); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(lock)
	a := defaultMCPActions(sharePaths{Registry: path}, io.Discard)
	entered := make(chan struct{})
	original := a.tagsSet
	a.tagsSet = func(ctx context.Context, service, tag string) (any, error) {
		close(entered)
		return original(ctx, service, tag)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() {
		r, err := callMCPTool(ctx, a, "tags_set", json.RawMessage(`{"service":"photos","tag":"tag:updated"}`))
		done <- mcpResultCode(r, err)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("tags writer did not start")
	}
	cancel()
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != "mcp_scope_denied" {
			t.Fatalf("code=%s", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call did not finish")
	}
	reg, err := registry.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reg.Services[0].Tags, []string{"tag:original"}) {
		t.Fatal("cancelled call changed tags")
	}
	entries, err := a.auditRead()
	if err != nil || len(entries) != 2 || entries[1].Phase != "completion" || entries[1].Result != "mcp_scope_denied" {
		t.Fatalf("audit=%v err=%v", entries, err)
	}
}

func TestMCPDoctorReportsPotentialTagOverlap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		allow    []string
		bindings []mcpscope.Binding
		overlap  bool
	}{
		{"single-legacy", []string{"tag:owner"}, nil, false},
		{"duplicate-legacy", []string{"tag:owner", "tag:owner"}, nil, false},
		{"legacy-and-scoped", []string{"tag:owner"}, []mcpscope.Binding{{Principal: "tag:helper", Scope: testRoleScope("viewer")}}, true},
		{"two-scoped", nil, []mcpscope.Binding{{Principal: "tag:a", Scope: testRoleScope("viewer")}, {Principal: "tag:b", Scope: testRoleScope("viewer")}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := DoctorResult{}
			diagnoseMCPBindings(&result, config.GlobalConfig{MCP: &config.MCPConfig{Allow: tc.allow, Bindings: tc.bindings}}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			found := false
			for _, f := range result.Findings {
				if f.Code == "mcp_multiple_tags" {
					found = true
				}
			}
			if found != tc.overlap {
				t.Fatalf("overlap=%v want=%v findings=%v", found, tc.overlap, result.Findings)
			}
		})
	}
}

func TestMCPDefaultMutationsRecheckRegistrySession(t *testing.T) {
	for _, tc := range []struct{ tool, raw string }{
		{"share", `{"target":"3001"}`}, {"add", `{"name":"new-app","type":"proxy","target":"http://localhost:3001","no_daemon_install":true}`},
		{"template_apply", `{"name":"local-web","no_daemon_install":true}`}, {"recipe_apply", `{"recipe_id":"immich","no_daemon_install":true}`},
		{"people_add", `{"who":"bob","apps":["photos"]}`}, {"people_update", `{"who":"alice","apps":["photos"]}`}, {"people_remove", `{"who":"alice"}`},
		{"people_grant", `{"who":"alice","app":"photos","for":"1h"}`}, {"people_revoke", `{"who":"alice","app":"photos"}`},
		{"app_restart", `{"app":"photos"}`}, {"tags_set", `{"service":"photos","tag":"tag:updated"}`}, {"unshare", `{"name":"photos"}`},
	} {
		for _, state := range []string{"active", "expired", "cancelled"} {
			t.Run(tc.tool+"/"+state, func(t *testing.T) {
				restoreShareSeams(t)
				dir := t.TempDir()
				t.Setenv(config.ConfigDirEnv, dir)
				paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), Ownership: filepath.Join(dir, "ownership.json"), AuthHandoff: filepath.Join(dir, "auth.json")}
				for _, name := range []string{"photos", "finance"} {
					if _, err := registry.Add(paths.Registry, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"finance"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(paths.Registry)
				if err != nil {
					t.Fatal(err)
				}
				shareIsRunningFn = func(string) bool { return false }
				shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
					return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.example.invalid/test"}, nil
				}
				originalDelete := deleteDevicesFn
				deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
					return tailapi.CleanupResult{}, tailapi.ErrNoAPIClient
				}
				defer func() { deleteDevicesFn = originalDelete }()
				lock, err := os.OpenFile(paths.Registry+".lock", os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := filelock.Lock(lock); err != nil {
					t.Fatal(err)
				}
				defer filelock.Unlock(lock)
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(time.Hour)
				var expired atomic.Bool
				a := defaultMCPActions(paths, io.Discard)
				s := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
				a.session = &s
				a.nowFn = func() time.Time {
					if expired.Load() {
						return expiry
					}
					return now
				}
				entered := make(chan struct{})
				originalAudit := a.audit
				a.audit = func(ctx context.Context, e mcpaudit.Entry) error {
					err := originalAudit(ctx, e)
					if e.Phase == "intent" {
						close(entered)
					}
					return err
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan string, 1)
				go func() { r, err := callMCPTool(ctx, a, tc.tool, json.RawMessage(tc.raw)); done <- mcpResultCode(r, err) }()
				<-entered
				select {
				case code := <-done:
					t.Fatalf("call completed while lock held: %s", code)
				case <-time.After(30 * time.Millisecond):
				}
				if state == "expired" {
					expired.Store(true)
				}
				if state == "cancelled" {
					cancel()
				}
				if err := filelock.Unlock(lock); err != nil {
					t.Fatal(err)
				}
				var code string
				select {
				case code = <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("mutation did not complete")
				}
				if state == "active" {
					if code != "ok" {
						t.Fatalf("active mutation code=%s", code)
					}
				} else {
					if code != "mcp_scope_denied" {
						t.Fatalf("inactive mutation code=%s", code)
					}
					after, err := os.ReadFile(paths.Registry)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("inactive default action changed registry")
					}
				}
				entries, err := a.auditRead()
				if err != nil || len(entries) != 2 || entries[1].Result != code || entries[1].Phase != "completion" {
					t.Fatalf("audit=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestMCPShareCompensationRetainsBindingDeadline(t *testing.T) {
	for _, state := range []string{"active", "expired"} {
		t.Run(state, func(t *testing.T) {
			restoreShareSeams(t)
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth.json")}
			shareIsRunningFn = func(string) bool { return false }
			started, returned := make(chan struct{}), make(chan struct{})
			shareStartDaemonFn = func(ctx context.Context, _ io.Writer) (shareDaemonStart, error) {
				close(started)
				<-ctx.Done()
				close(returned)
				return shareDaemonStart{}, ctx.Err()
			}
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expiry := now.Add(time.Hour)
			var expired atomic.Bool
			a := defaultMCPActions(paths, io.Discard)
			s := mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}
			a.session = &s
			a.nowFn = func() time.Time {
				if expired.Load() {
					return expiry
				}
				return now
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan string, 1)
			go func() {
				r, err := callMCPTool(ctx, a, "share", json.RawMessage(`{"target":"3001"}`))
				done <- mcpResultCode(r, err)
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("share did not reach startup")
			}
			lock, err := os.OpenFile(paths.Registry+".lock", os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := filelock.Lock(lock); err != nil {
				t.Fatal(err)
			}
			defer filelock.Unlock(lock)
			if state == "expired" {
				expired.Store(true)
			}
			cancel()
			<-returned
			if err := filelock.Unlock(lock); err != nil {
				t.Fatal(err)
			}
			var code string
			select {
			case code = <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("share compensation did not complete")
			}
			wantCode := "internal_error"
			if state == "expired" {
				wantCode = "mcp_scope_denied"
			}
			if code != wantCode {
				t.Fatalf("code=%s want=%s", code, wantCode)
			}
			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			wantServices := 0
			if state == "expired" {
				wantServices = 1
			}
			if len(reg.Services) != wantServices {
				t.Fatalf("services=%v want count=%d", reg.Services, wantServices)
			}
			entries, err := a.auditRead()
			if err != nil || len(entries) != 2 || entries[1].Result != code || !reflect.DeepEqual(entries[1].Apps, []string{"port-3001"}) {
				t.Fatalf("audit=%v err=%v", entries, err)
			}
		})
	}
}

func mcpOwnerEffectContext(now func() time.Time, expiry time.Time) context.Context {
	return mcpscope.WithClock(mcpscope.WithSession(context.Background(), mcpscope.Session{
		Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry,
	}), now)
}

func TestMCPExistingRegistrationAdoption(t *testing.T) {
	for _, tc := range []struct{ tool, raw string }{
		{"add", `{"name":"photos","type":"proxy","target":"http://localhost:3000","no_daemon_install":true}`},
		{"recipe_apply", `{"recipe_id":"immich","no_daemon_install":true}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			a := defaultMCPActions(sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid")}, io.Discard)
			for range 2 {
				r, err := callMCPTool(context.Background(), a, tc.tool, json.RawMessage(tc.raw))
				if code := mcpResultCode(r, err); code != "ok" {
					t.Fatalf("adoption code=%s result=%v err=%v", code, r, err)
				}
			}
		})
	}
}

func TestMCPSupervisorEffectBoundaries(t *testing.T) {
	for _, phase := range []string{"lock-acquired", "before-install"} {
		for _, expire := range []bool{false, true} {
			state := "active"
			if expire {
				state = "expired"
			}
			t.Run(phase+"/"+state, func(t *testing.T) {
				isolateBootstrap(t)
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(time.Hour)
				current := now
				ctx := mcpOwnerEffectContext(func() time.Time { return current }, expiry)
				calls := 0
				var err error
				if phase == "lock-acquired" {
					old := trySupervisorLockFn
					t.Cleanup(func() { trySupervisorLockFn = old })
					trySupervisorLockFn = func(f *os.File) (bool, error) {
						acquired, err := old(f)
						if acquired && expire {
							current = expiry
						}
						return acquired, err
					}
					err = withSupervisorTransaction(ctx, func() error { calls++; return nil })
				} else {
					isRunningFn = func(string) bool {
						if expire {
							current = expiry
						}
						return false
					}
					installDaemonFn = func(context.Context, io.Writer) error { calls++; return errors.New("fixture install outcome") }
					err = ensureDaemon(ctx, io.Discard, false)
				}
				if expire {
					var denied mcpscope.Denied
					if !errors.As(err, &denied) || calls != 0 {
						t.Fatalf("err=%v effects=%d", err, calls)
					}
				} else if calls != 1 {
					t.Fatalf("active effects=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestMCPInviteCreateChecksSessionBeforeClient(t *testing.T) {
	for _, kind := range []string{"user", "device"} {
		for _, expire := range []bool{false, true} {
			state := "active"
			if expire {
				state = "expired"
			}
			t.Run(kind+"/"+state, func(t *testing.T) {
				t.Setenv(config.ConfigDirEnv, t.TempDir())
				service := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
				reg, pid, snapshot := configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"photos": "node-photos"})
				calls := 0
				inviteCreateUserFn = func(context.Context, string, string, bool) (tailapi.Invite, error) {
					calls++
					return tailapi.Invite{}, nil
				}
				inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
					calls++
					return tailapi.Invite{}, nil
				}
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(time.Hour)
				ctx := mcpOwnerEffectContext(func() time.Time {
					if expire {
						return expiry
					}
					return now
				}, expiry)
				var err error
				if kind == "user" {
					_, err = inviteUserCreate(ctx, "friend@example.com", "member", false)
				} else {
					_, err = inviteDeviceCreate(ctx, reg, pid, snapshot, "photos", "friend@example.com", false, false, false)
				}
				if expire {
					var denied mcpscope.Denied
					if !errors.As(err, &denied) || calls != 0 {
						t.Fatalf("err=%v client effects=%d", err, calls)
					}
				} else if err != nil || calls != 1 {
					t.Fatalf("active err=%v client effects=%d", err, calls)
				}
			})
		}
	}
}

func TestMCPRemovalRechecksEachEffect(t *testing.T) {
	for _, phase := range []string{"remote-cleanup", "local-state", "forget-ownership"} {
		for _, expire := range []bool{false, true} {
			state := "active"
			if expire {
				state = "expired"
			}
			t.Run(phase+"/"+state, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv(config.ConfigDirEnv, dir)
				regPath, ledgerPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "ownership.json")
				if _, err := registry.Add(regPath, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
					t.Fatal(err)
				}
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				if err := tsruntime.RecordOwnedNode(ledgerPath, "photos", "node-photos", now); err != nil {
					t.Fatal(err)
				}
				expiry := now.Add(time.Hour)
				current := now
				oldDelete, oldLocal, oldRunning, oldNow := deleteDevicesFn, removeNodeStateFn, removeDaemonRunningFn, removeNowFn
				t.Cleanup(func() {
					deleteDevicesFn, removeNodeStateFn, removeDaemonRunningFn, removeNowFn = oldDelete, oldLocal, oldRunning, oldNow
				})
				removeDaemonRunningFn = func() bool { return false }
				removeNowFn = func() time.Time { return now.Add(time.Minute) }
				remoteCalls, localCalls := 0, 0
				deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
					remoteCalls++
					if expire && phase == "local-state" {
						current = expiry
					}
					return tailapi.CleanupResult{Deleted: []string{"photos"}, ResolvedOwnershipIDs: []string{"node-photos"}}, nil
				}
				removeNodeStateFn = func(dir, name string) error {
					localCalls++
					if err := tsruntime.RemoveServiceNodeState(dir, name); err != nil {
						return err
					}
					if expire && phase == "forget-ownership" {
						current = expiry
					}
					return nil
				}
				ctx := mcpOwnerEffectContext(func() time.Time {
					if expire && phase == "remote-cleanup" {
						reg, err := registry.Load(regPath)
						if err != nil {
							t.Fatal(err)
						}
						if len(reg.Services) == 0 {
							return expiry
						}
					}
					return current
				}, expiry)
				result, err := removeServiceResultContext(ctx, regPath, ledgerPath, "photos")
				if !result.Removed {
					t.Fatalf("registry removal was not committed: result=%+v err=%v", result, err)
				}
				if expire {
					if phase == "forget-ownership" {
						if err != nil || !strings.Contains(result.DeviceWarning, "ownership ledger update failed") {
							t.Fatalf("result=%+v err=%v", result, err)
						}
					} else {
						var denied mcpscope.Denied
						if !errors.As(err, &denied) {
							t.Fatalf("err=%v want scope denial", err)
						}
					}
					if phase == "remote-cleanup" && remoteCalls != 0 {
						t.Fatalf("remote effects=%d", remoteCalls)
					}
					if phase != "forget-ownership" && localCalls != 0 {
						t.Fatalf("local effects=%d", localCalls)
					}
				} else if err != nil || remoteCalls != 1 || localCalls != 1 {
					t.Fatalf("err=%v remote=%d local=%d", err, remoteCalls, localCalls)
				}
				ledger, loadErr := tsruntime.LoadOwnership(ledgerPath)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				wantNodes := 0
				if expire {
					wantNodes = 1
				}
				if len(ledger.Nodes) != wantNodes || (expire && ledger.Nodes[0].RetiredAt == nil) {
					t.Fatalf("ownership=%+v", ledger)
				}
			})
		}
	}
}

func TestMCPShareStartupAndRearmEffects(t *testing.T) {
	for _, rearm := range []bool{false, true} {
		for _, expire := range []bool{false, true} {
			phase, state := "startup", "active"
			if rearm {
				phase = "rearm"
			}
			if expire {
				state = "expired"
			}
			t.Run(phase+"/"+state, func(t *testing.T) {
				restoreShareSeams(t)
				dir := t.TempDir()
				t.Setenv(config.ConfigDirEnv, dir)
				paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json"), AuthHandoff: filepath.Join(dir, "auth.json")}
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry, current := now.Add(time.Hour), now
				req := shareRequest{Target: "3000", Name: "photos"}
				var original registry.Service
				if rearm {
					past := time.Now().Add(-time.Hour)
					req.Funnel, req.FunnelTTL, req.PublicAck = true, "1h", true
					original = registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &past}
					_, err := registry.Add(paths.Registry, original)
					if err != nil {
						t.Fatal(err)
					}
					stored, err := registry.Load(paths.Registry)
					if err != nil {
						t.Fatal(err)
					}
					original = stored.Services[0]
				}
				shareIsRunningFn = func(string) bool {
					if expire && !rearm {
						current = expiry
					}
					return false
				}
				starts := 0
				shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
					starts++
					if rearm {
						if expire {
							current = expiry
						}
						return shareDaemonStart{}, errors.New("fixture startup failure")
					}
					return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.example.invalid/test"}, nil
				}
				ctx := mcpOwnerEffectContext(func() time.Time { return current }, expiry)
				_, err := executeShare(ctx, paths, req, time.Millisecond, io.Discard)
				reg, loadErr := registry.Load(paths.Registry)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				if rearm {
					if starts != 1 || err == nil || len(reg.Services) != 1 {
						t.Fatalf("starts=%d err=%v registry=%+v", starts, err, reg)
					}
					if reflect.DeepEqual(reg.Services[0], original) == expire {
						t.Fatalf("expired=%v original=%+v persisted=%+v", expire, original, reg.Services[0])
					}
				} else if expire {
					var denied mcpscope.Denied
					if !errors.As(err, &denied) || starts != 0 {
						t.Fatalf("err=%v starts=%d", err, starts)
					}
				} else if err != nil || starts != 1 {
					t.Fatalf("err=%v starts=%d", err, starts)
				}
			})
		}
	}
}
