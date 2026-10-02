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

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/server"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func peopleAuthorityServer(t *testing.T, login string, tagged bool, actions mcpActions) *httptest.Server {
	t.Helper()
	client := mcpHTTPWhoIsClient(t, login)
	allow := []string{login}
	if tagged {
		allow = []string{"tag:operator"}
		client.Transport = mcpHTTPRoundTripper(func(*http.Request) (*http.Response, error) {
			body, err := json.Marshal(apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: login}, Node: &tailcfg.Node{Tags: allow}})
			if err != nil {
				return nil, err
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
	}
	return httptest.NewServer(server.NewMCPControlPlaneHandler(&server.MCPControlPlane{AllowedUsers: allow, Handler: newMCPStreamableHandler(actions)}, client))
}

func TestPeopleRemoteAuthorityMutationMatrix(t *testing.T) {
	for _, tool := range []string{"people_add", "people_update", "people_remove", "extend"} {
		for _, target := range []string{"owner", "admin", "ordinary"} {
			for _, caller := range []string{"owner", "operator", "admin", "revoked-owner", "tagged-owner"} {
				t.Run(tool+"/"+target+"/"+caller, func(t *testing.T) {
					paths, request := commandRequest(t)
					if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Admins: []string{"admin"}}); err != nil {
						t.Fatal(err)
					}
					if tool != "people_add" {
						if _, err := registry.ChangePerson(paths.Registry, target, []string{request.App}, nil, false, false); err != nil {
							t.Fatal(err)
						}
					} else if _, err := registry.RemovePerson(paths.Registry, target); err != nil {
						t.Fatal(err)
					}
					login := caller
					if caller == "revoked-owner" || caller == "tagged-owner" {
						login = "owner"
					}
					if caller == "revoked-owner" {
						if _, err := registry.RemovePerson(paths.Registry, login); err != nil {
							t.Fatal(err)
						}
					}
					srv := peopleAuthorityServer(t, login, caller == "tagged-owner", defaultMCPActions(paths, io.Discard))
					defer srv.Close()
					before, err := os.ReadFile(paths.Registry)
					if err != nil {
						t.Fatal(err)
					}
					args := map[string]any{"who": target}
					if tool == "people_add" || tool == "people_update" {
						args["apps"], args["for"] = []string{request.App}, "8h"
					} else if tool == "extend" {
						args["service"], args["for"] = request.App, "8h"
					}
					body := portalRemoteCall(t, srv, tool, args)
					// An add restores a tombstone, so a tombstoned owner cannot
					// authorize it even when it is the caller.
					deny := target != "ordinary" && (caller != "owner" || (tool == "people_add" && target == "owner"))
					if deny {
						if !bytes.Contains(body, []byte(`"isError":true`)) || !bytes.Contains(body, []byte("access_request_owner_required")) {
							t.Fatal("protected person mutation was not refused with owner code", string(body))
						}
						after, err := os.ReadFile(paths.Registry)
						if err != nil || !bytes.Equal(before, after) {
							t.Fatal("refused mutation changed registry bytes", err)
						}
					} else if bytes.Contains(body, []byte(`"isError":true`)) {
						t.Fatal("authorized or ordinary-person control failed", string(body))
					}
				})
			}
		}
	}
}

func TestPeopleLocalRevokedOwnerRecovery(t *testing.T) {
	for _, transport := range []string{"cli", "stdio-action"} {
		t.Run(transport, func(t *testing.T) {
			paths, request := commandRequest(t)
			if _, err := registry.RemovePerson(paths.Registry, "owner"); err != nil {
				t.Fatal(err)
			}
			if transport == "cli" {
				out, stderr, code := runTSLinkBinaryWithConfigDir(t, compiledTSLinkBinary(t), filepath.Dir(paths.Registry), "", "people", "add", "owner", "--apps", request.App, "--for", "8h", "--json")
				if code != 0 || !strings.Contains(out, `"ok":true`) {
					t.Fatal(out, stderr, code)
				}
			} else {
				wire, err := json.Marshal(map[string]any{"who": "owner", "apps": []string{request.App}, "for": "8h"})
				if err != nil {
					t.Fatal(err)
				}
				result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "people_add", wire)
				if err != nil || result.IsError {
					t.Fatal(result, err)
				}
			}
			reg, _, err := registry.Preflight(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			restored := false
			for _, p := range reg.People {
				if p.Login == "owner" {
					restored = !p.Revoked && len(p.Grants) == 1
				}
			}
			if !restored {
				t.Fatal("local recovery did not restore owner grants and tombstone")
			}
			srv := httptest.NewServer(mcpHTTPControlPlaneHandler(t, []string{"owner"}, "owner", defaultMCPActions(paths, io.Discard)))
			defer srv.Close()
			result := portalRemoteCall(t, srv, "requests_approve", map[string]any{"id": request.ID, "for": "8h"})
			if bytes.Contains(result, []byte(`"isError":true`)) {
				t.Fatal("locally recovered owner could not approve remotely", string(result))
			}
		})
	}
}

func TestRequestToolsUseCurrentAuthority(t *testing.T) {
	for _, tool := range []string{"requests_list", "requests_approve", "requests_deny"} {
		for _, change := range []string{"unchanged", "replace", "revoke"} {
			t.Run(tool+"/"+change, func(t *testing.T) {
				paths, request := commandRequest(t)
				oldClock := peopleNowFn
				peopleNowFn = func() time.Time {
					if change == "replace" {
						if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "replacement"}); err != nil {
							t.Fatal(err)
						}
					} else if change == "revoke" {
						if _, err := registry.RemovePerson(paths.Registry, "owner"); err != nil {
							t.Fatal(err)
						}
					}
					return oldClock()
				}
				actions := defaultMCPActions(paths, io.Discard)
				peopleNowFn = oldClock
				srv := peopleAuthorityServer(t, "owner", false, actions)
				defer srv.Close()
				args := map[string]any{}
				if tool != "requests_list" {
					args["id"] = request.ID
					if tool == "requests_approve" {
						args["for"] = "8h"
					}
				}
				body := portalRemoteCall(t, srv, tool, args)
				if change != "unchanged" {
					if !bytes.Contains(body, []byte(`"isError":true`)) || !bytes.Contains(body, []byte("access_request_owner_required")) {
						t.Fatal("request tool retained stale authority", string(body))
					}
					reg, _, err := registry.Preflight(paths.Registry)
					if err != nil || reg.Requests[0].Status != registry.RequestPending {
						t.Fatal("denied request decision committed", reg, err)
					}
				} else if bytes.Contains(body, []byte(`"isError":true`)) {
					t.Fatal("current owner control failed", string(body))
				}
			})
		}
	}
}
