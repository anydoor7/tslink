package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// countingWhoIsClient is fakeWhoIsClient with a call counter. The counter is
// what the cache assertions are about: "the same caller is resolved once" and
// "no caching at all" produce identical log output and differ only here.
func countingWhoIsClient(t *testing.T, resp *apitype.WhoIsResponse, respErr error) (*LocalClient, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	client := &LocalClient{
		OmitAuth: true,
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			if respErr != nil {
				return nil, respErr
			}
			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}
	return client, &calls
}

func whoIsUser(login, node string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: login, DisplayName: login},
		Node:        &tailcfg.Node{ComputedName: node},
	}
}

// getRequest drives one request through handler from addr and returns the
// recorder, so each test states only what it asserts.
func getRequest(t *testing.T, handler http.Handler, addr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// accessRecord returns the attributes of the idx-th "access" record, skipping
// everything else the handler under test logged. Indexing by position instead
// would read the ACL denial line on the one test whose request is refused.
func accessRecord(t *testing.T, ch *captureHandler, idx int) map[string]any {
	t.Helper()
	seen := 0
	for i := 0; ; i++ {
		if ch.message(t, i) != "access" {
			continue
		}
		if seen == idx {
			return ch.attrMap(t, i)
		}
		seen++
	}
}

// logField reads one attribute of the idx-th access record as a string,
// failing when the key is absent. Absent and empty are different answers here:
// the schema promise is that the field is always present.
func logField(t *testing.T, ch *captureHandler, idx int, key string) string {
	t.Helper()
	attrs := accessRecord(t, ch, idx)
	value, ok := attrs[key]
	if !ok {
		t.Fatalf("access log record %d has no %q field: %+v", idx, key, attrs)
	}
	text, ok := value.(string)
	if !ok {
		t.Fatalf("access log %q = %v of type %T, want a string", key, value, value)
	}
	return text
}

func TestAccessLog_LogsVerifiedLogin(t *testing.T) {
	ch := installCaptureLogger()
	client, _ := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)

	handler := AccessLogMiddleware("svc", NewStaticIdentityResolver(client), okHandler())
	rec := getRequest(t, handler, "100.64.0.1:1234")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := logField(t, ch, 0, "login"); got != "alice@example.com" {
		t.Fatalf("login = %q, want alice@example.com", got)
	}
	if got := logField(t, ch, 0, "node"); got != "alice-laptop" {
		t.Fatalf("node = %q, want alice-laptop", got)
	}
}

// TestAccessLog_WhoIsFailureServesTheRequestAndLogsAnEmptyLogin is the
// fail-open contract. The access log is an observation of a request, not a
// gate on it, so an identity lookup that fails must cost the log line's login
// field and nothing else.
func TestAccessLog_WhoIsFailureServesTheRequestAndLogsAnEmptyLogin(t *testing.T) {
	ch := installCaptureLogger()
	client, calls := countingWhoIsClient(t, nil, errors.New("whois unavailable"))

	served := false
	handler := AccessLogMiddleware("svc", NewStaticIdentityResolver(client),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			served = true
			w.WriteHeader(http.StatusOK)
		}))
	rec := getRequest(t, handler, "100.64.0.1:1234")

	if !served {
		t.Fatal("handler was not reached after a WhoIs failure")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the request served as 200 despite the WhoIs failure", rec.Code)
	}
	if got := logField(t, ch, 0, "login"); got != "" {
		t.Fatalf("login = %q, want empty on WhoIs failure", got)
	}
	if got := logField(t, ch, 0, "node"); got != "" {
		t.Fatalf("node = %q, want empty on WhoIs failure", got)
	}
	// Control: the lookup was actually attempted, so the empty field above is
	// the failure being handled and not the resolver never having run.
	if calls.Load() == 0 {
		t.Fatal("WhoIs was never called; the empty login proves nothing")
	}
}

// TestAccessLog_NilResolverServesAndLogsEmptyIdentity is the node that cannot
// resolve anyone at all: the fields are present and empty rather than missing,
// so one parser reads every access line.
func TestAccessLog_NilResolverServesAndLogsEmptyIdentity(t *testing.T) {
	ch := installCaptureLogger()

	rec := getRequest(t, AccessLogMiddleware("svc", nil, okHandler()), "100.64.0.1:1234")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := logField(t, ch, 0, "login"); got != "" {
		t.Fatalf("login = %q, want empty with no resolver", got)
	}
	if got := logField(t, ch, 0, "node"); got != "" {
		t.Fatalf("node = %q, want empty with no resolver", got)
	}
}

