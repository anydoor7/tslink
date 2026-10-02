package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m, func() int { return testenv.RunWithNonLoopbackDialGuard(m.Run, "internal/health") }))
}

func TestProbeHTTPUsesProxyTargetAndBusinessBody(t *testing.T) {
	var path, query string
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.EscapedPath(), r.URL.RawQuery
		io.WriteString(w, `{"ready":true}`)
	}))
	defer app.Close()
	svc := registry.Service{Type: registry.TypeProxy, Target: app.URL + "/base?token=private", Health: &registry.HealthConfig{Path: "/health", BodyContains: `"ready":true`}}
	if code := Probe(context.Background(), svc); code != "" {
		t.Fatal(code)
	}
	if path != "/base/health" || query != "token=private" {
		t.Fatalf("wrong backend target: %s %s", path, query)
	}
	svc.Health.BodyContains = "missing-secret"
	if code := Probe(context.Background(), svc); code != "health_body_mismatch" {
		t.Fatalf("code=%q", code)
	}
	// A listening TCP backend is not enough to pass the HTTP probe.
	svc.Health.BodyContains = ""
	svc.Health.StatusMin, svc.Health.StatusMax = 500, 599
	if code := Probe(context.Background(), svc); code != "health_status_mismatch" {
		t.Fatalf("code=%q", code)
	}
}

func TestProbeHTTPBoundedNoRedirects(t *testing.T) {
	requests := 0
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/secret", http.StatusFound)
	}))
	defer app.Close()
	svc := registry.Service{Type: registry.TypeProxy, Target: app.URL}
	if code := Probe(context.Background(), svc); code != "health_status_mismatch" || requests != 1 {
		t.Fatalf("%s requests=%d", code, requests)
	}
	svc.Health = &registry.HealthConfig{StatusMin: 300, StatusMax: 399}
	if code := Probe(context.Background(), svc); code != "" {
		t.Fatal(code)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	svc.Target = slow.URL
	svc.Health = &registry.HealthConfig{Timeout: "100ms"}
	if code := Probe(context.Background(), svc); code != "health_timeout" {
		t.Fatalf("code=%q", code)
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, strings.Repeat("x", 64<<10)+"secret") }))
	defer large.Close()
	svc.Target = large.URL
	svc.Health = &registry.HealthConfig{BodyContains: "secret"}
	if code := Probe(context.Background(), svc); code != "health_body_mismatch" {
		t.Fatalf("code=%q", code)
	}
}

