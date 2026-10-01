package tailapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/zalando/go-keyring"
	tailscale "tailscale.com/client/tailscale/v2"
)

// aclSetup initialises a clean test environment and saves/restores aclClientFn.
func aclSetup(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Cleanup(credentials.SetMutationLockPathForTesting(filepath.Join(home, "credential-test.lock")))
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	orig := aclClientFn
	t.Cleanup(func() { aclClientFn = orig })
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

func aclWithClientServer(t *testing.T, handler http.Handler) {
	aclWithClientServerHTTPS(t, true, handler)
}

func aclWithClientServerHTTPS(t *testing.T, httpsEnabled bool, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/settings") {
			_ = json.NewEncoder(w).Encode(map[string]bool{"httpsEnabled": httpsEnabled})
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	client := &tailscale.Client{Tailnet: "-", APIKey: "fake-api-key", BaseURL: baseURL}
	aclClientFn = func() (*tailscale.Client, error) { return client, nil }
}

// ---------- ReadTags ----------

func TestReadTags_NoClient(t *testing.T) {
	aclSetup(t)
	// No API key → credentials.NewTailscaleClient returns (nil, nil)
	tags, err := ReadTags(context.Background())
	if !errors.Is(err, ErrNoAPIClient) {
		t.Fatalf("ReadTags() error = %v, want ErrNoAPIClient", err)
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
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"],"tag:db":["user@example.com"],"tag:tslink-funnel":["tag:web"]}}`), nil
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
	err := EnsureTags(context.Background(), []string{"tag:test"})
	if !errors.Is(err, ErrNoAPIClient) {
		t.Fatalf("EnsureTags() error = %v, want ErrNoAPIClient", err)
	}
}

func TestEnsureTags_NoClientEmptyTagsOK(t *testing.T) {
	aclSetup(t)
	if err := EnsureTags(context.Background(), nil); err != nil {
		t.Fatalf("EnsureTags() error = %v, want nil for empty tags", err)
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

func TestEnsureTags_ACLReadForbiddenIsClassified(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return aclJSONResponse(http.StatusForbidden, `{"message":"forbidden"}`), nil
	})

	err := EnsureTags(context.Background(), []string{"tag:test"})
	if !errors.Is(err, ErrPolicyAccessDenied) {
		t.Fatalf("EnsureTags() error = %v, want errors.Is ErrPolicyAccessDenied", err)
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

	var postedACL map[string]any
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
	tagOwners, ok := postedACL["tagowners"].(map[string]any)
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

	var postedACL map[string]any
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

	tagOwners, ok := postedACL["tagOwners"].(map[string]any)
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

func TestEnsureTags_ACLWriteForbiddenIsClassified(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusForbidden, `{"message":"forbidden"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := EnsureTags(context.Background(), []string{"tag:new"})
	if !errors.Is(err, ErrPolicyAccessDenied) {
		t.Fatalf("EnsureTags() error = %v, want errors.Is ErrPolicyAccessDenied", err)
	}
}

func TestEnsureTags_ETagConflictDoesNotRetryWithStalePolicy(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var posts int
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:existing":["user@example.com"]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			posts++
			return aclJSONResponse(http.StatusPreconditionFailed, `{"message":"etag conflict"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := EnsureTags(context.Background(), []string{"tag:new"})
	if !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("EnsureTags() error = %v, want errors.Is ErrPolicyConflict", err)
	}
	if posts != 1 {
		t.Fatalf("ACL POST count = %d, want exactly one failed write and no retry", posts)
	}
}

// ---------- EnsureFunnelAttr ----------

func funnelPolicyRequest() FunnelPolicyRequest {
	return FunnelPolicyRequest{
		Tags:   []string{"tag:tsmain", registry.FunnelTag},
		Target: registry.FunnelTag,
		Owners: []string{"tag:tsmain"},
	}
}

func TestEnsureFunnelAttr_LosslessHuJSONFusedPatch(t *testing.T) {
	aclSetup(t)
	// The groups/hosts/acls portion is the archived OpenAPI
	// ExampleHuJSONPolicyFile fixture; the remaining keys exercise realistic
	// human-maintained extensions and a future field unknown to this binary.
	input := `// Example/default ACLs for unrestricted connections.
{
  // Declare static groups of users beyond those in the identity service.
  "groups": {
    "group:example": ["user1@example.com", "user2@example.com"],
  },

  // Declare convenient hostname aliases to use in place of IP addresses.
  "hosts": {
    "example-host-1": "100.100.100.100",
  },

  // Existing tag ownership remains human-maintained.
  "tagOwners": {
    "tag:tsmain": ["autogroup:admin"],
  },

  // This unknown future key must survive exactly.
  "futurePolicyKey": {"opaque": [1, 2, 3], "enabled": true},

  "nodeAttrs": [
    {"target": ["tag:web", "tag:shared"], "attr": ["drive:share"]},
  ],

  // Access control lists.
  "acls": [
    // Match absolutely everything.
      // Comment this section out if you want to define specific restrictions.
    {"action": "accept", "src": ["*"], "dst": ["*:*"]},
  ],
}
`
	want := `// Example/default ACLs for unrestricted connections.
{
  // Declare static groups of users beyond those in the identity service.
  "groups": {
    "group:example": ["user1@example.com", "user2@example.com"],
  },

  // Declare convenient hostname aliases to use in place of IP addresses.
  "hosts": {
    "example-host-1": "100.100.100.100",
  },

  // Existing tag ownership remains human-maintained.
  "tagOwners": {
    "tag:tsmain": ["autogroup:admin"],
    "tag:tslink-funnel":["tag:tsmain"],
  },

  // This unknown future key must survive exactly.
  "futurePolicyKey": {"opaque": [1, 2, 3], "enabled": true},

  "nodeAttrs": [
    {"target": ["tag:web", "tag:shared"], "attr": ["drive:share"]},
    {"attr":["funnel"],"target":["tag:tslink-funnel"]},
  ],

  // Access control lists.
  "acls": [
    // Match absolutely everything.
      // Comment this section out if you want to define specific restrictions.
    {"action": "accept", "src": ["*"], "dst": ["*:*"]},
  ],
}
`

	var posted string
	gets, posts := 0, 0
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			gets++
			if got := r.Header.Get("Accept"); got != "application/hujson" {
				t.Errorf("GET Accept = %q, want application/hujson", got)
			}
			w.Header().Set("ETag", `"policy-v7"`)
			_, _ = w.Write([]byte(input))
		case http.MethodPost:
			posts++
			if got := r.Header.Get("Content-Type"); got != "application/hujson" {
				t.Errorf("POST Content-Type = %q, want application/hujson", got)
			}
			if got := r.Header.Get("If-Match"); got != `"policy-v7"` {
				t.Errorf("If-Match = %q, want quoted policy ETag", got)
			}
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				t.Errorf("read posted policy: %v", readErr)
			}
			posted = string(body)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if err != nil {
		t.Fatalf("EnsureFunnelAttr() error = %v", err)
	}
	if !result.Changed || result.WriteOutcome != PolicyWriteChanged || gets != 1 || posts != 1 {
		t.Fatalf("EnsureFunnelAttr() result=%+v gets=%d posts=%d, want confirmed one-GET/one-POST change", result, gets, posts)
	}
	if posted != want {
		t.Fatalf("lossless HuJSON patch mismatch (-want +got):\nwant:\n%s\ngot:\n%s", want, posted)
	}
	if !strings.Contains(logs.String(), "updated tailnet policy for Funnel") || !strings.Contains(logs.String(), "target=tag:tslink-funnel") {
		t.Fatalf("policy mutation log = %q, want visible target and mutation", logs.String())
	}
}

func TestEnsureFunnelAttr_MultiTargetGrantIsNeverWidened(t *testing.T) {
	aclSetup(t)
	var posted tailscale.ACL
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:tsmain"]},"nodeAttrs":[{"target":["tag:tslink-funnel","tag:shared"],"attr":["drive:share"]}]}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Errorf("decode posted policy: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if err != nil || !result.Changed {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want confirmed change", result, err)
	}
	if len(posted.NodeAttrs) != 2 {
		t.Fatalf("nodeAttrs = %+v, want original multi-target grant plus exact grant", posted.NodeAttrs)
	}
	if got := strings.Join(posted.NodeAttrs[0].Attr, ","); got != "drive:share" {
		t.Fatalf("multi-target grant attrs = %q, want unchanged drive:share", got)
	}
	if got := strings.Join(posted.NodeAttrs[1].Target, ","); got != registry.FunnelTag || strings.Join(posted.NodeAttrs[1].Attr, ",") != FunnelNodeAttr {
		t.Fatalf("exact grant = %+v, want only %s/funnel", posted.NodeAttrs[1], registry.FunnelTag)
	}
}

func TestEnsureFunnelAttr_WildcardFunnelCoverageIsIdempotent(t *testing.T) {
	aclSetup(t)
	posts := 0
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Error("idempotent wildcard coverage issued a policy write")
			return
		}
		_, _ = w.Write([]byte(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:tsmain"]},"nodeAttrs":[{"target":["*"],"attr":["funnel"]}]}`))
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if err != nil {
		t.Fatalf("EnsureFunnelAttr() error = %v", err)
	}
	if result.Changed || result.WriteOutcome != PolicyWriteUnchanged || posts != 0 {
		t.Fatalf("EnsureFunnelAttr() result=%+v posts=%d, want unchanged and zero writes", result, posts)
	}
}

func TestEnsureFunnelAttr_ExactTargetGrantCanBeExtended(t *testing.T) {
	aclSetup(t)
	var posted tailscale.ACL
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:tsmain"]},"nodeAttrs":[{"target":["tag:tslink-funnel"],"attr":["drive:share"]}]}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Errorf("decode posted policy: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if err != nil || !result.Changed {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v", result, err)
	}
	if len(posted.NodeAttrs) != 1 || strings.Join(posted.NodeAttrs[0].Attr, ",") != "drive:share,funnel" {
		t.Fatalf("exact nodeAttrs grant = %+v, want drive:share,funnel", posted.NodeAttrs)
	}
}

func TestEnsureFunnelAttr_DerivedOwnerIsAdditive(t *testing.T) {
	aclSetup(t)
	var posted tailscale.ACL
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"tagOwners":{"tag:tsmain":["autogroup:admin"],"tag:tslink-funnel":["tag:legacy-owner"]},"nodeAttrs":[{"target":["*"],"attr":["funnel"]}]}`))
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Errorf("decode posted policy: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if err != nil || !result.Changed {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v", result, err)
	}
	owners := posted.TagOwners[registry.FunnelTag]
	if strings.Join(owners, ",") != "tag:legacy-owner,tag:tsmain" {
		t.Fatalf("Funnel tag owners = %v, want existing owner plus derived service tag", owners)
	}
	if len(posted.NodeAttrs) != 1 || strings.Join(posted.NodeAttrs[0].Target, ",") != "*" {
		t.Fatalf("wildcard Funnel coverage changed unexpectedly: %+v", posted.NodeAttrs)
	}
}

func TestEnsureFunnelAttr_RefusesAmbiguousOrMalformedPolicyWithoutPOST(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		want   string
	}{
		{name: "duplicate tagOwners", policy: `{"tagOwners":{},"tagowners":{}}`, want: `duplicate "tagOwners"`},
		{name: "tagOwners is not object", policy: `{"tagOwners":[]}`, want: "tagOwners must be an object"},
		{name: "nodeAttrs is not array", policy: `{"tagOwners":{},"nodeAttrs":{}}`, want: "nodeAttrs must be an array"},
		{name: "grant target is not array", policy: `{"tagOwners":{},"nodeAttrs":[{"target":"tag:tslink-funnel","attr":["drive:share"]}]}`, want: "target must be an array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aclSetup(t)
			posts := 0
			aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					t.Error("malformed policy issued a POST")
					return
				}
				_, _ = w.Write([]byte(tc.policy))
			}))
			result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want %q", result, err, tc.want)
			}
			if result.WriteOutcome != PolicyWriteNotAttempted || posts != 0 {
				t.Fatalf("result=%+v posts=%d, want safe refusal", result, posts)
			}
		})
	}
}

func TestEnsureFunnelAttr_NoClient(t *testing.T) {
	aclSetup(t)
	aclClientFn = func() (*tailscale.Client, error) { return nil, nil }
	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if result.Changed || result.WriteOutcome != PolicyWriteNotAttempted || !errors.Is(err, ErrNoAPIClient) {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want unchanged ErrNoAPIClient", result, err)
	}
}

func TestEnsureFunnelAttr_HTTPSDisabledDoesNotReadOrWritePolicy(t *testing.T) {
	aclSetup(t)
	policyRequests := 0
	aclWithClientServerHTTPS(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policyRequests++
		t.Errorf("HTTPS-disabled preflight reached policy endpoint: %s %s", r.Method, r.URL.Path)
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if !errors.Is(err, ErrTailnetHTTPSDisabled) {
		t.Fatalf("EnsureFunnelAttr() error = %v, want ErrTailnetHTTPSDisabled", err)
	}
	if result.Changed || result.WriteOutcome != PolicyWriteNotAttempted || policyRequests != 0 {
		t.Fatalf("EnsureFunnelAttr() result=%+v policyRequests=%d, want no policy mutation attempt", result, policyRequests)
	}
	for _, want := range []string{"PATCH /api/v2/tailnet/{tailnet}/settings", "networking_settings"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("EnsureFunnelAttr() error = %q, want remedy %q", err, want)
		}
	}
}

func TestEnsureFunnelAttr_SettingsUnavailableDoesNotTouchPolicy(t *testing.T) {
	aclSetup(t)
	policyRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/settings") {
			http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
			return
		}
		policyRequests++
		t.Errorf("settings failure reached policy endpoint: %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	aclClientFn = func() (*tailscale.Client, error) {
		return &tailscale.Client{Tailnet: "-", APIKey: "fake", BaseURL: baseURL}, nil
	}

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if !errors.Is(err, ErrTailnetSettingsUnavailable) || !strings.Contains(err.Error(), "networking_settings") {
		t.Fatalf("EnsureFunnelAttr() error = %v, want scoped settings error", err)
	}
	if result.WriteOutcome != PolicyWriteNotAttempted || policyRequests != 0 {
		t.Fatalf("result=%+v policyRequests=%d, want no policy attempt", result, policyRequests)
	}
}

func TestEnsureFunnelAttr_RejectsEmptyTargetBeforeClient(t *testing.T) {
	aclSetup(t)
	aclClientFn = func() (*tailscale.Client, error) {
		t.Fatal("client created for empty target")
		return nil, nil
	}
	result, err := EnsureFunnelAttr(context.Background(), FunnelPolicyRequest{Owners: []string{"tag:tsmain"}})
	if result.Changed || err == nil || !strings.Contains(err.Error(), "target is empty") {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want empty-target rejection", result, err)
	}
}

func TestEnsureFunnelAttr_RequiresCallerDerivedOwner(t *testing.T) {
	aclSetup(t)
	request := funnelPolicyRequest()
	request.Owners = nil
	result, err := EnsureFunnelAttr(context.Background(), request)
	if result.Changed || err == nil || !strings.Contains(err.Error(), "no usable existing tag owner") {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want no-owner rejection", result, err)
	}
}

func TestEnsureFunnelAttr_ETagConflictIsRetryable(t *testing.T) {
	aclSetup(t)
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"policy-v1"`)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"message":"etag conflict"}`))
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if result.Changed || result.WriteOutcome != PolicyWriteRejected || !errors.Is(err, ErrPolicyConflict) {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want rejected retryable policy conflict", result, err)
	}
}

func TestEnsureFunnelAttr_TransportFailureReportsUnknownWriteOutcome(t *testing.T) {
	aclSetup(t)
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		panic(http.ErrAbortHandler)
	}))

	result, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest())
	if result.Changed || result.WriteOutcome != PolicyWriteUnknown || err == nil || !strings.Contains(err.Error(), "outcome is unknown") {
		t.Fatalf("EnsureFunnelAttr() result=%+v error=%v, want unknown write outcome", result, err)
	}
}

func TestPolicyMutatorsSerializeReadModifyWrite(t *testing.T) {
	aclSetup(t)
	var mu sync.Mutex
	policy := `{"tagOwners":{"tag:existing":["autogroup:admin"]}}`
	activeGets, maxActiveGets := 0, 0
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			activeGets++
			if activeGets > maxActiveGets {
				maxActiveGets = activeGets
			}
			body := policy
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			mu.Lock()
			activeGets--
			mu.Unlock()
			_, _ = w.Write([]byte(body))
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			policy = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}
	}))

	start := make(chan struct{})
	errs := make(chan error, 3)
	go func() { <-start; errs <- EnsureTags(context.Background(), []string{"tag:new"}) }()
	go func() { <-start; _, err := EnsureFunnelAttr(context.Background(), funnelPolicyRequest()); errs <- err }()
	go func() { <-start; errs <- DeleteTag(context.Background(), "tag:existing") }()
	close(start)
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent policy mutator error = %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActiveGets != 1 {
		t.Fatalf("max concurrent policy GETs = %d, want 1", maxActiveGets)
	}
}

// ---------- DeleteTag ----------

func TestDeleteTag_NoClient(t *testing.T) {
	aclSetup(t)
	err := DeleteTag(context.Background(), "tag:test")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want error")
	}
	if !errors.Is(err, ErrNoAPIClient) {
		t.Fatalf("DeleteTag() error = %v, want ErrNoAPIClient", err)
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

	var postedACL map[string]any
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

	tagOwners, ok := postedACL["tagowners"].(map[string]any)
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

func TestDeleteTag_FunnelTagCoRemovesCanonicalGrantLosslessly(t *testing.T) {
	aclSetup(t)
	input := `// human-maintained policy
{
  "tagOwners": {
    "tag:tsmain": ["autogroup:admin"] /* KEEPME_OBJECT_PRE_COMMA */,
    "tag:tslink-funnel": ["tag:tsmain"],
  },
  "nodeAttrs": [
    // TSLink-owned canonical grant.
    {"target":["tag:tslink-funnel"],"attr":["funnel"]},
    {"target":["tag:other"],"attr":["drive:share"]} /* KEEPME_ARRAY_PRE_COMMA */,
  ],
  "futurePolicyKey": {"untouched": true},
}
`
	var posted string
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"delete-v2"`)
			_, _ = w.Write([]byte(input))
			return
		}
		if got := r.Header.Get("If-Match"); got != `"delete-v2"` {
			t.Errorf("If-Match = %q, want delete-v2", got)
		}
		body, _ := io.ReadAll(r.Body)
		posted = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	if err := DeleteTag(context.Background(), registry.FunnelTag); err != nil {
		t.Fatalf("DeleteTag() error = %v", err)
	}
	for _, forbidden := range []string{`"tag:tslink-funnel"`, `"funnel"`, "TSLink-owned canonical grant"} {
		if strings.Contains(posted, forbidden) {
			t.Fatalf("posted policy still contains removed Funnel plumbing %q:\n%s", forbidden, posted)
		}
	}
	for _, preserved := range []string{"// human-maintained policy", `"tag:tsmain": ["autogroup:admin"]`, "KEEPME_OBJECT_PRE_COMMA", `{"target":["tag:other"],"attr":["drive:share"]}`, "KEEPME_ARRAY_PRE_COMMA", `"futurePolicyKey": {"untouched": true}`} {
		if !strings.Contains(posted, preserved) {
			t.Fatalf("posted policy lost unrelated bytes %q:\n%s", preserved, posted)
		}
	}
}

