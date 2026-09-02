package testenv

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tailscale/hujson"
	tailscale "tailscale.com/client/tailscale/v2"
)

const (
	TailnetDevicesPath  = "/api/v2/tailnet/-/devices"
	TailnetPolicyPath   = "/api/v2/tailnet/-/acl"
	TailnetSettingsPath = "/api/v2/tailnet/-/settings"
)

// TailnetDevicePath returns the exact REST path used by the v2 SDK for one
// device. Tests should pass a NodeID, matching TSLink's deletion contract.
func TailnetDevicePath(nodeID string) string {
	return "/api/v2/device/" + url.PathEscape(nodeID)
}

// TailnetRequest records only non-secret request metadata. Authorization and
// request bodies are deliberately excluded.
type TailnetRequest struct {
	Method      string
	Path        string
	Accept      string
	ContentType string
	IfMatch     string
	BasicAuth   bool
}

type tailnetFailure struct {
	method string
	path   string
	status int
	delay  time.Duration
}

// StatefulTailnet is an in-memory httptest implementation of the Tailscale v2
// REST resources TSLink currently consumes: devices, policy file, and tailnet
// settings. Its wire payloads use the v2.9.0 SDK types and JSON field names.
type StatefulTailnet struct {
	t testing.TB

	server *httptest.Server
	mu     sync.Mutex

	devices  []tailscale.Device
	policy   []byte
	revision int
	settings tailscale.TailnetSettings
	failures []tailnetFailure
	requests []TailnetRequest
}

// NewStatefulTailnet starts an isolated loopback server with an empty policy.
func NewStatefulTailnet(t testing.TB) *StatefulTailnet {
	t.Helper()
	fake := &StatefulTailnet{
		t:        t,
		policy:   []byte(`{"tagOwners":{}}`),
		revision: 1,
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(fake.server.Close)
	return fake
}

// URL returns the loopback-only base URL accepted by tailapi.APIBaseURLEnv.
func (f *StatefulTailnet) URL() string {
	return f.server.URL
}

// Client returns the real v2 SDK client configured for this fake.
func (f *StatefulTailnet) Client(apiKey string) *tailscale.Client {
	f.t.Helper()
	baseURL, err := url.Parse(f.server.URL)
	if err != nil {
		f.t.Fatalf("parse stateful tailnet URL: %v", err)
	}
	return &tailscale.Client{Tailnet: "-", APIKey: apiKey, BaseURL: baseURL}
}

// SetDevices replaces the fake tailnet's device state.
func (f *StatefulTailnet) SetDevices(devices []tailscale.Device) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices = cloneDevices(devices)
}

// Devices returns an isolated snapshot of current device state.
func (f *StatefulTailnet) Devices() []tailscale.Device {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneDevices(f.devices)
}

func cloneDevices(devices []tailscale.Device) []tailscale.Device {
	cloned := append([]tailscale.Device(nil), devices...)
	for i := range cloned {
		cloned[i].Addresses = append([]string(nil), devices[i].Addresses...)
		cloned[i].Tags = append([]string(nil), devices[i].Tags...)
		cloned[i].AdvertisedRoutes = append([]string(nil), devices[i].AdvertisedRoutes...)
		cloned[i].EnabledRoutes = append([]string(nil), devices[i].EnabledRoutes...)
	}
	return cloned
}

// SetPolicy replaces the raw HuJSON policy and advances its ETag.
func (f *StatefulTailnet) SetPolicy(policy string) {
	f.t.Helper()
	if _, err := hujson.Standardize([]byte(policy)); err != nil {
		f.t.Fatalf("SetPolicy received invalid HuJSON: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policy = []byte(policy)
	f.revision++
}

// Policy returns the raw policy and its current HTTP ETag value.
func (f *StatefulTailnet) Policy() (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.policy), f.etagLocked()
}

// SetTailnetSettings replaces the read-only settings response.
func (f *StatefulTailnet) SetTailnetSettings(settings tailscale.TailnetSettings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = settings
}

// FailNext makes the next exact method/path request return an API error.
func (f *StatefulTailnet) FailNext(method, path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, tailnetFailure{method: method, path: path, status: status})
}

// DelayNext delays the next exact method/path request. A caller with a shorter
// context deadline observes a real HTTP timeout/cancellation.
func (f *StatefulTailnet) DelayNext(method, path string, delay time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, tailnetFailure{method: method, path: path, delay: delay})
}

// Requests returns recorded non-secret request metadata.
func (f *StatefulTailnet) Requests() []TailnetRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TailnetRequest(nil), f.requests...)
}

