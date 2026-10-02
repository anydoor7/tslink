package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func requestMember(login string) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}, Node: &tailcfg.Node{Hostinfo: (&tailcfg.Hostinfo{}).View()}}
}

func requestableApp(t *testing.T, f portalFixture, name string, on bool) {
	t.Helper()
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://127.0.0.1:8002", AllowedUsers: []string{"owner"}}
	for _, s := range reg.Services {
		if s.Name == name {
			svc = s
		}
	}
	svc.Requestable = on
	if _, err := registry.Add(f.path, svc); err != nil {
		t.Fatal(err)
	}
}

func requestForm(t *testing.T, f portalFixture) (string, string) {
	t.Helper()
	r, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode, body)
	}
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatal("missing request form", body)
	}
	return m[1], body
}

func requestPost(t *testing.T, f portalFixture, form url.Values, origin string) (int, string, http.Header) {
	t.Helper()
	r, err := http.NewRequest("POST", "http://"+f.addr+PortalRequestPath, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "home.tailnet.ts.net"
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := c.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(b), response.Header
}

func TestAccessRequestListenerLifecycle(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	token, body := requestForm(t, f)
	if !strings.Contains(body, `<option value="secret-payroll">`) || strings.Contains(body, "secret-payroll.tailnet.ts.net") {
		t.Fatal("requestable discovery leaked an inaccessible URL", body)
	}
	note := `<script>alert("x")</script>` + "\x1b[2J\u009b31m"
	form := url.Values{"csrf": {token}, "app": {"secret-payroll"}, "duration": {"3d"}, "note": {note}}
	status, b, h := requestPost(t, f, form, "https://home.tailnet.ts.net")
	if status != 303 || h.Get("Location") != "/" {
		t.Fatal(status, b, h)
	}
	status, b, _ = requestPost(t, f, form, "https://home.tailnet.ts.net")
	if status != 409 || !strings.Contains(b, "access_request_duplicate") {
		t.Fatal(status, b)
	}
	_, body = requestForm(t, f)
	if !strings.Contains(body, "Waiting for the owner.") || !strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "<script>") {
		t.Fatal("untrusted note not escaped", body)
	}
	now := time.Unix(0, f.now.Load())
	requests, err := registry.ListAccessRequests(f.path, now)
	if err != nil || len(requests) != 1 || requests[0].Note != note {
		t.Fatal(requests, err)
	}
	result, changed, err := registry.DecideAccessRequest(f.path, requests[0].ID, registry.RequestApproved, "8h", "", false, duration.Policy{}, now)
	if err != nil || !changed || result.Grant == nil || !result.Grant.ExpiresAt.Equal(now.Add(8*time.Hour)) {
		t.Fatal(result, changed, err)
	}
	apps := f.apps(t)
	if len(apps) != 2 || apps[1].Name != "secret-payroll" {
		t.Fatal("approved app missing from real portal", apps)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range reg.People {
		if p.Login == "alice" {
			if len(p.Grants) != 2 || !p.Grants[0].ExpiresAt.Equal(now.Add(time.Hour)) {
				t.Fatal("approval changed another app", p)
			}
		}
	}
	_, body = requestForm(t, f)
	if !strings.Contains(body, "Approved.") {
		t.Fatal(body)
	}
	_, changed, err = registry.DecideAccessRequest(f.path, requests[0].ID, registry.RequestApproved, "8h", "", false, duration.Policy{}, now.Add(time.Minute))
	if err != nil || changed {
		t.Fatal("retry not idempotent", changed, err)
	}
	_, _, err = registry.DecideAccessRequest(f.path, requests[0].ID, registry.RequestApproved, "24h", "", false, duration.Policy{}, now)
	if code, _ := registry.ErrorCode(err); code != "access_request_decided" {
		t.Fatal(err)
	}
	// A second request can ask for more time, then be denied in one action.
	token, _ = requestForm(t, f)
	form.Set("csrf", token)
	status, b, _ = requestPost(t, f, form, "https://home.tailnet.ts.net")
	if status != 303 {
		t.Fatal(status, b)
	}
	requests, err = registry.ListAccessRequests(f.path, now)
	if err != nil || len(requests) != 2 {
		t.Fatal(requests, err)
	}
	denied, changed, err := registry.DecideAccessRequest(f.path, requests[1].ID, registry.RequestDenied, "", "<no>", false, duration.Policy{}, now)
	if err != nil || !changed || denied.Status != "denied" {
		t.Fatal(denied, err)
	}
	_, body = requestForm(t, f)
	if !strings.Contains(body, "declined") || !strings.Contains(body, "&lt;no&gt;") {
		t.Fatal(body)
	}
	_, changed, err = registry.DecideAccessRequest(f.path, requests[1].ID, registry.RequestDenied, "", "<no>", false, duration.Policy{}, now)
	if err != nil || changed {
		t.Fatal(changed, err)
	}
	_, _, err = registry.DecideAccessRequest(f.path, requests[1].ID, registry.RequestApproved, "8h", "", false, duration.Policy{}, now)
	if code, _ := registry.ErrorCode(err); code != "access_request_decided" {
		t.Fatal(err)
	}
	// Reconstruct the entire listener from persisted files: no request cache.
	restartRequestPortal(t, &f, reg.Portal)

	if got := f.apps(t); len(got) != 2 {
		t.Fatal("restart lost grants", got)
	}
}

func TestAccessRequestCSRFAndHiddenApps(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	valid, _ := requestForm(t, f)
	for _, tc := range []struct {
		name, token, app, origin string
		status                   int
	}{
		{"missing token", "", "secret-payroll", "https://home.tailnet.ts.net", 403},
		{"forged token", valid + "0", "secret-payroll", "https://home.tailnet.ts.net", 403},
		{"missing origin", valid, "secret-payroll", "", 403},
		{"wrong origin", valid, "secret-payroll", "https://evil.test", 403},
		{"http origin", valid, "secret-payroll", "http://home.tailnet.ts.net", 403},
		{"non-requestable", valid, "photos", "https://home.tailnet.ts.net", 403},
		{"hidden name", valid, "canary-hidden", "https://home.tailnet.ts.net", 403},
		{"control", valid, "secret-payroll", "https://home.tailnet.ts.net", 303},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, b, _ := requestPost(t, f, url.Values{"csrf": {tc.token}, "app": {tc.app}}, tc.origin)
			if code != tc.status {
				t.Fatal(code, b)
			}
		})
	}
	// Tokens belong to one WhoIs identity and expire at equality.
	f.who.Store(requestMember("bob"))
	code, b, _ := requestPost(t, f, url.Values{"csrf": {valid}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if code != 403 {
		t.Fatal(code, b)
	}
	f.who.Store(requestMember("alice"))
	f.now.Add(int64(2 * time.Hour))
	code, b, _ = requestPost(t, f, url.Values{"csrf": {valid}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if code != 403 {
		t.Fatal(code, b)
	}
	requestableApp(t, f, "secret-payroll", false)
	_, page := f.request(t, "GET", "/api/apps", "home.tailnet.ts.net", "", nil)
	if strings.Contains(page, "secret-payroll") {
		t.Fatal("disabled requestable app leaked request history", page)
	}
}

func TestAccessRequestListenerRateAndExpiry(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	for _, name := range []string{"one", "two", "three", "four", "five", "six"} {
		requestableApp(t, f, name, true)
	}
	token, _ := requestForm(t, f)
	for i, name := range []string{"one", "two", "three", "four", "five", "six"} {
		code, b, _ := requestPost(t, f, url.Values{"csrf": {token}, "app": {name}}, "https://home.tailnet.ts.net")
		want := 303
		if i == 5 {
			want = 429
		}
		if code != want {
			t.Fatal(name, code, b)
		}
	}
	f.now.Add(int64(7 * 24 * time.Hour))
	_, body := requestForm(t, f)
	if !strings.Contains(body, "timed out") {
		t.Fatal(body)
	}
	requests, err := registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
	if err != nil || len(requests) != 5 {
		t.Fatal(requests, err)
	}
	for _, r := range requests {
		if r.Status != "expired" {
			t.Fatal(r)
		}
	}
	f.now.Add(-int64(24 * time.Hour))
	requests, err = registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
	if err != nil || requests[0].Status != "expired" {
		t.Fatal("rollback revived request", requests, err)
	}
}

func TestAccessRequestHumanMembersOnly(t *testing.T) {
	f := newPortalFixture(t)
	requestableApp(t, f, "secret-payroll", true)
	for _, kind := range []string{"tag", "sharee", "sharer", "missing node", "invalid login"} {
		t.Run(kind, func(t *testing.T) {
			who := requestMember("alice")
			switch kind {
			case "tag":
				who.Node.Tags = []string{"tag:robot"}
			case "sharee":
				who.Node.Hostinfo = (&tailcfg.Hostinfo{ShareeNode: true}).View()
			case "sharer":
				who.Node.Sharer = 42
			case "missing node":
				who.Node = nil
			case "invalid login":
				who.UserProfile.LoginName = "bad\x1b"
			}
			f.who.Store(who)
			code, b, _ := requestPost(t, f, url.Values{"app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
			if code != 403 {
				t.Fatal(code, b)
			}
		})
	}
	f.who.Store(requestMember("alice"))
	token, _ := requestForm(t, f)
	code, b, _ := requestPost(t, f, url.Values{"csrf": {token}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if code != 303 {
		t.Fatal(code, b)
	}
}

func TestAccessRequestWebhookAndEventStream(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	// Restart portal so it captures the isolated notifier configuration.
	receivedEvents := make(chan health.Event, 1)
	webhook := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e health.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			t.Error(err)
		}
		receivedEvents <- e
		w.WriteHeader(204)
	})
	notifyServer := httptest.NewServer(webhook)
	defer notifyServer.Close()
	config, _ := json.Marshal(health.NotifierConfig{Webhook: notifyServer.URL})
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.path), health.ConfigFile), config, 0600); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	restartRequestPortal(t, &f, reg.Portal)
	streamServer := mcpEventsTestServer(t, f.s.events, func(context.Context) (any, error) {
		r, err := registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
		ev := []registry.RequestEvent{}
		for _, v := range r {
			ev = append(ev, v.Event())
		}
		return ev, err
	})
	defer streamServer.Close()
	frames, closeStream := mcpEventsOpenStream(t, streamServer)
	defer closeStream()
	first := readMCPEventFrame(t, frames)
	if first.Event != "snapshot" {
		t.Fatal(first)
	}
	token, _ := requestForm(t, f)
	status, b, h := requestPost(t, f, url.Values{"csrf": {token}, "app": {"secret-payroll"}, "duration": {"3d"}, "note": {"never-push-secret"}}, "https://home.tailnet.ts.net")
	if status != 303 || h.Get("X-TSLink-Notification") != "sent" {
		t.Fatal(status, b, h)
	}
	update := readMCPEventFrame(t, frames)
	encoded, _ := json.Marshal(update)
	if update.Event != "update" || !bytes.Contains(encoded, []byte("secret-payroll")) || bytes.Contains(encoded, []byte("never-push-secret")) {
		t.Fatal(string(encoded))
	}
	received := <-receivedEvents
	if received.Kind != "access_requested" || received.Request == nil || received.Request.ID == "" || received.Request.Who != "alice" || received.Request.RequestedDuration != "3d" {
		t.Fatal(received)
	}
}

func restartRequestPortal(t *testing.T, f *portalFixture, p *registry.PortalConfig) {
	t.Helper()
	f.s.closePortal()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f.fake.ln = ln
	f.addr = ln.Addr().String()
	f.s.syncPortal(t.Context(), p)
	select {
	case <-f.s.portalRun.done:
	case <-time.After(3 * time.Second):
		t.Fatal("restart stalled")
	}
	if f.s.portalState.State != "running" {
		t.Fatal(f.s.portalState)
	}
}

func TestAccessRequestCommandNotification(t *testing.T) {
	if file := os.Getenv("TSLINK_REQUEST_NOTIFY_FILE"); file != "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(2)
		}
		if os.Getenv("TSLINK_ALERT_KIND") != "access_requested" || os.Getenv("TSLINK_ALERT_SERVICE") != "secret-payroll" || string(data) != os.Getenv("TSLINK_ALERT_JSON")+"\n" {
			os.Exit(3)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	file := filepath.Join(t.TempDir(), "notification.json")
	t.Setenv("TSLINK_REQUEST_NOTIFY_FILE", file)
	c := health.NotifierConfig{Command: []string{os.Args[0], "-test.run=^TestAccessRequestCommandNotification$"}}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.path), health.ConfigFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	old := accessRequestRecordedFn
	t.Cleanup(func() { accessRequestRecordedFn = old })
	recorded := make(chan registry.RequestEvent, 2)
	accessRequestRecordedFn = func(e registry.RequestEvent) { recorded <- e }
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	restartRequestPortal(t, &f, reg.Portal)
	token, _ := requestForm(t, f)
	form := url.Values{"csrf": {token}, "app": {"secret-payroll"}, "duration": {"3d"}, "note": {"notification-secret-canary"}}
	code, b, h := requestPost(t, f, form, "https://home.tailnet.ts.net")
	if code != 303 || h.Get("X-TSLink-Notification") != "sent" {
		t.Fatal(code, b, h)
	}
	got, err := os.ReadFile(file)
	if err != nil || bytes.Contains(got, []byte("notification-secret-canary")) {
		t.Fatal(string(got), err)
	}
	var event health.Event
	if json.Unmarshal(got, &event) != nil || event.Request == nil || event.Request.RequestedDuration != "3d" {
		t.Fatal(string(got))
	}
	select {
	case e := <-recorded:
		if e.ID != event.Request.ID {
			t.Fatal(e, event)
		}
	default:
		t.Fatal("missing F4 request hook")
	}
	code, b, _ = requestPost(t, f, form, "https://home.tailnet.ts.net")
	if code != 409 {
		t.Fatal(code, b)
	}
	select {
	case e := <-recorded:
		t.Fatal("duplicate recorded", e)
	default:
	}
}

