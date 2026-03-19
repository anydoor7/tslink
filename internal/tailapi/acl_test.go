package tailapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale"
)

// aclSetup initialises a clean test environment and saves/restores aclClientFn.
func aclSetup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	orig := aclClientFn
	t.Cleanup(func() { aclClientFn = orig })
}

// setACLClientFn overrides aclClientFn for one test.
func setACLClientFn(t *testing.T, fn func() (*tailscale.Client, error)) {
	t.Helper()
	aclClientFn = fn
}

// aclRoundTripper creates a roundTripperFunc and installs it as DefaultTransport.
func aclWithTransport(t *testing.T, fn func(*http.Request) (*http.Response, error)) {
	t.Helper()
	old := http.DefaultTransport
	http.DefaultTransport = roundTripperFunc(fn)
	t.Cleanup(func() { http.DefaultTransport = old })
}

func aclJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Etag": []string{`"test-etag"`}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// ---------- ReadTags ----------

func TestReadTags_NoClient(t *testing.T) {
	aclSetup(t)
	// No API key → credentials.NewTailscaleClient returns (nil, nil)
	tags, err := ReadTags(context.Background())
	if err != nil {
		t.Fatalf("ReadTags() error = %v", err)
	}
	if tags != nil {
		t.Fatalf("ReadTags() = %v, want nil", tags)
	}
}

func TestReadTags_ClientError(t *testing.T) {
	aclSetup(t)
	// Make the API key path a directory so GetAPIKey fails.
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err = ReadTags(context.Background())
	if err == nil {
		t.Fatal("ReadTags() error = nil, want error")
	}
}

func TestReadTags_ACLReadError(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network error")
	})

	_, err := ReadTags(context.Background())
	if err == nil {
		t.Fatal("ReadTags() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read ACL") {
		t.Fatalf("ReadTags() error = %v, want 'read ACL' error", err)
	}
}

func TestReadTags_SuccessWithTags(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl") {
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"],"tag:db":["user@example.com"]}}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	tags, err := ReadTags(context.Background())
	if err != nil {
		t.Fatalf("ReadTags() error = %v", err)
	}
	sort.Strings(tags)
	if got := strings.Join(tags, ","); got != "tag:db,tag:web" {
		t.Fatalf("ReadTags() = %v, want [tag:db, tag:web]", tags)
	}
}

func TestReadTags_SuccessEmpty(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl") {
			return aclJSONResponse(http.StatusOK, `{}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	tags, err := ReadTags(context.Background())
	if err != nil {
		t.Fatalf("ReadTags() error = %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("ReadTags() = %v, want empty", tags)
	}
}

// ---------- EnsureTags ----------

func TestEnsureTags_NoClient(t *testing.T) {
	aclSetup(t)
	if err := EnsureTags(context.Background(), []string{"tag:test"}); err != nil {
		t.Fatalf("EnsureTags() error = %v", err)
	}
}

func TestEnsureTags_ClientError(t *testing.T) {
	aclSetup(t)
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := EnsureTags(context.Background(), []string{"tag:test"}); err == nil {
		t.Fatal("EnsureTags() error = nil, want error")
	}
}

func TestEnsureTags_ACLReadError(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network error")
	})

	err := EnsureTags(context.Background(), []string{"tag:test"})
	if err == nil {
		t.Fatal("EnsureTags() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read ACL") {
		t.Fatalf("EnsureTags() error = %v, want 'read ACL' error", err)
	}
}

func TestEnsureTags_AllExist(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl") {
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"],"tag:db":["autogroup:admin"]}}`), nil
		}
		// SetACL should NOT be called
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	if err := EnsureTags(context.Background(), []string{"tag:web", "tag:db"}); err != nil {
		t.Fatalf("EnsureTags() error = %v", err)
	}
}