func (f *StatefulTailnet) popFailure(method, path string) (tailnetFailure, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, failure := range f.failures {
		if failure.method == method && failure.path == path {
			f.failures = append(f.failures[:i], f.failures[i+1:]...)
			return failure, true
		}
	}
	return tailnetFailure{}, false
}

func (f *StatefulTailnet) serveHTTP(w http.ResponseWriter, r *http.Request) {
	_, _, basicAuth := r.BasicAuth()
	f.mu.Lock()
	f.requests = append(f.requests, TailnetRequest{
		Method:      r.Method,
		Path:        r.URL.EscapedPath(),
		Accept:      r.Header.Get("Accept"),
		ContentType: r.Header.Get("Content-Type"),
		IfMatch:     r.Header.Get("If-Match"),
		BasicAuth:   basicAuth,
	})
	f.mu.Unlock()

	if failure, ok := f.popFailure(r.Method, r.URL.EscapedPath()); ok {
		if failure.delay > 0 {
			timer := time.NewTimer(failure.delay)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			}
		}
		if failure.status != 0 {
			writeTailnetAPIError(w, failure.status, http.StatusText(failure.status))
			return
		}
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == TailnetDevicesPath:
		f.serveDevices(w)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v2/device/"):
		f.serveDeleteDevice(w, strings.TrimPrefix(r.URL.Path, "/api/v2/device/"))
	case r.Method == http.MethodGet && r.URL.Path == TailnetPolicyPath:
		f.servePolicyGet(w, r)
	case r.Method == http.MethodPost && r.URL.Path == TailnetPolicyPath:
		f.servePolicySet(w, r)
	case r.Method == http.MethodGet && r.URL.Path == TailnetSettingsPath:
		f.serveSettings(w)
	default:
		writeTailnetAPIError(w, http.StatusNotFound, "unsupported fake tailnet endpoint")
	}
}

func (f *StatefulTailnet) serveDevices(w http.ResponseWriter) {
	f.mu.Lock()
	devices := cloneDevices(f.devices)
	f.mu.Unlock()
	writeTailnetJSON(w, http.StatusOK, map[string][]tailscale.Device{"devices": devices})
}

func (f *StatefulTailnet) serveDeleteDevice(w http.ResponseWriter, nodeID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, device := range f.devices {
		if device.NodeID != nodeID && device.ID != nodeID {
			continue
		}
		f.devices = append(f.devices[:i], f.devices[i+1:]...)
		writeTailnetJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeTailnetAPIError(w, http.StatusNotFound, "device not found")
}

func (f *StatefulTailnet) servePolicyGet(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	policy := append([]byte(nil), f.policy...)
	etag := f.etagLocked()
	f.mu.Unlock()
	w.Header().Set("ETag", etag)
	if strings.Contains(r.Header.Get("Accept"), "application/hujson") {
		w.Header().Set("Content-Type", "application/hujson")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(policy)
		return
	}
	standard, err := hujson.Standardize(policy)
	if err != nil {
		writeTailnetAPIError(w, http.StatusInternalServerError, "stored policy is invalid")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(standard)
}

func (f *StatefulTailnet) servePolicySet(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeTailnetAPIError(w, http.StatusBadRequest, "read policy body")
		return
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/hujson") {
		if _, err := hujson.Standardize(body); err != nil {
			writeTailnetAPIError(w, http.StatusBadRequest, "invalid HuJSON policy")
			return
		}
	} else if !json.Valid(body) {
		writeTailnetAPIError(w, http.StatusBadRequest, "invalid JSON policy")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if match := strings.Trim(r.Header.Get("If-Match"), `"`); match != "" && match != strings.Trim(f.etagLocked(), `"`) {
		writeTailnetAPIError(w, http.StatusPreconditionFailed, "policy ETag conflict")
		return
	}
	f.policy = append([]byte(nil), body...)
	f.revision++
	w.Header().Set("ETag", f.etagLocked())
	writeTailnetJSON(w, http.StatusOK, map[string]any{})
}

func (f *StatefulTailnet) serveSettings(w http.ResponseWriter) {
	f.mu.Lock()
	settings := f.settings
	f.mu.Unlock()
	writeTailnetJSON(w, http.StatusOK, settings)
}

func (f *StatefulTailnet) etagLocked() string {
	return fmt.Sprintf(`"fake-policy-%d"`, f.revision)
}

func writeTailnetJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeTailnetAPIError(w http.ResponseWriter, status int, message string) {
	writeTailnetJSON(w, status, tailscale.APIError{Status: status, Message: message})
}