// TestAccessLog_SameAddressIsResolvedOnce pins the shared cache. Without it
// every request pays a WhoIs round trip, and nothing in the log output would
// say so.
func TestAccessLog_SameAddressIsResolvedOnce(t *testing.T) {
	installCaptureLogger()
	client, calls := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)
	handler := AccessLogMiddleware("svc", NewStaticIdentityResolver(client), okHandler())

	getRequest(t, handler, "100.64.0.1:1234")
	getRequest(t, handler, "100.64.0.1:5678")

	if got := calls.Load(); got != 1 {
		t.Fatalf("WhoIs calls = %d for two requests from one address, want 1", got)
	}
}

// TestAccessLog_DifferentAddressesAreResolvedSeparately is the control for the
// test above: a cache that returned one answer for every caller would satisfy
// "resolved once" perfectly and attribute the whole tailnet to one account.
func TestAccessLog_DifferentAddressesAreResolvedSeparately(t *testing.T) {
	ch := installCaptureLogger()
	var calls atomic.Int64
	client := &LocalClient{
		OmitAuth: true,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			n := calls.Add(1)
			who := whoIsUser("alice@example.com", "alice-laptop")
			if n > 1 {
				who = whoIsUser("bob@example.com", "bob-desktop")
			}
			body, err := json.Marshal(who)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}
	handler := AccessLogMiddleware("svc", NewStaticIdentityResolver(client), okHandler())

	getRequest(t, handler, "100.64.0.1:1234")
	getRequest(t, handler, "100.64.0.2:1234")

	if got := calls.Load(); got != 2 {
		t.Fatalf("WhoIs calls = %d for two distinct addresses, want 2", got)
	}
	if got := logField(t, ch, 0, "login"); got != "alice@example.com" {
		t.Fatalf("first login = %q, want alice@example.com", got)
	}
	if got := logField(t, ch, 1, "login"); got != "bob@example.com" {
		t.Fatalf("second login = %q, want bob@example.com", got)
	}
}

// TestAccessLog_TaggedNodeHasNoLoginAndKeepsItsNodeName states what a tagged
// caller is: a machine. Reporting the pseudo-user Tailscale attributes tagged
// traffic to would make a fleet of CI nodes read as one busy person in exactly
// the per-account counts this field exists to produce.
func TestAccessLog_TaggedNodeHasNoLoginAndKeepsItsNodeName(t *testing.T) {
	ch := installCaptureLogger()
	tagged := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "tagged-devices"},
		Node:        &tailcfg.Node{ComputedName: "ci-runner", Tags: []string{"tag:ci"}},
	}
	client, _ := countingWhoIsClient(t, tagged, nil)

	rec := getRequest(t, AccessLogMiddleware("svc", NewStaticIdentityResolver(client), okHandler()), "100.64.0.9:1234")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := logField(t, ch, 0, "login"); got != "" {
		t.Fatalf("login = %q, want empty for a tagged node", got)
	}
	if got := logField(t, ch, 0, "node"); got != "ci-runner" {
		t.Fatalf("node = %q, want the tagged machine still identified as ci-runner", got)
	}
}

// TestSecuritySemantics_AccessLogLoginIsNeverTakenFromRequestHeaders is the
// reason this field is worth writing down at all. X-Tailscale-User-Login is
// attacker-controlled on the way in -- the proxy strips it for that reason --
// so a log that echoed it would record an attacker's claim as an audited fact,
// which is worse than having no field.
func TestSecuritySemantics_AccessLogLoginIsNeverTakenFromRequestHeaders(t *testing.T) {
	ch := installCaptureLogger()
	client, _ := countingWhoIsClient(t, nil, errors.New("whois unavailable"))

	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.RemoteAddr = "100.64.0.1:1234"
	req.Header.Set("X-Tailscale-User-Login", "attacker@example.com")
	req.Header.Set("X-Tailscale-Node", "attacker-node")
	rec := httptest.NewRecorder()

	AccessLogMiddleware("svc", NewStaticIdentityResolver(client), okHandler()).ServeHTTP(rec, req)

	if got := logField(t, ch, 0, "login"); got != "" {
		t.Fatalf("login = %q, want empty: the header is a claim, not an identity", got)
	}
	if got := logField(t, ch, 0, "node"); got != "" {
		t.Fatalf("node = %q, want empty: the header is a claim, not an identity", got)
	}
}

