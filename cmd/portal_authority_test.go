package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/registry"
)

func portalRemoteCall(t *testing.T, srv *httptest.Server, tool string, args map[string]any) []byte {
	t.Helper()
	r := mcpHTTPToolCallRequest(tool)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	wire["params"].(map[string]any)["arguments"] = args
	b, err = json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	r.ContentLength, r.RequestURI = int64(len(b)), ""
	r.URL.Scheme, r.URL.Host = "http", strings.TrimPrefix(srv.URL, "http://")
	r.Host = r.URL.Host
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err = io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal(resp.StatusCode, err)
	}
	return b
}

func TestRequestInboxContentionCLIAndMCP(t *testing.T) {
	paths, _ := commandRequest(t)
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	// Far from the injected command clock and expired under the CLI clock.
	reg.Requests[0].CreatedAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	b, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Registry, b, 0600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(paths.Registry+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(lock)
	srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", defaultMCPActions(paths, io.Discard)))
	defer srv.Close()
	result := portalRemoteCall(t, srv, "requests_list", map[string]any{})
	if !bytes.Contains(result, []byte("access_request_busy")) {
		t.Fatal(string(result))
	}
	stdout, stderr, code := runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "requests", "list", "--json")
	if code != 4 || !strings.Contains(stdout, `"code":"access_request_busy"`) {
		t.Fatal(stdout, stderr, code)
	}
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	result = portalRemoteCall(t, srv, "requests_list", map[string]any{})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	stdout, stderr, code = runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "requests", "list", "--json")
	if code != 0 || !strings.Contains(stdout, `"ok":true`) {
		t.Fatal(stdout, stderr, code)
	}
}

func TestPortalRemoteAuthorityChanges(t *testing.T) {
	for _, kind := range []string{"replace", "clear", "admins", "bootstrap", "empty-owner", "revoked-owner", "action-authority"} {
		t.Run(kind, func(t *testing.T) {
			paths, request := commandRequest(t)
			login := "operator"
			args := map[string]any{"owner": "operator", "hostname": "home"}
			switch kind {
			case "clear":
				args["owner"] = ""
			case "admins":
				args["owner"], args["admins"] = "owner", []string{"operator"}
				if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"operator"}}); err != nil {
					t.Fatal(err)
				}
			case "bootstrap":
				if err := registry.SetPortal(paths.Registry, nil); err != nil {
					t.Fatal(err)
				}
			case "empty-owner":
				b, err := os.ReadFile(paths.Registry)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(b, []byte(`"owner": "owner"`)) {
					t.Fatal("owner fixture missing")
				}
				if err := os.WriteFile(paths.Registry, bytes.Replace(b, []byte(`"owner": "owner"`), []byte(`"owner": ""`), 1), 0600); err != nil {
					t.Fatal(err)
				}
			case "revoked-owner":
				login = "owner"
				if _, err := registry.ChangePerson(paths.Registry, login, []string{request.App}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.RemovePerson(paths.Registry, login); err != nil {
					t.Fatal(err)
				}
			}
			actions := defaultMCPActions(paths, io.Discard)
			if kind == "action-authority" {
				// The action checks authority when dispatcher prevalidation is absent.
				actions.portalCheck = nil
			}
			srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{login}, login, actions))
			defer srv.Close()
			approveArgs := map[string]any{"id": request.ID, "for": "8h"}
			initial := portalRemoteCall(t, srv, "requests_approve", approveArgs)
			if !bytes.Contains(initial, []byte("access_request_owner_required")) {
				t.Fatal("initial owner denial", string(initial))
			}
			before, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			changed := portalRemoteCall(t, srv, "portal_enable", args)
			if !bytes.Contains(changed, []byte("access_request_owner_required")) {
				t.Error("authority change denial", string(changed))
			}
			approved := portalRemoteCall(t, srv, "requests_approve", approveArgs)
			if !bytes.Contains(approved, []byte("access_request_owner_required")) {
				t.Error("subsequent owner denial", string(approved))
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("denied authority change modified registry", err)
			}
		})
	}
}

func TestPortalCurrentOwnerAndLocalRecovery(t *testing.T) {
	paths, _ := commandRequest(t)
	srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", defaultMCPActions(paths, io.Discard)))
	defer srv.Close()
	result := portalRemoteCall(t, srv, "portal_enable", map[string]any{"owner": "replacement", "admins": []string{"helper"}})
	if bytes.Contains(result, []byte(`"isError":true`)) {
		t.Fatal(string(result))
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil || reg.Portal.Owner != "replacement" || len(reg.Portal.Admins) != 1 || reg.Portal.Admins[0] != "helper" {
		t.Fatal(reg, err)
	}
	result = portalRemoteCall(t, srv, "portal_enable", map[string]any{"owner": "owner"})
	if !bytes.Contains(result, []byte("access_request_owner_required")) {
		t.Fatal("previous owner retained authority", string(result))
	}
	// The compiled local CLI is the trusted recovery path for a lost identity.
	stdout, stderr, code := runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "portal", "enable", "--owner", "recovered", "--json")
	if code != 0 || !strings.Contains(stdout, `"ok":true`) {
		t.Fatal(stdout, stderr, code)
	}
	reg, _, err = registry.Preflight(paths.Registry)
	if err != nil || reg.Portal.Owner != "recovered" {
		t.Fatal(reg, err)
	}
	// Stdio MCP has no remote caller and uses the same local owner process.
	if err := registry.SetPortal(paths.Registry, nil); err != nil {
		t.Fatal(err)
	}
	local, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "portal_enable", json.RawMessage(`{"owner":"bootstrapped"}`))
	if err != nil || local.IsError {
		t.Fatal(local, err)
	}
	reg, _, err = registry.Preflight(paths.Registry)
	if err != nil || reg.Portal.Owner != "bootstrapped" {
		t.Fatal(reg, err)
	}
}