func TestProbeErrorsAndFileTCP(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		svc  registry.Service
		code string
	}{
		{registry.Service{Type: registry.TypeProxy, Target: "%secret"}, "health_target_invalid"},
		{registry.Service{Type: registry.TypeProxy, Target: "file:///secret"}, "health_target_invalid"},
		{registry.Service{Type: registry.TypeProxy, Target: "http://127.0.0.1:1/secret"}, "health_request_failed"},
		{registry.Service{Type: registry.TypeProxy, Health: &registry.HealthConfig{Path: "//secret"}}, "health_config_invalid"},
		{registry.Service{Type: registry.TypeTCP, Target: "127.0.0.1:1"}, "health_tcp_failed"},
		{registry.Service{Type: registry.TypeFile, Path: filepath.Join(t.TempDir(), "absent")}, "health_file_unavailable"},
		{registry.Service{Type: "other"}, "health_target_invalid"},
	} {
		if code := Probe(ctx, tc.svc); code != tc.code {
			t.Fatalf("got %q want %q", code, tc.code)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if code := Probe(ctx, registry.Service{Type: registry.TypeTCP, Target: ln.Addr().String()}); code != "" {
		t.Fatal(code)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "app.txt")
	if err := os.WriteFile(file, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, svc := range []registry.Service{{Type: registry.TypeFile, Path: dir}, {Type: registry.TypeFile, Path: dir, File: "app.txt"}} {
		if code := Probe(ctx, svc); code != "" {
			t.Fatal(code)
		}
	}
	if code := Probe(ctx, registry.Service{Type: registry.TypeFile, Path: file}); code != "health_file_invalid" {
		t.Fatal(code)
	}
}

func TestHealthFlappingRecoveryRestartAndDedup(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), StateFile)
	r := NewRecorder(path, NotifierConfig{Command: []string{"/private/secret", "hidden-arg"}})
	sent := 0
	r.Send = func(context.Context, NotifierConfig, Event) error { sent++; return nil }
	states := []string{Degraded, Healthy, Degraded, Degraded, Down, Down, Healthy, Degraded, Degraded, Down, Healthy}
	for i, want := range states {
		code := "failed"
		if want == Healthy {
			code = ""
		}
		now = now.Add(30 * time.Second)
		h := Result(r.Previous("app", "id"), "proxy", code, now)
		if h.State != want {
			t.Fatalf("step %d: %+v want %s", i, h, want)
		}
		r.Commit(context.Background(), r.ObserveHealth("app", "id", h, now), now)
		if i == 5 {
			r = NewRecorder(path, r.Config)
			r.Send = func(context.Context, NotifierConfig, Event) error { sent++; return nil }
		}
	}
	if len(r.State.Events) != 4 || sent != 2 {
		t.Fatalf("events=%+v deliveries=%d", r.State.Events, sent)
	}
	for i, want := range []string{"app_down", "app_recovered", "app_down", "app_recovered"} {
		if r.State.Events[i].Kind != want {
			t.Fatal(r.State.Events)
		}
	}
	if r.State.Events[2].Delivery != "rate_limited" || r.State.Events[3].Delivery != "rate_limited" {
		t.Fatal(r.State.Events)
	}
	b, err := json.Marshal(r.View())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "hidden-arg") || strings.Contains(string(b), "/private/secret") {
		t.Fatal(string(b))
	}
	if r.Previous("app", "replacement").State != Unknown {
		t.Fatal("replaced app inherited prior health")
	}
}

func TestAlertGlobalRateLimitPersistsAcrossServices(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{Command: []string{"/private/notifier"}})
	sent := 0
	r.Send = func(context.Context, NotifierConfig, Event) error { sent++; return nil }
	r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "first"}, {Kind: "app_down", Service: "second"}}, now)
	if sent != 1 || len(r.State.Events) != 2 || r.State.Events[1].Delivery != "rate_limited" {
		t.Fatalf("deliveries=%d events=%+v", sent, r.State.Events)
	}
	r = NewRecorder(r.Path, r.Config)
	r.Send = func(context.Context, NotifierConfig, Event) error { sent++; return nil }
	r.Commit(context.Background(), []Event{{Kind: "app_recovered", Service: "second"}}, now.Add(59*time.Second))
	if sent != 1 {
		t.Fatal("restart lost global rate limit", sent)
	}
	r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "third"}}, now.Add(time.Minute))
	if sent != 2 || r.State.Events[3].Delivery != "sent" {
		t.Fatalf("deliveries=%d events=%+v", sent, r.State.Events)
	}
}

func TestExpiryThresholdCrossingsAndRestart(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	deadline := now.Add(30 * 24 * time.Hour)
	path := filepath.Join(t.TempDir(), StateFile)
	r := NewRecorder(path, NotifierConfig{})
	for _, tc := range []struct {
		left    time.Duration
		warning string
		count   int
	}{{15 * 24 * time.Hour, "", 0}, {14 * 24 * time.Hour, "warning_14d", 1}, {4 * 24 * time.Hour, "warning_14d", 1}, {3 * 24 * time.Hour, "critical_3d", 2}, {time.Hour, "critical_3d", 2}, {0, "expired", 3}, {-24 * time.Hour, "expired", 3}} {
		now = deadline.Add(-tc.left)
		e := ExpiryAt(&deadline, "reported", now, []string{"next"})
		if e.Warning != tc.warning {
			t.Fatalf("%+v", e)
		}
		r.Commit(context.Background(), r.ObserveExpiry("app", "node_key", e, now), now)
		if len(r.State.Events) != tc.count {
			t.Fatal(r.State.Events)
		}
		r = NewRecorder(path, NotifierConfig{})
	}
	if e := ExpiryAt(nil, "unavailable", now, nil); e.State != Unknown || e.DaysLeft != nil {
		t.Fatal(e)
	}
	if events := r.ObserveExpiry("app", "node_key", ExpiryAt(nil, "unavailable", now, nil), now); len(events) != 0 {
		t.Fatal(events)
	}
	newDeadline := now.Add(2 * 24 * time.Hour)
	if events := r.ObserveExpiry("app", "node_key", ExpiryAt(&newDeadline, "reported", now, nil), now); len(events) != 1 {
		t.Fatal(events)
	}
}