func TestEnsureTags_CreatesMissing(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var postedACL map[string]interface{}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:existing":["user@example.com"]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			body, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(body, &postedACL)
			// Return the same body as response
			return aclJSONResponse(http.StatusOK, string(body)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	if err := EnsureTags(context.Background(), []string{"tag:existing", "tag:new"}); err != nil {
		t.Fatalf("EnsureTags() error = %v", err)
	}

	// Verify the posted ACL includes both tags
	tagOwners, ok := postedACL["tagowners"].(map[string]interface{})
	if !ok {
		t.Fatalf("posted ACL missing tagowners: %v", postedACL)
	}
	if _, exists := tagOwners["tag:existing"]; !exists {
		t.Fatal("posted ACL missing tag:existing")
	}
	if _, exists := tagOwners["tag:new"]; !exists {
		t.Fatal("posted ACL missing tag:new")
	}
}

func TestEnsureTags_NilTagOwners(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var postedACL map[string]interface{}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			// Return ACL with no tagowners field at all
			return aclJSONResponse(http.StatusOK, `{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			body, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(body, &postedACL)
			return aclJSONResponse(http.StatusOK, string(body)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	if err := EnsureTags(context.Background(), []string{"tag:new"}); err != nil {
		t.Fatalf("EnsureTags() error = %v", err)
	}

	tagOwners, ok := postedACL["tagowners"].(map[string]interface{})
	if !ok {
		t.Fatalf("posted ACL missing tagowners: %v", postedACL)
	}
	if _, exists := tagOwners["tag:new"]; !exists {
		t.Fatal("posted ACL missing tag:new")
	}
}

func TestEnsureTags_SetACLError(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			return nil, errors.New("server error")
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := EnsureTags(context.Background(), []string{"tag:new"})
	if err == nil {
		t.Fatal("EnsureTags() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "update ACL") {
		t.Fatalf("EnsureTags() error = %v, want 'update ACL' error", err)
	}
}

// ---------- DeleteTag ----------

func TestDeleteTag_NoClient(t *testing.T) {
	aclSetup(t)
	err := DeleteTag(context.Background(), "tag:test")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no API client") {
		t.Fatalf("DeleteTag() error = %v, want 'no API client' error", err)
	}
}

func TestDeleteTag_ClientError(t *testing.T) {
	aclSetup(t)
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatalf("APIKeyPath() error = %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := DeleteTag(context.Background(), "tag:test"); err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
}

func TestDeleteTag_ACLReadError(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("network error")
	})

	err := DeleteTag(context.Background(), "tag:test")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read ACL") {
		t.Fatalf("DeleteTag() error = %v, want 'read ACL' error", err)
	}
}

func TestDeleteTag_NotFound(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl") {
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:other":["autogroup:admin"]}}`), nil
		}
		t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		return nil, nil
	})

	err := DeleteTag(context.Background(), "tag:missing")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("DeleteTag() error = %v, want 'not found' error", err)
	}
}

func TestDeleteTag_Success(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var postedACL map[string]interface{}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"],"tag:db":["autogroup:admin"]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			body, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(body, &postedACL)
			return aclJSONResponse(http.StatusOK, string(body)), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	if err := DeleteTag(context.Background(), "tag:web"); err != nil {
		t.Fatalf("DeleteTag() error = %v", err)
	}

	tagOwners, ok := postedACL["tagowners"].(map[string]interface{})
	if !ok {
		t.Fatalf("posted ACL missing tagowners: %v", postedACL)
	}
	if _, exists := tagOwners["tag:web"]; exists {
		t.Fatal("posted ACL should not contain tag:web after deletion")
	}
	if _, exists := tagOwners["tag:db"]; !exists {
		t.Fatal("posted ACL should still contain tag:db")
	}
}

func TestDeleteTag_SetACLError(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			return nil, errors.New("server error")
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := DeleteTag(context.Background(), "tag:web")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "update ACL") {
		t.Fatalf("DeleteTag() error = %v, want 'update ACL' error", err)
	}
}