// Removing the final member of a trailing-comma container leaves the new final
// member without the comma the author wrote. hujson records AfterExtra only for
// the last member, so the restoration blocks in removeObjectMember and
// removeArrayElement are reachable exactly in this shape: the removed entry is
// last, and the surviving entry before it carries no comma of its own.
func TestDeleteTag_PreservesTrailingCommaWhenRemovingFinalEntry(t *testing.T) {
	aclSetup(t)
	input := `{
  "tagOwners": {
    "tag:tsmain": ["autogroup:admin"],
    "tag:tslink-funnel": ["tag:tsmain"],
  },
  "nodeAttrs": [
    {"target":["tag:other"],"attr":["drive:share"]},
    {"target":["tag:tslink-funnel"],"attr":["funnel"]},
  ],
}
`
	var posted string
	aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", `"trailing-comma"`)
			_, _ = w.Write([]byte(input))
			return
		}
		body, _ := io.ReadAll(r.Body)
		posted = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	if err := DeleteTag(context.Background(), registry.FunnelTag); err != nil {
		t.Fatalf("DeleteTag() error = %v", err)
	}

	want := `{
  "tagOwners": {
    "tag:tsmain": ["autogroup:admin"],
  },
  "nodeAttrs": [
    {"target":["tag:other"],"attr":["drive:share"]},
  ],
}
`
	if posted != want {
		t.Fatalf("posted policy lost the author's trailing commas.\n got:\n%s\nwant:\n%s", posted, want)
	}
}