func TestNotifierWebhookAndMaskedErrors(t *testing.T) {
	var got Event
	var contentType string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		if json.NewDecoder(r.Body).Decode(&got) != nil {
			t.Error("invalid payload")
		}
		w.WriteHeader(204)
	}))
	defer endpoint.Close()
	e := Event{ID: 1, Kind: "app_down", Service: "app"}
	if err := Notify(context.Background(), NotifierConfig{Webhook: endpoint.URL + "/private?token=secret"}, e); err != nil {
		t.Fatal(err)
	}
	if got.Kind != e.Kind || contentType != "application/json" {
		t.Fatalf("%+v %s", got, contentType)
	}
	for _, c := range []NotifierConfig{{Webhook: "http://127.0.0.1:1/private?token=secret"}, {Command: []string{filepath.Join(t.TempDir(), "secret"), "token=secret"}}} {
		err := Notify(context.Background(), c, e)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("error %v", err)
		}
	}
	if err := Notify(context.Background(), NotifierConfig{}, e); err != nil {
		t.Fatal(err)
	}
}

func TestNotifierCommandStructuredInput(t *testing.T) {
	// Use the test binary as the actual executable, so the contract also runs
	// on Windows without requiring a shell or platform-specific permissions.
	if os.Getenv("TSLINK_HEALTH_TEST_CHILD") == "1" {
		b, _ := io.ReadAll(os.Stdin)
		payload, _ := json.Marshal(map[string]string{"stdin": string(b), "json": os.Getenv("TSLINK_ALERT_JSON"), "kind": os.Getenv("TSLINK_ALERT_KIND"), "service": os.Getenv("TSLINK_ALERT_SERVICE")})
		if err := os.WriteFile(os.Getenv("TSLINK_HEALTH_TEST_OUTPUT"), payload, 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	t.Setenv("TSLINK_HEALTH_TEST_CHILD", "1")
	path := filepath.Join(t.TempDir(), "received.json")
	t.Setenv("TSLINK_HEALTH_TEST_OUTPUT", path)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := Notify(context.Background(), NotifierConfig{Command: []string{exe, "-test.run=^TestNotifierCommandStructuredInput$"}}, Event{Kind: "app_recovered", Service: "app"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if json.Unmarshal(b, &got) != nil || got["kind"] != "app_recovered" || got["service"] != "app" || got["stdin"] != got["json"]+"\n" || !strings.Contains(got["json"], `"kind":"app_recovered"`) {
		t.Fatal(string(b))
	}
}

func TestAlertConfigValidationAndPersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ConfigFile)
	if c, err := LoadNotifier(path); err != nil || c.Kind() != "none" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, raw := range []string{`{`, `{"webhook":"https://host/secret","command":["/bin/secret"]}`, `{"command":["relative"]}`, `{"webhook":"file:///secret"}`, `{"unknown":"secret"}`, `{} {}`} {
		if os.WriteFile(path, []byte(raw), 0600) != nil {
			t.Fatal("write")
		}
		if _, err := LoadNotifier(path); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
	if err := os.WriteFile(path, []byte(`{"webhook":"https://host/secret"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadNotifier(path)
	if err != nil || c.Kind() != "webhook" {
		t.Fatalf("%+v %v", c, err)
	}
	r := NewRecorder(dir, c)
	sent := 0
	r.Send = func(context.Context, NotifierConfig, Event) error { sent++; return nil }
	r.Commit(context.Background(), []Event{{Kind: "app_down"}}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if r.Error != "alert_state_write_failed" || sent != 0 {
		t.Fatalf("%+v sent=%d", r.View(), sent)
	}
}

func TestAlertFailurePathsAndJournalBound(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := LoadNotifier(dir); err == nil || err.Error() != "alert_config_unreadable" {
		t.Fatal(err)
	}
	path := filepath.Join(dir, StateFile)
	if err := os.WriteFile(path, []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if r := NewRecorder(path, NotifierConfig{}); r.Error != "alert_state_invalid" {
		t.Fatal(r.Error)
	}
	if r := NewRecorder(dir, NotifierConfig{}); r.Error != "alert_state_unreadable" {
		t.Fatal(r.Error)
	}
	r := NewRecorder(filepath.Join(dir, "journal.json"), NotifierConfig{Webhook: "https://fake.invalid/secret"})
	r.Send = func(context.Context, NotifierConfig, Event) error { return fmt.Errorf("private-error") }
	events := make([]Event, 101)
	for i := range events {
		events[i] = Event{Kind: "app_down", At: now, Service: fmt.Sprint(i)}
	}
	r.Commit(context.Background(), events, now)
	if len(r.State.Events) != 100 || r.State.Events[0].ID != 2 {
		t.Fatal(len(r.State.Events), r.State.Events[0])
	}
	r.Commit(context.Background(), []Event{{Kind: "app_recovered", At: now.Add(time.Minute)}}, now.Add(time.Minute))
	if got := r.State.Events[len(r.State.Events)-1].Delivery; got != "failed" {
		t.Fatal(got)
	}
	r.Send = func(context.Context, NotifierConfig, Event) error { r.Path = dir; return nil }
	r.Commit(context.Background(), []Event{{Kind: "expiry_threshold", At: now.Add(2 * time.Minute)}}, now.Add(2*time.Minute))
	if r.Error != "alert_state_write_failed" {
		t.Fatal(r.Error)
	}
	badTime := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := Notify(context.Background(), NotifierConfig{}, Event{At: badTime}); err == nil || err.Error() != "alert_payload_invalid" {
		t.Fatal(err)
	}
	r = NewRecorder(filepath.Join(dir, "bad-time.json"), NotifierConfig{})
	r.Commit(context.Background(), []Event{{At: badTime}}, now)
	if r.Error != "alert_state_write_failed" {
		t.Fatal(r.Error)
	}
}

func TestWebhookRefusesRedirectAndHTTPFailure(t *testing.T) {
	var forwarded int
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++ }))
	defer destination.Close()
	for _, status := range []int{http.StatusFound, http.StatusInternalServerError} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", destination.URL)
			w.WriteHeader(status)
		}))
		err := Notify(context.Background(), NotifierConfig{Webhook: origin.URL + "/secret"}, Event{Kind: "app_down"})
		origin.Close()
		if err == nil || err.Error() != "alert_webhook_failed" {
			t.Fatal(err)
		}
	}
	if forwarded != 0 {
		t.Fatal("redirect received private event")
	}
	if err := Notify(context.Background(), NotifierConfig{Webhook: "%secret"}, Event{}); err == nil || err.Error() != "alert_webhook_failed" {
		t.Fatal(err)
	}
}

func TestProbeRejectsTruncatedBody(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		io.WriteString(w, "ready")
	}))
	defer app.Close()
	if code := Probe(context.Background(), registry.Service{Type: registry.TypeProxy, Target: app.URL, Health: &registry.HealthConfig{BodyContains: "ready"}}); code != "health_body_read_failed" {
		t.Fatal(code)
	}
}