// TestSecuritySemantics_AccessLogRecordSchema documents the whole record. The
// forbidden list is the point: it is the set of plausible near-misses a future
// change might introduce beside the two real fields, and a reader parsing
// these lines would silently read the wrong one.
func TestSecuritySemantics_AccessLogRecordSchema(t *testing.T) {
	ch := installCaptureLogger()
	client, _ := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)

	handler := AccessLogMiddleware("svc", NewStaticIdentityResolver(client),
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("ok"))
		}))
	req := httptest.NewRequest(http.MethodPost, "/submit?ignored=true", strings.NewReader("body"))
	req.RemoteAddr = "100.64.0.1:1234"
	req.Header.Set("User-Agent", "tslink-test")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if msg := ch.message(t, 0); msg != "access" {
		t.Fatalf("log message = %q, want access", msg)
	}
	attrs := accessRecord(t, ch, 0)
	for _, key := range []string{"service", "method", "path", "status", "duration_ms", "bytes", "remote_addr", "user_agent", "login", "node"} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("access log missing %q in %+v", key, attrs)
		}
	}
	for _, key := range []string{"user", "user_login", "tailscale_user", "tailscale_node", "display_name", "profile_pic"} {
		if _, ok := attrs[key]; ok {
			t.Fatalf("access log invented identity field %q in %+v", key, attrs)
		}
	}
	if len(attrs) != 10 {
		t.Fatalf("access log has %d fields in %+v, want exactly the 10 documented ones", len(attrs), attrs)
	}
	if attrs["service"] != "svc" || attrs["method"] != http.MethodPost || attrs["path"] != "/submit" ||
		attrs["remote_addr"] != "100.64.0.1:1234" || attrs["user_agent"] != "tslink-test" ||
		attrs["login"] != "alice@example.com" || attrs["node"] != "alice-laptop" {
		t.Fatalf("access log attrs = %+v, want documented schema values", attrs)
	}
}

// TestIdentityResolver_AcquiresItsClientLazilyAndRetries covers the file share
// with no allow list from the other side: construction must not touch the
// local client, and one early failure must not cost identity for the life of
// the process.
func TestIdentityResolver_AcquiresItsClientLazilyAndRetries(t *testing.T) {
	installCaptureLogger()
	client, _ := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)

	var provides atomic.Int64
	resolver := NewIdentityResolver(func() (*LocalClient, error) {
		if provides.Add(1) == 1 {
			return nil, errors.New("node is not up yet")
		}
		return client, nil
	})
	if got := provides.Load(); got != 0 {
		t.Fatalf("local client requested %d times at construction, want 0", got)
	}

	if login, _ := resolver.Principal(t.Context(), "100.64.0.1:1234"); login != "" {
		t.Fatalf("login = %q while the client was unavailable, want empty", login)
	}
	login, node := resolver.Principal(t.Context(), "100.64.0.1:1234")
	if login != "alice@example.com" || node != "alice-laptop" {
		t.Fatalf("login/node = %q/%q after the client became available, want alice@example.com/alice-laptop", login, node)
	}
	if got := provides.Load(); got != 2 {
		t.Fatalf("local client requested %d times, want a retry after the first failure", got)
	}
}

// TestIdentityResolver_NilIsSafe pins the representation choice: "no identity
// available" is a nil resolver, and every entry point tolerates it.
func TestIdentityResolver_NilIsSafe(t *testing.T) {
	if r := NewStaticIdentityResolver(nil); r != nil {
		t.Fatalf("NewStaticIdentityResolver(nil) = %v, want nil", r)
	}
	if r := NewIdentityResolver(nil); r != nil {
		t.Fatalf("NewIdentityResolver(nil) = %v, want nil", r)
	}
	var resolver *IdentityResolver
	if login, node := resolver.Principal(t.Context(), "100.64.0.1:1234"); login != "" || node != "" {
		t.Fatalf("nil resolver Principal = %q/%q, want empty", login, node)
	}
	if who := resolver.whoIs(t.Context(), "100.64.0.1:1234"); who != nil {
		t.Fatalf("nil resolver whoIs = %v, want nil", who)
	}
}

// TestIdentityResolver_AddressWithoutAPortIsStillCached covers the key the
// cache is built on. A RemoteAddr with no port would otherwise miss on every
// request while looking like it worked.
func TestIdentityResolver_AddressWithoutAPortIsStillCached(t *testing.T) {
	client, calls := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)
	resolver := NewStaticIdentityResolver(client)

	for range 2 {
		if login, _ := resolver.Principal(t.Context(), "100.64.0.1"); login != "alice@example.com" {
			t.Fatalf("login = %q, want alice@example.com for a portless address", login)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("WhoIs calls = %d for a repeated portless address, want 1", got)
	}
}