func TestAccessRequestNotificationFailureAndPartialBody(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	calls := make(chan struct{}, 2)
	notify := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls <- struct{}{}; w.WriteHeader(503) }))
	defer notify.Close()
	c, _ := json.Marshal(health.NotifierConfig{Webhook: notify.URL + "/private?secret=fixture"})
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.path), health.ConfigFile), c, 0600); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	restartRequestPortal(t, &f, reg.Portal)
	token, _ := requestForm(t, f)
	form := url.Values{"csrf": {token}, "app": {"secret-payroll"}}
	status, b, h := requestPost(t, f, form, "https://home.tailnet.ts.net")
	if status != 303 || h.Get("X-TSLink-Notification") != "failed" || strings.Contains(b, "secret=fixture") {
		t.Fatal(status, b, h)
	}
	<-calls
	requests, err := registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
	if err != nil || len(requests) != 1 || requests[0].Status != "pending" {
		t.Fatal(requests, err)
	}
	status, b, _ = requestPost(t, f, form, "https://home.tailnet.ts.net")
	if status != 409 {
		t.Fatal(status, b)
	}
	select {
	case <-calls:
		t.Fatal("duplicate resent notification")
	default:
	}
	// Cut a real request body short. No parser failure can create a request.
	conn, err := net.Dial("tcp", f.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	fmt.Fprintf(conn, "POST /access-requests HTTP/1.1\r\nHost: home.tailnet.ts.net\r\nOrigin: https://home.tailnet.ts.net\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\nConnection: close\r\n\r\ncsrf=partial")
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.CloseWrite()
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal(response.StatusCode)
	}
	requests, err = registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
	if err != nil || len(requests) != 1 {
		t.Fatal(requests, err)
	}
	status, b, _ = requestPost(t, f, url.Values{"csrf": {token}, "app": {"photos"}, "note": {strings.Repeat("x", 9000)}}, "https://home.tailnet.ts.net")
	if status != 400 {
		t.Fatal(status, b)
	}
}