func TestDeleteTag_RefusesAnyNonCanonicalNodeAttrsReference(t *testing.T) {
	cases := []struct {
		name   string
		tag    string
		policy string
	}{
		{
			name: "ordinary referenced tag",
			tag:  "tag:web",
			policy: `{"tagOwners":{"tag:web":["autogroup:admin"]},` +
				`"nodeAttrs":[{"target":["tag:web"],"attr":["drive:share"]}]}`,
		},
		{
			name: "Funnel multi-target grant",
			tag:  registry.FunnelTag,
			policy: `{"tagOwners":{"tag:tslink-funnel":["tag:tsmain"]},` +
				`"nodeAttrs":[{"target":["tag:tslink-funnel","tag:shared"],"attr":["funnel"]}]}`,
		},
		{
			name: "Funnel exact grant has another attr",
			tag:  registry.FunnelTag,
			policy: `{"tagOwners":{"tag:tslink-funnel":["tag:tsmain"]},` +
				`"nodeAttrs":[{"target":["tag:tslink-funnel"],"attr":["funnel","drive:share"]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aclSetup(t)
			posts := 0
			aclWithClientServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts++
					t.Error("refused deletion issued a policy write")
					return
				}
				_, _ = w.Write([]byte(tc.policy))
			}))
			err := DeleteTag(context.Background(), tc.tag)
			if err == nil || !strings.Contains(err.Error(), "still references") {
				t.Fatalf("DeleteTag() error = %v, want actionable reference refusal", err)
			}
			if posts != 0 {
				t.Fatalf("POST count = %d, want 0", posts)
			}
		})
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

func TestDeleteTag_ETagConflictDoesNotRetryWithStalePolicy(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}

	var posts int
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/acl"):
			return aclJSONResponse(http.StatusOK, `{"tagowners":{"tag:web":["autogroup:admin"]}}`), nil
		case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/acl"):
			posts++
			return aclJSONResponse(http.StatusPreconditionFailed, `{"message":"etag conflict"}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			return nil, nil
		}
	})

	err := DeleteTag(context.Background(), "tag:web")
	if err == nil {
		t.Fatal("DeleteTag() error = nil, want ETag conflict error")
	}
	if posts != 1 {
		t.Fatalf("ACL POST count = %d, want exactly one failed write and no retry", posts)
	}
}
