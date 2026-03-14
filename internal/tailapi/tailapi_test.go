package tailapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/zalando/go-keyring"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func setup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
}

func withDefaultTransport(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = rt
	t.Cleanup(func() {
		http.DefaultTransport = old
	})
}

func mustSetAPIKey(t *testing.T, key string) {
	t.Helper()
	if err := credentials.SetAPIKey(key); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDeleteDevicesByHostname_NoClient(t *testing.T) {
	setup(t)

	if err := DeleteDevicesByHostname(context.Background(), "test-host"); err != nil {
		t.Fatalf("DeleteDevicesByHostname() error = %v", err)
	}
}

func TestDeleteDevicesByHostname_ClientError(t *testing.T) {
	setup(t)

	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := DeleteDevicesByHostname(context.Background(), "test-host"); err == nil {
		t.Fatal("DeleteDevicesByHostname() error = nil, want error")
	}
}

func TestDeleteDevicesByHostname_DeletesMatchingDevices(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if user, _, ok := req.BasicAuth(); !ok || user != "api-key" {
			t.Fatalf("expected basic auth with API key, got ok=%v user=%q", ok, user)
		}

		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"test-host"},{"id":"dev2","hostname":"test-host-1"},{"id":"dev3","hostname":"other"}]}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v2/device/dev1":
			deleted = append(deleted, "dev1")
			return jsonResponse(http.StatusOK, `{}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v2/device/dev2":
			deleted = append(deleted, "dev2")
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	if err := DeleteDevicesByHostname(context.Background(), "test-host"); err != nil {
		t.Fatalf("DeleteDevicesByHostname() error = %v", err)
	}

	if got := strings.Join(deleted, ","); got != "dev1,dev2" {
		t.Fatalf("deleted devices = %q, want %q", got, "dev1,dev2")
	}
}

func TestDeleteDevicesByHostname_ListError(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}))

	err := DeleteDevicesByHostname(context.Background(), "test-host")
	if err == nil {
		t.Fatal("DeleteDevicesByHostname() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "list devices") {
		t.Fatalf("DeleteDevicesByHostname() error = %v, want list devices error", err)
	}
}

func TestDeleteDevicesByHostname_DeleteError(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"test-host"}]}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v2/device/dev1":
			return jsonResponse(http.StatusInternalServerError, `{"message":"boom"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	err := DeleteDevicesByHostname(context.Background(), "test-host")
	if err == nil {
		t.Fatal("DeleteDevicesByHostname() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "delete device test-host") {
		t.Fatalf("DeleteDevicesByHostname() error = %v, want delete error", err)
	}
}

func TestCleanupStaleNodes_NoClient(t *testing.T) {
	setup(t)

	if err := CleanupStaleNodes(context.Background(), []string{"host1", "host2"}); err != nil {
		t.Fatalf("CleanupStaleNodes() error = %v", err)
	}
}

func TestCleanupStaleNodes_ClientError(t *testing.T) {
	setup(t)

	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := CleanupStaleNodes(context.Background(), []string{"host1"}); err == nil {
		t.Fatal("CleanupStaleNodes() error = nil, want error")
	}
}

func TestCleanupStaleNodes_ListErrorIsNonFatal(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}))

	if err := CleanupStaleNodes(context.Background(), []string{"host1"}); err != nil {
		t.Fatalf("CleanupStaleNodes() error = %v", err)
	}
}

func TestCleanupStaleNodes_DeletesExactAndSuffixedMatches(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"host1"},{"id":"dev2","hostname":"host1-1"},{"id":"dev3","hostname":"host2"},{"id":"dev4","hostname":"host2-2"},{"id":"dev5","hostname":"other-1"}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	if err := CleanupStaleNodes(context.Background(), []string{"host1", "host2"}); err != nil {
		t.Fatalf("CleanupStaleNodes() error = %v", err)
	}

	if got := strings.Join(deleted, ","); got != "dev1,dev2,dev3,dev4" {
		t.Fatalf("deleted devices = %q, want %q", got, "dev1,dev2,dev3,dev4")
	}
}
