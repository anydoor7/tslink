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
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/zalando/go-keyring"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func setup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	testenv.SetHome(t, t.TempDir())
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

func cleanupTarget(hostname string) CleanupTarget {
	return CleanupTarget{Hostname: hostname, Tags: []string{"tag:tsmain"}}
}

func TestCleanupTargetConstructors(t *testing.T) {
	services := []registry.Service{
		{Name: "web", Tags: []string{"tag:web"}},
		{Name: "docs", Tags: []string{"tag:docs", "tag:shared"}},
	}

	target := CleanupTargetForService(services[0])
	if target.Hostname != "web" || strings.Join(target.Tags, ",") != "tag:web" {
		t.Fatalf("CleanupTargetForService() = %+v, want service hostname/tags only", target)
	}

	targets := CleanupTargetsForServices(services)
	if len(targets) != 2 {
		t.Fatalf("CleanupTargetsForServices() returned %d targets, want 2", len(targets))
	}
	if targets[0].Hostname != "web" || strings.Join(targets[0].Tags, ",") != "tag:web" {
		t.Fatalf("first cleanup target = %+v, want web target", targets[0])
	}
	if targets[1].Hostname != "docs" || strings.Join(targets[1].Tags, ",") != "tag:docs,tag:shared" {
		t.Fatalf("second cleanup target = %+v, want docs target", targets[1])
	}
	if empty := CleanupTargetsForServices(nil); len(empty) != 0 {
		t.Fatalf("CleanupTargetsForServices(nil) = %d targets, want 0", len(empty))
	}
}

func TestDeleteDevicesForService_NoClient(t *testing.T) {
	setup(t)

	result, err := DeleteDevicesForService(context.Background(), cleanupTarget("test-host"))
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v, want nil skip", err)
	}
	if !result.Skipped || result.SkipReason != ErrNoAPIClient.Error() {
		t.Fatalf("DeleteDevicesForService() result = %+v, want no-client skip", result)
	}
}

func TestHostnameMatchesCleanupTarget_PositiveNumericSuffixesOnly(t *testing.T) {
	cases := []struct {
		hostname string
		target   string
		want     bool
	}{
		{"app", "app", true},
		{"app-1", "app", true},
		{"app-12", "app", true},
		{"app-0", "app", false},
		{"app--1", "app", false},
		{"app-01", "app", false},
		{"app-01a", "app", false},
		{"app-staging", "app", false},
		{"application-1", "app", false},
	}
	for _, tc := range cases {
		if got := hostnameMatchesCleanupTarget(tc.hostname, tc.target); got != tc.want {
			t.Errorf("hostnameMatchesCleanupTarget(%q, %q) = %v, want %v", tc.hostname, tc.target, got, tc.want)
		}
	}
}

func TestDeleteDevicesForService_ClientError(t *testing.T) {
	setup(t)

	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if _, err := DeleteDevicesForService(context.Background(), cleanupTarget("test-host")); err == nil {
		t.Fatal("DeleteDevicesForService() error = nil, want error")
	}
}

