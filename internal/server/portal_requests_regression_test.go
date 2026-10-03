package server

import (
	"encoding/json"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPortalRequestBrowserFixtures(t *testing.T) {
	for _, kind := range []string{"normal", "long-name"} {
		t.Run(kind, func(t *testing.T) {
			f := newPortalFixture(t)
			f.who.Store(requestMember("alice"))
			app := "family-photos"
			if kind == "long-name" {
				app = strings.Repeat("w", 63)
			}
			requestableApp(t, f, app, true)
			now := time.Unix(0, f.now.Load())
			r, err := registry.SubmitAccessRequest(f.path, "alice", app, "3d", strings.Repeat("W", 500), now)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = registry.DecideAccessRequest(f.path, r.ID, registry.RequestDenied, "", "Please ask again tomorrow.", false, duration.Policy{}, now); err != nil {
				t.Fatal(err)
			}
			resp, body := f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
			if resp.StatusCode != 200 {
				t.Fatal(resp.StatusCode)
			}
			out := os.Getenv("PORTAL_REQUEST_BROWSER_OUT")
			if out == "" {
				return
			}
			if err := os.WriteFile(filepath.Join(out, "portal-"+kind+".html"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(resp.Header)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(out, "portal-"+kind+"-headers.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPortalRequestDurationOptions(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	for _, value := range []string{"1h", "1d", "3d", "7d"} {
		requestableApp(t, f, "request-"+value, true)
	}
	token, body := requestForm(t, f)
	for _, option := range []struct{ value, label string }{
		{"1h", "1 hour / 1 小时"}, {"1d", "1 day / 1 天"}, {"3d", "3 days / 3 天"}, {"7d", "7 days / 7 天"},
	} {
		t.Run(option.value, func(t *testing.T) {
			if !strings.Contains(body, `<option value="`+option.value+`">`+option.label+`</option>`) {
				t.Fatal("labelled duration option missing", option.value, body)
			}
			code, b, _ := requestPost(t, f, url.Values{"csrf": {token}, "app": {"request-" + option.value}, "duration": {option.value}}, "https://home.tailnet.ts.net")
			if code != 303 {
				t.Fatal(code, b)
			}
		})
	}
	rows, err := registry.ListAccessRequests(f.path, time.Unix(0, f.now.Load()))
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	for i, value := range []string{"1h", "1d", "3d", "7d"} {
		if rows[i].RequestedDuration != value {
			t.Fatal(rows)
		}
	}
}

func TestPortalRequestMaintenanceContentionKeepsApps(t *testing.T) {
	f := newPortalFixture(t)
	f.who.Store(requestMember("alice"))
	requestableApp(t, f, "secret-payroll", true)
	now := time.Unix(0, f.now.Load())
	if _, err := registry.SubmitAccessRequest(f.path, "alice", "secret-payroll", "3d", "", now.Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = filelock.Unlock(lock) })
	resp, body := f.request(t, "GET", "/api/apps", "home.tailnet.ts.net", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"name":"photos"`) || !strings.Contains(body, `"request_error":"Requests are temporarily unavailable.`) || strings.Contains(body, `"status":"expired"`) {
		t.Fatal(resp.StatusCode, body)
	}
	resp, body = f.request(t, "GET", "/", "home.tailnet.ts.net", "", nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "Open photos") || !strings.Contains(body, "Requests are temporarily unavailable.") {
		t.Fatal(resp.StatusCode, body)
	}
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	_, body = requestForm(t, f)
	if !strings.Contains(body, "This request timed out.") {
		t.Fatal(body)
	}
	reg, _, err := registry.Preflight(f.path)
	if err != nil {
		t.Fatal(err)
	}
	restartRequestPortal(t, &f, reg.Portal)
	_, body = requestForm(t, f)
	if !strings.Contains(body, "This request timed out.") {
		t.Fatal("restart lost expiry", body)
	}
}