func TestAccessRequestMalformedHiddenAppKeepsPortal(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "photos", true)
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services = append(reg.Services, registry.Service{Name: "hidden-broken-app", Type: "invalid"})
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	response, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if response.StatusCode != 200 || !strings.Contains(body, "Open photos") || strings.Contains(body, "hidden-broken-app") || !strings.Contains(body, "Requests are temporarily unavailable") {
		t.Fatal("invalid hidden app blocked the valid app directory", response.StatusCode, body)
	}
	after, err := os.ReadFile(f.path)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("rendering dropped the invalid service", err)
	}
}

func TestAccessRequestListenerFormAndInboxFailures(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	token, _ := requestForm(t, f)
	for _, form := range []url.Values{
		{"csrf": {token}, "app": {"secret-payroll"}, "unknown": {"field"}},
		{"csrf": {token, token}, "app": {"secret-payroll"}},
	} {
		if status, b, _ := requestPost(t, f, form, "https://home.tailnet.ts.net"); status != 400 {
			t.Fatal(status, b)
		}
	}
	r, err := http.NewRequest("POST", "http://"+f.addr+PortalRequestPath, strings.NewReader("app=secret-payroll"))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "home.tailnet.ts.net"
	r.Header.Set("Origin", "https://home.tailnet.ts.net")
	r.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 {
		t.Fatal(resp.StatusCode)
	}
	// Invalid notifier configuration does not prevent a durable request.
	if err = os.WriteFile(filepath.Join(filepath.Dir(f.path), health.ConfigFile), []byte(`{"command":[`), 0600); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	restartRequestPortal(t, &f, reg.Portal)
	token, _ = requestForm(t, f)
	status, b, h := requestPost(t, f, url.Values{"csrf": {token}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if status != 303 || h.Get("X-TSLink-Notification") != "failed" {
		t.Fatal(status, b, h)
	}
	reg, _, err = registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Requests = nil
	for i := range 1000 {
		reg.Requests = append(reg.Requests, registry.AccessRequest{ID: fmt.Sprintf("%032x", i), Who: fmt.Sprintf("u%d", i), App: "photos", Status: registry.RequestPending, CreatedAt: time.Unix(0, f.now.Load()).Add(-2 * time.Hour)})
	}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.path, data, 0600); err != nil {
		t.Fatal(err)
	}
	token, body := requestForm(t, f)
	if strings.Contains(body, "Your requests") {
		t.Fatal("other visitors' requests disclosed", body)
	}
	status, b, _ = requestPost(t, f, url.Values{"csrf": {token}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if status != 503 || !strings.Contains(b, "access_request_capacity") {
		t.Fatal(status, b)
	}
	if _, err = registry.RemovePerson(f.path, "alice"); err != nil {
		t.Fatal(err)
	}
	status, b, _ = requestPost(t, f, url.Values{"csrf": {token}, "app": {"secret-payroll"}}, "https://home.tailnet.ts.net")
	if status != 403 {
		t.Fatal(status, b)
	}
}

func TestRequestMCPCallerThroughAuth(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("owner"))
	if _, ok := MCPCallerFromContext(context.Background()); ok {
		t.Fatal("unauthenticated context acquired identity")
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := MCPCallerFromContext(r.Context())
		if !ok || c.Login != "owner" || len(c.Tags) != 0 {
			t.Error(c, ok)
		}
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(MCPAuthMiddleware([]string{"owner"}, f.fake.localClient, next))
	defer srv.Close()
	response, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
}