func TestDeleteDevicesForService_ProtectsMatchingTaggedDevicesWithoutExactProof(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if user, _, ok := req.BasicAuth(); !ok || user != "api-key" {
			t.Fatalf("expected basic auth with API key, got ok=%v user=%q", ok, user)
		}

		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"test-host","tags":["tag:tsmain"]},{"id":"dev2","hostname":"test-host-1","tags":["tag:tsmain"]},{"id":"dev3","hostname":"other","tags":["tag:tsmain"]}]}`), nil
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

	result, err := DeleteDevicesForService(context.Background(), cleanupTarget("test-host"))
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none without exact ownership proof", deleted)
	}
	if got := strings.Join(result.Matched, ","); got != "test-host,test-host-1" {
		t.Fatalf("result.Matched = %q, want test-host,test-host-1", got)
	}
	if got := strings.Join(result.Protected, ","); got != "test-host,test-host-1" {
		t.Fatalf("result.Protected = %q, want test-host,test-host-1", got)
	}
	if !result.Skipped || !strings.Contains(result.SkipReason, "exact TSLink ownership proof") {
		t.Fatalf("result = %+v, want protected skip", result)
	}
}

func TestDeleteDevicesForService_ProtectsReusedNameAndSuffixesWithoutDelete(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"old-dev","hostname":"app","tags":["tag:tsmain"]},{"id":"reused-dev","hostname":"app","tags":["tag:other"]},{"id":"suffix-dev","hostname":"app-1","tags":["tag:tsmain"]},{"id":"adjacent-dev","hostname":"app-staging","tags":["tag:tsmain"]}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	result, err := DeleteDevicesForService(context.Background(), cleanupTarget("app"))
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none after retiring automatic deletion", deleted)
	}
	if got := strings.Join(result.Protected, ","); got != "app,app,app-1" {
		t.Fatalf("result.Protected = %q, want app,app,app-1", got)
	}
	if !result.Skipped || !strings.Contains(result.SkipReason, "exact TSLink ownership proof") {
		t.Fatalf("result = %+v, want protected/manual cleanup", result)
	}
}

func TestDeleteDevicesForService_ProtectsAppAndNumericSuffixWithSharedDefaultTag(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"app","tags":["tag:tsmain"]},{"id":"dev2","hostname":"app-1","tags":["tag:tsmain"]},{"id":"dev3","hostname":"app-staging","tags":["tag:tsmain"]},{"id":"dev4","hostname":"app-0","tags":["tag:tsmain"]},{"id":"dev5","hostname":"app--1","tags":["tag:tsmain"]},{"id":"dev6","hostname":"app-01a","tags":["tag:tsmain"]}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	result, err := DeleteDevicesForService(context.Background(), cleanupTarget("app"))
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none without exact ownership proof", deleted)
	}
	if got := strings.Join(result.Matched, ","); got != "app,app-1" {
		t.Fatalf("result.Matched = %q, want app,app-1", got)
	}
	if got := strings.Join(result.Protected, ","); got != "app,app-1" {
		t.Fatalf("result.Protected = %q, want app,app-1", got)
	}
}

func TestDeleteDevicesForService_ProtectsSameHostnameWithoutExpectedTag(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"app","tags":["tag:other"]},{"id":"dev2","hostname":"app-1"}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	result, err := DeleteDevicesForService(context.Background(), cleanupTarget("app"))
	if err != nil {
		t.Fatalf("DeleteDevicesForService() error = %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none", deleted)
	}
	if got := strings.Join(result.Protected, ","); got != "app,app-1" {
		t.Fatalf("result.Protected = %q, want app,app-1", got)
	}
	if !result.Skipped || !strings.Contains(result.SkipReason, "exact TSLink ownership proof") {
		t.Fatalf("result = %+v, want protected skip", result)
	}
}

func TestDeleteDevicesForService_ListError(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}))

	_, err := DeleteDevicesForService(context.Background(), CleanupTarget{Hostname: "test-host", Tags: []string{"tag:tsmain"}})
	if err == nil {
		t.Fatal("DeleteDevicesForService() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "list devices") {
		t.Fatalf("DeleteDevicesForService() error = %v, want list devices error", err)
	}
}

func TestDeleteDevicesForService_IdempotentWhenNoMatchingDevice(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleteCalled bool
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"other","tags":["tag:tsmain"]}]}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v2/device/dev1":
			deleteCalled = true
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	for i := 0; i < 2; i++ {
		result, err := DeleteDevicesForService(context.Background(), cleanupTarget("test-host"))
		if err != nil {
			t.Fatalf("DeleteDevicesForService() run %d error = %v", i, err)
		}
		if result.Skipped || len(result.Matched) != 0 || len(result.Deleted) != 0 || len(result.Protected) != 0 {
			t.Fatalf("run %d result = %+v, want idempotent no-op", i, result)
		}
	}
	if deleteCalled {
		t.Fatal("DELETE was called for idempotent no-match cleanup")
	}
}

func TestCleanupStaleNodes_NoClient(t *testing.T) {
	setup(t)

	result, err := CleanupStaleNodesResult(context.Background(), []CleanupTarget{cleanupTarget("host1"), cleanupTarget("host2")})
	if err != nil {
		t.Fatalf("CleanupStaleNodesResult() error = %v, want nil skip", err)
	}
	if !result.Skipped || result.SkipReason == "" {
		t.Fatalf("CleanupStaleNodesResult() result = %+v, want skipped with reason", result)
	}
	if err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1"), cleanupTarget("host2")}); err != nil {
		t.Fatalf("CleanupStaleNodes() error = %v, want nil skip", err)
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

	if err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1")}); err == nil {
		t.Fatal("CleanupStaleNodes() error = nil, want error")
	}
}

func TestCleanupStaleNodes_ListErrorIsFatal(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	}))

	if err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1")}); err == nil {
		t.Fatal("CleanupStaleNodes() error = nil, want list devices error")
	}
}

func TestCleanupStaleNodes_ProtectsExactAndSuffixedMatchesWithoutExactProof(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"host1","tags":["tag:tsmain"]},{"id":"dev2","hostname":"host1-1","tags":["tag:tsmain"]},{"id":"dev3","hostname":"host2","tags":["tag:tsmain"]},{"id":"dev4","hostname":"host2-2","tags":["tag:tsmain"]},{"id":"dev5","hostname":"other-1","tags":["tag:tsmain"]}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	if err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1"), cleanupTarget("host2")}); err != nil {
		t.Fatalf("CleanupStaleNodes() error = %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none without exact ownership proof", deleted)
	}
}

func TestCleanupStaleNodes_DoesNotDeleteAdjacentHyphenatedHostnames(t *testing.T) {
	setup(t)
	mustSetAPIKey(t, "api-key")

	var deleted []string
	withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
			return jsonResponse(http.StatusOK, `{"devices":[{"id":"dev1","hostname":"app","tags":["tag:tsmain"]},{"id":"dev2","hostname":"app-1","tags":["tag:tsmain"]},{"id":"dev3","hostname":"app-staging","tags":["tag:tsmain"]},{"id":"dev4","hostname":"app-0","tags":["tag:tsmain"]},{"id":"dev5","hostname":"app--1","tags":["tag:tsmain"]},{"id":"dev6","hostname":"app-01a","tags":["tag:tsmain"]}]}`), nil
		case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/api/v2/device/"):
			deleted = append(deleted, strings.TrimPrefix(req.URL.Path, "/api/v2/device/"))
			return jsonResponse(http.StatusOK, `{}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
			return nil, nil
		}
	}))

	result, err := CleanupStaleNodesResult(context.Background(), []CleanupTarget{cleanupTarget("app")})
	if err != nil {
		t.Fatalf("CleanupStaleNodesResult() error = %v", err)
	}

	if len(deleted) != 0 {
		t.Fatalf("deleted devices = %v, want none without exact ownership proof", deleted)
	}
	if got := strings.Join(result.Matched, ","); got != "app,app-1" {
		t.Fatalf("result.Matched = %q, want app,app-1", got)
	}
	if got := strings.Join(result.Protected, ","); got != "app,app-1" {
		t.Fatalf("result.Protected = %q, want app,app-1", got)
	}
}
