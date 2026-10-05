package server

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestGuestRecipientPages(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN, en;q=0.5", "en;q=1, zh;q=0.2"} {
		t.Run(lang, func(t *testing.T) {
			f := newGuestFixture(t, "", false, true)
			get := func(path string) (*http.Response, string) {
				req, _ := http.NewRequest("GET", f.base+path, nil)
				req.Header.Set("Accept-Language", lang)
				resp, err := f.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				return resp, string(raw)
			}
			resp, invalid := get("/guest/" + strings.Repeat("x", 43))
			if lang == "en" {
				guestArtifact(t, "401.html", []byte(invalid))
			}
			if resp.StatusCode != 401 || !strings.Contains(invalid, "Reopen the original link or ask the person who sent it.") || strings.Contains(invalid, "photos") {
				t.Fatal("generic recipient guidance missing")
			}
			if strings.Contains(invalid, "请重新打开") != strings.HasPrefix(lang, "zh") {
				t.Fatal("language preference ignored", lang)
			}
			f.now.Store(f.grant.ExpiresAt.UnixNano())
			resp, expired := get("/guest/" + f.token)
			if resp.StatusCode != 401 || expired != invalid {
				t.Fatal("expiry page differs")
			}
			f.now.Store(accessTestTime.UnixNano())
			if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
				t.Fatal(err)
			}
			resp, revoked := get("/guest/" + f.token)
			if resp.StatusCode != 401 || revoked != invalid {
				t.Fatal("revocation page differs")
			}
		})
	}
	f := newGuestFixture(t, "975310", false, true)
	form, body := f.healthyRequest("/guest/"+f.token, nil)
	guestArtifact(t, "pin.html", []byte(body))
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(body)
	if len(csrf) != 2 {
		t.Fatal("PIN control missing")
	}
	if !strings.Contains(body, "min-height:44px") {
		t.Fatal("PIN touch target size missing")
	}
	for i := range 5 {
		req, _ := http.NewRequest("POST", f.base+"/guest/pin", strings.NewReader(url.Values{"csrf": {csrf[1]}, "pin": {"111111"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept-Language", "zh-CN, en;q=0.5")
		for _, cookie := range form.Cookies() {
			req.AddCookie(cookie)
		}
		resp, err := f.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		body := string(raw)
		if i < 4 {
			if resp.StatusCode != 200 || (!strings.Contains(body, "Please try again.") || !strings.Contains(body, "请重试")) || !strings.Contains(body, `action="/guest/pin"`) || strings.Contains(body, "remaining") {
				t.Fatal("PIN retry page missing or discloses attempts")
			}
		} else if resp.StatusCode != 401 || strings.Contains(body, "name=\"pin\"") || !strings.Contains(body, "Reopen the original link") {
			t.Fatal("lockout page differs")
		}
	}
}

func TestGuestReadFailureRetainsSession(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	// Keep the background counter writer out of this read-failure experiment.
	// Requests can share this lock; a restore notification cannot start a flush
	// between restoring valid bytes and checking the original session.
	lock, err := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if ok, err := filelock.TryReadLock(lock); err != nil || !ok {
		t.Fatalf("read-failure fixture lock: acquired=%t err=%v", ok, err)
	}
	defer filelock.Unlock(lock)
	cookies := f.login()
	raw, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(f.path, []byte(`{"guests":`), 0600); err != nil {
		t.Fatal(err)
	}
	resp, body := f.request("GET", "/", "", cookies)
	// The response-time contract is checked with virtual time in
	// TestGuestReadFailureVirtualBoundAndSessionRecovery.
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") != "1" || strings.Contains(body, "photos") || f.hits.Load() != 0 {
		t.Fatal("temporary read failure response")
	}
	if err = os.WriteFile(f.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	resp, _ = f.request("GET", "/", "", cookies)
	if resp.StatusCode != 204 {
		t.Fatal("read failure discarded session", resp.StatusCode)
	}
}

func TestGuestPeriodicAndShutdownCounters(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	cookies := f.login()
	releaseRead := f.holdCounterFlush()
	defer func() {
		if releaseRead != nil {
			releaseRead()
		}
	}()
	resp, _ := f.request("GET", "/", "", cookies)
	if resp.StatusCode != 204 {
		t.Fatal("app control")
	}
	// Inspect disk directly: in-process views also include the pending batch.
	persisted := func() uint64 {
		reg, _, err := registry.Preflight(f.path)
		if err != nil {
			t.Fatal(err)
		}
		return reg.Guests[0].Uses
	}
	if persisted() != 0 {
		t.Fatal("counter written per request")
	}
	releaseRead()
	releaseRead = nil
	// The 30s period itself is pinned in virtual time by
	// TestGuestMonitorFlushOwnershipAndSessionRecovery.
	testwait.Until(t, "periodic counter batch persisted", func() bool { return persisted() != 0 })
	resp, _ = f.healthyRequest("/", cookies)
	if resp.StatusCode != 204 {
		t.Fatal("second app control")
	}
	f.s.stopNodeLocked("photos")
	if persisted() != 2 {
		t.Fatal("shutdown counter batch missing")
	}
}
