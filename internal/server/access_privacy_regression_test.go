package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/registry"
)

// Port of the independent review's listener-level capability replay probe.
func TestAccessBearerCapabilityReplay(t *testing.T) {
	const capability = "Ab7qP9k2Lm4vR8x6"
	const appCapability = "shortbearer"
	for _, policy := range []struct{ name, global, service, want string }{
		{"default", "", "", "prefix"}, {"prefix", "prefix", "", "prefix"}, {"full", "full", "", "full"}, {"off", "off", "", "off"},
		{"service-full", "", "full", "full"}, {"service-prefix", "full", "prefix", "prefix"}, {"service-off", "", "off", "off"}, {"global-off", "off", "full", "off"},
	} {
		t.Run(policy.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch strings.ReplaceAll(r.URL.Path, "\\", "/") {
				case "/album/item":
					io.WriteString(w, "normal")
				case "/guest/" + capability, "/invite/" + capability, "/g/" + capability, "/s/" + appCapability, "/guest/" + appCapability, "/invite/" + appCapability, "/" + capability:
					w.WriteHeader(204)
				default:
					w.WriteHeader(403)
				}
			}))
			defer backend.Close()
			svc := registry.Service{Name: "bearerapp", Type: registry.TypeProxy, Target: backend.URL, Funnel: true, PublicAck: true, AccessLogPathMode: policy.service}
			s, store, dir, base := setupAccessNode(t, svc, nil, accesslog.Options{PathMode: policy.global}, nil, nil, true)
			paths := []string{"/album/item", "/guest%2F" + capability, "/invite%2f" + capability, "/g%2F" + capability, "/s%2F" + appCapability, "/guest%5C" + appCapability, "/invite%2f" + appCapability, "/" + capability, "/guest/incorrect-token"}
			for i, p := range paths {
				want := 204
				if i == 0 {
					want = 200
				}
				if i == len(paths)-1 {
					want = 403
				}
				if code := requestAccess(t, base+p, "GET", ""); code != want {
					t.Fatalf("backend control %q=%d want %d", p, code, want)
				}
			}
			// A completed response does not join either the handler's deferred
			// record or the asynchronous disk writer. Stop producers, then join
			// persistence; this test has no disk-throughput requirement.
			s.stopNodeLocked(svc.Name)
			drainAccess(t, store)
			result, err := accesslog.Query(dir, accesslog.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Events) != len(paths) {
				t.Fatalf("missing listener records: %+v", result)
			}
			statuses := map[int]int{}
			for _, e := range result.Events {
				statuses[e.Status]++
				if e.Kind != "http" || e.App != svc.Name || e.Method != "GET" || !e.Time.Equal(accessTestTime) {
					t.Fatalf("listener record metadata: %+v", e)
				}
			}
			if statuses[200] != 1 || statuses[204] != 7 || statuses[403] != 1 {
				t.Fatalf("record statuses: %v", statuses)
			}
			if h := store.Health(); h.Drops != 0 || h.Error != "" {
				t.Fatalf("listener writer health: %+v", h)
			}
			// Replay through another real listener with the same policy/backend.
			// Its own writer keeps replay requests out of the initial nine-record
			// snapshot without submitting records to an already closed store.
			replay, replayStore, replayDir, replayBase := setupAccessNode(t, svc, nil, accesslog.Options{PathMode: policy.global}, nil, nil, true)
			replays := 0
			mode := policy.want
			for _, e := range result.Events {
				if e.Identity.Login != "public" {
					t.Fatal(e.Identity)
				}
				if e.Status == 200 {
					want := "/album"
					if mode == "full" {
						want = "/album/item"
					}
					if mode == "off" {
						want = ""
					}
					if e.Path != want {
						t.Errorf("normal-path control got %q want %q", e.Path, want)
					}
				}
				// Any TSLink route is safe in every mode. App-defined /s is safe in prefix/off.
				if strings.HasPrefix(e.Path, "/s/") && mode == "full" {
					if !strings.Contains(e.Path, appCapability) {
						t.Error("full app path opt-in not honored")
					}
					continue
				}
				decoded, err := url.PathUnescape(e.Path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(decoded, capability) || strings.Contains(decoded, appCapability) {
					t.Errorf("replayable capability persisted: %s", e.Path)
				}
				if e.Status == 204 {
					// Replaying a sanitized TSLink/default-prefix path cannot authorize.
					u := url.URL{Path: decoded}
					replays++
					if code := requestAccess(t, replayBase+u.EscapedPath(), "GET", ""); code == 204 {
						t.Errorf("stored path replays capability: %q", e.Path)
					}
				}
			}
			replay.stopNodeLocked(svc.Name)
			drainAccess(t, replayStore)
			replayed, err := accesslog.Query(replayDir, accesslog.Filter{})
			if err != nil || len(replayed.Events) != replays {
				t.Fatalf("missing replay records: %+v, %v", replayed, err)
			}
			if h := replayStore.Health(); h.Drops != 0 || h.Error != "" {
				t.Fatalf("replay writer health: %+v", h)
			}
			t.Logf("listener records: initial=%d statuses=%v replay=%d; both writers drained without drops", len(result.Events), statuses, len(replayed.Events))
			for _, logDir := range []string{dir, replayDir} {
				files, err := filepath.Glob(filepath.Join(logDir, "access-log", "*.jsonl"))
				if err != nil || len(files) != 1 {
					t.Fatal(files, err)
				}
				raw, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				// Full mode intentionally permits application-specific paths; remove only
				// that positive control before checking the exact disk bytes.
				for _, line := range strings.Split(string(raw), "\n") {
					if mode == "full" && strings.Contains(line, `"path":"/s/`) {
						continue
					}
					if strings.Contains(line, capability) || strings.Contains(line, appCapability) {
						t.Errorf("capability on disk: %s", line)
					}
				}
			}
		})
	}
}