// startHTTPNode runs the production node assembly for svc and returns the
// handler chain the daemon actually serves, so these tests cannot pass against
// a chain assembled only in a test.
func startHTTPNode(t *testing.T, fake *fakeTSNetServer, svc registry.Service) http.Handler {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	s, err := New("key", "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return fake }
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	if err := s.startNodeLocked(t.Context(), svc); err != nil {
		t.Fatalf("startNodeLocked() error = %v", err)
	}
	node, ok := s.nodes[svc.Name]
	if !ok {
		t.Fatalf("service %q did not commit a node", svc.Name)
	}
	t.Cleanup(func() { s.stopNodeLocked(svc.Name, false) })
	return node.httpSrv.Handler
}

// TestStartNodeLocked_FileShareWithNoAllowListLogsIdentity is the case the
// whole change is for: a share with no allow list is the one nothing else on
// the node resolves identity for, and it was the one whose access lines named
// only an address.
//
// It also pins the ordering that makes that possible. The startup path must
// still not touch the local client -- a share that works today must not start
// refusing to start over a log field -- so the assertion is both: zero at
// startup, resolved by the time the first request is logged.
func TestStartNodeLocked_FileShareWithNoAllowListLogsIdentity(t *testing.T) {
	ch := installCaptureLogger()
	fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)}

	handler := startHTTPNode(t, fake, registry.Service{
		Name: "files", Type: registry.TypeFile, Path: t.TempDir(),
	})
	if fake.localClientCalled != 0 {
		t.Fatalf("LocalClient called %d times at startup, want none for file/no-allow", fake.localClientCalled)
	}

	getRequest(t, handler, "100.64.0.1:1234")

	if fake.localClientCalled != 1 {
		t.Fatalf("LocalClient called %d times after one request, want 1", fake.localClientCalled)
	}
	if got := logField(t, ch, 0, "login"); got != "alice@example.com" {
		t.Fatalf("login = %q on a no-allow file share, want alice@example.com", got)
	}
}

// TestStartNodeLocked_DeniedRequestIsStillAttributed is the row that makes the
// audit usable. A refusal is the line an operator most wants a name on, and it
// is produced by a middleware that returns before the handler -- so the access
// log has to have resolved identity by then.
func TestStartNodeLocked_DeniedRequestIsStillAttributed(t *testing.T) {
	ch := installCaptureLogger()
	fake := &fakeTSNetServer{localClient: fakeWhoIsClient(t, whoIsUser("mallory@example.com", "mallory-laptop"), nil)}

	handler := startHTTPNode(t, fake, registry.Service{
		Name: "files", Type: registry.TypeFile, Path: t.TempDir(),
		AllowedUsers: []string{"alice@example.com"},
	})
	rec := getRequest(t, handler, "100.64.0.1:1234")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want the allow list still fail-closed at 403", rec.Code)
	}
	if got := logField(t, ch, 0, "login"); got != "mallory@example.com" {
		t.Fatalf("login = %q on a denied request, want mallory@example.com", got)
	}
	status, ok := accessRecord(t, ch, 0)["status"].(int64)
	if !ok || status != int64(http.StatusForbidden) {
		t.Fatalf("logged status = %v, want 403", accessRecord(t, ch, 0)["status"])
	}
}

// TestStartNodeLocked_ProxyShareSharesOneWhoIsWithItsAccessLog is the "no
// duplicate cost" requirement measured end to end: the proxy's identity
// headers and the access line describe the same caller, and asking twice would
// be invisible in both.
func TestStartNodeLocked_ProxyShareSharesOneWhoIsWithItsAccessLog(t *testing.T) {
	ch := installCaptureLogger()
	var backendHeaders http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)

	client, calls := countingWhoIsClient(t, whoIsUser("alice@example.com", "alice-laptop"), nil)
	fake := &fakeTSNetServer{localClient: client}

	handler := startHTTPNode(t, fake, registry.Service{
		Name: "app", Type: registry.TypeProxy, Target: backend.URL,
	})
	rec := getRequest(t, handler, "100.64.0.1:1234")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want the backend's 204", rec.Code)
	}
	if got := backendHeaders.Get("X-Tailscale-User-Login"); got != "alice@example.com" {
		t.Fatalf("X-Tailscale-User-Login = %q, want alice@example.com still injected", got)
	}
	if got := logField(t, ch, 0, "login"); got != "alice@example.com" {
		t.Fatalf("login = %q, want alice@example.com", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("WhoIs calls = %d for one proxied request, want the access log and the proxy to share 1", got)
	}
}
