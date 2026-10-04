package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/coder/websocket"
)

func guestArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	if dir := os.Getenv("GUEST_TEST_OUTPUT"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func replaceGuestBackend(t *testing.T, f *guestFixture, h http.Handler) {
	t.Helper()
	b := httptest.NewServer(h)
	t.Cleanup(b.Close)
	f.s.stopNodeLocked("photos")
	f.svc.Target = b.URL
	raw, e := os.ReadFile(f.path)
	if e != nil {
		t.Fatal(e)
	}
	var reg registry.Registry
	if e = json.Unmarshal(raw, &reg); e != nil {
		t.Fatal(e)
	}
	reg.Services[0] = f.svc
	raw, e = json.Marshal(reg)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	f.start()
}
func guestRequest(client *http.Client, base, path string, cookies []*http.Cookie) (int, error) {
	r, e := http.NewRequest("GET", base+path, nil)
	if e != nil {
		return 0, e
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	resp, e := client.Do(r)
	if e != nil {
		return 0, e
	}
	_, e = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, e
}
func TestGuestAssetPage(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, parallel := range []int{1, 6, 50} {
			t.Run(fmt.Sprintf("h2=%t/parallel=%d", h2, parallel), func(t *testing.T) {
				f := newGuestFixture(t, "", h2, true)
				cookies := f.login()
				// Pure asset reads: retain ownership across the concurrent batch.
				defer f.holdCounterFlush()()
				// Warm the connection and verify that the exact session reaches the backend.
				if status, e := guestRequest(f.client, f.base, "/control", cookies); e != nil || status != 204 {
					t.Fatalf("control: %d %v", status, e)
				}
				start := time.Now()
				var mu sync.Mutex
				lat := []float64{}
				counts := map[int]int{}
				errs := 0
				jobs := make(chan int, 50)
				for i := 0; i < 50; i++ {
					jobs <- i
				}
				close(jobs)
				var wg sync.WaitGroup
				for i := 0; i < parallel; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for j := range jobs {
							at := time.Now()
							status, e := guestRequest(f.client, f.base, fmt.Sprintf("/asset-%d", j), cookies)
							mu.Lock()
							counts[status]++
							if e != nil {
								errs++
							}
							lat = append(lat, float64(time.Since(at).Microseconds())/1000)
							mu.Unlock()
						}
					}()
				}
				wg.Wait()
				elapsed := time.Since(start).Seconds()
				sort.Float64s(lat)
				after, e := guestRequest(f.client, f.base, "/after", cookies)
				if e != nil {
					t.Fatal(e)
				}
				data := map[string]any{"http2": h2, "parallel": parallel, "requests": 50, "statuses": counts, "errors": errs, "seconds": elapsed, "attempts_per_second": 50 / elapsed, "successful_per_second": float64(counts[204]) / elapsed, "p50_ms": lat[25], "p95_ms": lat[47], "max_ms": lat[49], "after_status": after}
				raw, _ := json.Marshal(data)
				t.Log(string(raw))
				guestArtifact(t, fmt.Sprintf("assets-h2-%t-parallel-%d.json", h2, parallel), raw)
				if counts[204] != 50 || after != 204 {
					t.Errorf("valid session should serve all assets and remain usable: statuses=%v after=%d", counts, after)
				}
			})
		}
	}
}
func TestGuestWriterRecovery(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	cookies := f.login()
	release := f.holdCounterFlush()
	if status, e := guestRequest(f.client, f.base, "/control", cookies); e != nil || status != 204 {
		release()
		t.Fatal("control", status, e)
	}
	release()
	lock, e := os.OpenFile(f.path+".lock", os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = filelock.Lock(lock); e != nil {
		t.Fatal(e)
	}
	during, e := guestRequest(f.client, f.base, "/during", cookies)
	if e != nil {
		t.Fatal(e)
	}
	if e = filelock.Unlock(lock); e != nil {
		t.Fatal(e)
	}
	defer f.holdCounterFlush()()
	after, e := guestRequest(f.client, f.base, "/after", cookies)
	if e != nil {
		t.Fatal(e)
	}
	fresh := f.login()
	renewed, e := guestRequest(f.client, f.base, "/renewed", fresh)
	if e != nil || renewed != 204 {
		t.Fatal("reopen control", renewed, e)
	}
	t.Logf("writer_locked=%d same_session_after_unlock=%d reopened=%d", during, after, renewed)
	if during != 503 || after != 204 {
		t.Error("temporary writer contention should not permanently invalidate the session")
	}
}
func TestGuestStreams(t *testing.T) {
	for _, kind := range []string{"sse", "websocket"} {
		for _, end := range []string{"revoke", "expiry"} {
			t.Run(kind+"/"+end, func(t *testing.T) {
				f := newGuestFixture(t, "", false, true)
				// Keep the shared Funnel lifetime beyond the tested grant deadline.
				_, longerToken, e := registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "photos", Value: "4h", PublicAck: true, Now: accessTestTime})
				if e != nil {
					t.Fatal(e)
				}
				reg, _, e := registry.Preflight(f.path)
				if e != nil {
					t.Fatal(e)
				}
				f.svc = reg.Services[0]
				if f.svc.FunnelExpiresAt == nil || !f.svc.FunnelExpiresAt.After(f.grant.ExpiresAt) {
					t.Fatal("shared listener lifetime control")
				}
				release := make(chan struct{})
				defer close(release)
				var once sync.Once
				send := make(chan struct{})
				replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/longer" {
						w.WriteHeader(204)
						return
					}
					if kind == "sse" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: before\n\n")
						w.(http.Flusher).Flush()
						select {
						case <-send:
							fmt.Fprint(w, "data: after\n\n")
							w.(http.Flusher).Flush()
						case <-r.Context().Done():
							return
						}
						select {
						case <-release:
						case <-r.Context().Done():
						}
						return
					}
					c, e := websocket.Accept(w, r, nil)
					if e != nil {
						return
					}
					defer c.CloseNow()
					for {
						typ, msg, e := c.Read(r.Context())
						if e != nil {
							return
						}
						if e = c.Write(r.Context(), typ, msg); e != nil {
							return
						}
					}
				}))
				cookies := f.login()
				releaseRead := f.holdCounterFlush()
				defer func() {
					if releaseRead != nil {
						releaseRead()
					}
				}()
				invalidate := func() {
					releaseRead()
					releaseRead = nil
					if end == "revoke" {
						if _, e := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); e != nil {
							t.Fatal(e)
						}
					} else {
						f.now.Store(accessTestTime.Add(2 * time.Hour).UnixNano())
					}
				}
				var continued bool
				if kind == "sse" {
					r, _ := http.NewRequest("GET", f.base+"/events", nil)
					for _, c := range cookies {
						r.AddCookie(c)
					}
					resp, e := f.client.Do(r)
					if e != nil {
						t.Fatal(e)
					}
					defer resp.Body.Close()
					rd := bufio.NewReader(resp.Body)
					first, e := rd.ReadString('\n')
					if e != nil || first != "data: before\n" {
						t.Fatal("stream control", e, first)
					}
					_, _ = rd.ReadString('\n')
					invalidate()
					once.Do(func() { close(send) })
					line, e := rd.ReadString('\n')
					continued = e == nil && line == "data: after\n"
				} else {
					header := http.Header{}
					r, _ := http.NewRequest("GET", f.base, nil)
					for _, c := range cookies {
						r.AddCookie(c)
					}
					header.Set("Cookie", r.Header.Get("Cookie"))
					ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
					defer cancel()
					c, _, e := websocket.Dial(ctx, strings.Replace(f.base, "https:", "wss:", 1)+"/socket", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: header})
					if e != nil {
						t.Fatal(e)
					}
					defer c.CloseNow()
					if e = c.Write(ctx, websocket.MessageText, []byte("before")); e != nil {
						t.Fatal(e)
					}
					_, msg, e := c.Read(ctx)
					if e != nil || string(msg) != "before" {
						t.Fatal("socket control", e)
					}
					invalidate()
					if e = c.Write(ctx, websocket.MessageText, []byte("after")); e == nil {
						_, msg, e = c.Read(ctx)
						continued = e == nil && string(msg) == "after"
					}
				}
				// Stream authorization has finished latching expiry before returning
				// its final read. Join that work before the read-only denial check.
				cleanupGate(f).stateReads.Wait()
				defer f.holdCounterFlush()()
				response, _ := f.request("GET", "/next", "", cookies)
				next := response.StatusCode
				t.Logf("continued_after_%s=%t next_http_status=%d", end, continued, next)
				if next != 401 {
					t.Error("new request must be denied")
				}
				if continued {
					t.Error("stream still transfers application data after grant ends")
				}
				resp, _ := f.request("GET", "/guest/"+longerToken, "", nil)
				if resp.StatusCode != 303 {
					t.Fatal("longer grant lost access", resp.StatusCode)
				}
				resp, _ = f.request("GET", "/longer", "", resp.Cookies())
				if resp.StatusCode != 204 {
					t.Fatal("longer grant app access", resp.StatusCode)
				}
			})
		}
	}
}
func TestGuestTailnetSecrets(t *testing.T) {
	f := newGuestFixture(t, "", false, false)
	resp, _ := f.request("GET", "/control", "", nil)
	if resp.StatusCode != 204 {
		t.Fatal("tailnet control", resp.StatusCode)
	}
	<-f.received
	for _, path := range []string{"/guest/" + f.token + "?pin=975310", "/guest/" + strings.Repeat("x", 43), "/guest/pin"} {
		resp, _ = f.request("GET", path, "", nil)
		if resp.StatusCode != 303 || resp.Header.Get("Location") != "/" || len(resp.Cookies()) != 0 {
			t.Errorf("tailnet guest route: status=%d cookies=%v", resp.StatusCode, resp.Cookies())
		}
	}
	if f.hits.Load() != 1 {
		t.Errorf("guest routes reached backend: hits=%d", f.hits.Load())
	}
	resp, _ = f.request("GET", "/app?token="+f.token+"&pin=975310&guest_token="+f.token+"&guest_pin=975310&keep=yes", "", nil)
	if resp.StatusCode != 204 {
		t.Fatal("app control", resp.StatusCode)
	}
	var request *http.Request
	for len(f.received) > 0 {
		request = <-f.received
	}
	if request == nil || strings.Contains(request.URL.String(), f.token) || strings.Contains(request.Header.Get("Referer"), f.token) || request.URL.RawQuery != "keep=yes" {
		t.Errorf("backend credentials: %v", request)
	}
	view, e := registry.ShowGuest(f.path, f.grant.ID, accessTestTime)
	if e != nil || view.Uses != 0 || view.Sessions != 0 || view.PINFailures != 0 {
		t.Fatalf("tailnet guest state changed: %+v %v", view, e)
	}
}
func TestGuestReservedCookieWhitespace(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	allowed := []string{"app_session =preserved; Path=/; Secure", "__host-TSLinkGuest=distinct; Path=/; Secure", "__Host-TSLinkguestPIN=distinct; Path=/; Secure"}
	replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{guestCookie, guestPINCookie} {
			for _, space := range []string{"", " ", "\t", " \t "} {
				w.Header().Add("Set-Cookie", " \t"+name+space+"=replacement; Path=/; Secure; HttpOnly")
			}
		}
		for _, value := range allowed {
			w.Header().Add("Set-Cookie", value)
		}
		w.WriteHeader(204)
	}))
	cookies := f.login()
	resp, _ := f.healthyRequest("/", cookies)
	blocked := true
	app := false
	for _, c := range resp.Cookies() {
		if c.Name == guestCookie || c.Name == guestPINCookie {
			blocked = false
		}
		if c.Name == "app_session" {
			app = true
		}
	}
	t.Logf("reserved_cookie_blocked=%t app_cookie_preserved=%t", blocked, app)
	if !app {
		t.Fatal("app cookie positive control missing")
	}
	if !blocked {
		t.Error("reserved Set-Cookie is accepted after name whitespace normalization")
	}
	if got := resp.Header.Values("Set-Cookie"); !slices.Equal(got, allowed) {
		t.Errorf("allowed cookies changed: %q", got)
	}
}
func TestGuestWriterLatency(t *testing.T) {
	f := newGuestFixture(t, "", true, true)
	other, _, e := registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "photos", Value: "1h", PublicAck: true, Now: accessTestTime})
	if e != nil {
		t.Fatal(e)
	}
	cookies := f.login()
	if resp, _ := f.healthyRequest("/control", cookies); resp.StatusCode != 204 {
		t.Fatal("control", resp.StatusCode)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	counts := map[int]int{}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, e := guestRequest(f.client, f.base, "/asset", cookies)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				counts[0]++
			} else {
				counts[status]++
			}
		}()
	}
	writer := make(chan time.Duration, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		at := time.Now()
		_, e := registry.RevokeGuest(f.path, other.ID, accessTestTime)
		if e != nil {
			writer <- -1
		} else {
			writer <- time.Since(at)
		}
	}()
	close(start)
	wg.Wait()
	elapsed := <-writer
	if elapsed < 0 {
		t.Fatal("writer failed")
	}
	// Join the completed writer, then own only the recovery read. The actual
	// concurrent writer above and reference writer below remain unowned.
	response, _ := f.healthyRequest("/after", cookies)
	after := response.StatusCode
	raw, _ := json.Marshal(map[string]any{"other_grant_revoke_ms": float64(elapsed.Microseconds()) / 1000, "asset_statuses": counts, "same_session_after": after})
	guestArtifact(t, "writer-latency.json", raw)
	t.Log(string(raw))
	if after != 204 || counts[204]+counts[503] != 50 {
		t.Errorf("unrelated revoke interrupted session: %v after=%d", counts, after)
	}
	// Measure an uncontended registry writer on the same file as a local reference.
	at := time.Now()
	if _, e = registry.RevokeGuest(f.path, other.ID, accessTestTime); e != nil {
		t.Fatal(e)
	}
	t.Logf("uncontended_writer_ms=%.3f", float64(time.Since(at).Microseconds())/1000)
}
