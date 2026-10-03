package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func commandRequest(t *testing.T) (sharePaths, registry.AccessRequest) {
	t.Helper()
	paths := peopleTestPaths(t)
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	app := reg.Services[0]
	app.Requestable = true
	if _, err := registry.Add(paths.Registry, app); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortal(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	r, err := registry.SubmitAccessRequest(paths.Registry, "alice", app.Name, "3d", "<script>\x1b[2J\u009b31m", peopleNowFn())
	if err != nil {
		t.Fatal(err)
	}
	return paths, r
}

func TestRequestsCommandMCPAndHooks(t *testing.T) {
	paths, r := commandRequest(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	old := requestDecidedFn
	t.Cleanup(func() { requestDecidedFn = old })
	events := []registry.RequestEvent{}
	requestDecidedFn = func(e registry.RequestEvent) { events = append(events, e) }
	group := newRequestsCmd()
	var out bytes.Buffer
	group.SetOut(&out)
	group.SetArgs([]string{"list"})
	if err := group.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\u009b") || !strings.Contains(out.String(), "Visitor note (untrusted):") || !strings.Contains(out.String(), r.ID) {
		t.Fatal(out.String())
	}
	group = newRequestsCmd()
	group.SetOut(&out)
	group.SetArgs([]string{"approve", r.ID, "--for", "1d12h"})
	if err := group.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != "approved" || events[0].Who != "alice" {
		t.Fatal(events)
	}
	tools := defaultMCPActions(paths, io.Discard)
	for _, tc := range []struct {
		name, args string
		fail       bool
	}{
		{"requests_list", `{}`, false},
		{"requests_approve", `{"id":"` + r.ID + `","for":"1d12h"}`, false},
		{"requests_approve", `{"id":"` + r.ID + `","for":"8h"}`, true},
		{"requests_deny", `{"id":"` + r.ID + `"}`, true},
		{"requests_approve", `{"id":"` + r.ID + `"}`, true},
		{"requests_deny", `{"id":"` + r.ID + `","for":"8h"}`, true},
		{"requests_approve", `{"id":"` + r.ID + `","for":"8h","reason":"x"}`, true},
		{"requests_list", `{"unexpected":1}`, true},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			result, err := callMCPTool(context.Background(), tools, tc.name, json.RawMessage(tc.args))
			if err != nil || result.IsError != tc.fail {
				t.Fatal(result, err)
			}
		})
	}
	if len(events) != 1 {
		t.Fatal("retry emitted duplicate decision events", events)
	}
	listed, err := listRequests(paths.Registry, peopleNowFn())
	if err != nil || listed.Requests[0].Grant == nil || !listed.Requests[0].Grant.ExpiresAt.Equal(peopleNowFn().Add(36*time.Hour)) {
		t.Fatal(listed, err)
	}
	for _, definition := range mcpToolDefinitions {
		if strings.HasPrefix(definition.Name, "requests_") && !definition.OwnerOnly {
			t.Fatal(definition)
		}
	}
	state, err := mcpEventsSnapshotFn(tools)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(state)
	if !bytes.Contains(wire, []byte(`"access_requests"`)) || !bytes.Contains(wire, []byte(r.ID)) || bytes.Contains(wire, []byte("<script>")) || bytes.Contains(wire, []byte("[2J")) {
		t.Fatal(string(wire))
	}
}

func TestRequestsCompiledCLIEnvelopeAndRequestableFlag(t *testing.T) {
	paths, r := commandRequest(t)
	dir := filepath.Dir(paths.Registry)
	binary := compiledTSLinkBinary(t)
	stdout, stderr, code := runTSLinkBinaryWithConfigDir(t, binary, dir, "", "requests", "list", "--json")
	var envelope struct {
		OK            bool              `json:"ok"`
		SchemaVersion int               `json:"schema_version"`
		Command       string            `json:"command"`
		Data          RequestListResult `json:"data"`
	}
	if json.Unmarshal([]byte(stdout), &envelope) != nil || !envelope.OK || envelope.SchemaVersion != 1 || envelope.Command != "requests list" || len(envelope.Data.Requests) != 1 || code != 0 {
		t.Fatal(stdout, stderr, code)
	}
	stdout, stderr, code = runTSLinkBinaryWithConfigDir(t, binary, dir, "", "requests", "deny", r.ID, "--reason", "no time", "--json")
	if code != 0 || !strings.Contains(stdout, `"status":"denied"`) {
		t.Fatal(stdout, stderr, code)
	}
	stdout, stderr, code = runTSLinkBinaryWithConfigDir(t, binary, dir, "", "requests", "approve", r.ID, "--for", "8h", "--json")
	if code != 4 || !strings.Contains(stdout, `"code":"access_request_decided"`) {
		t.Fatal(stdout, stderr, code)
	}
	stdout, stderr, code = runTSLinkBinaryWithConfigDir(t, binary, dir, "", "add", "calendar", "--proxy", "localhost:3000", "--requestable", "--no-daemon-install", "--json")
	if code != 0 {
		t.Fatal(stdout, stderr, code)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range reg.Services {
		if s.Name == "calendar" {
			found = s.Requestable
		}
	}
	if !found {
		t.Fatal("compiled add dropped requestable flag", reg.Services)
	}
	stdout, stderr, code = runTSLinkBinaryWithConfigDir(t, binary, dir, "", "add", "raw", "--tcp", "localhost:3000", "--requestable", "--no-daemon-install", "--json")
	if code != 2 {
		t.Fatal(stdout, stderr, code)
	}
	// Local JSON contains note escapes, never executable terminal controls.
	stdout, _, _ = runTSLinkBinaryWithConfigDir(t, binary, dir, "", "requests", "list")
	if strings.ContainsAny(stdout, "\x1b\u009b") {
		t.Fatal(stdout)
	}
}

func TestRequestsDenyJSONAndPermanentMember(t *testing.T) {
	paths, r := commandRequest(t)
	args := requestDecisionArguments{ID: r.ID, For: "never", AckNever: true}
	result, err := decideRequest(paths.Registry, args, true, peopleNowFn())
	if err != nil || result.Request.Grant.ExpiresAt != nil {
		t.Fatal(result, err)
	}
	var out bytes.Buffer
	writeRequestResult(&out, "requests approve", result, true)
	if !json.Valid(out.Bytes()) {
		t.Fatal(out.String())
	}
	out.Reset()
	writeRequestResult(&out, "requests list", RequestListResult{}, false)
	if out.String() != "No access requests.\n" {
		t.Fatal(out.String())
	}
	_, err = decideRequest(paths.Registry, requestDecisionArguments{ID: r.ID}, true, time.Now())
	if err == nil {
		t.Fatal("missing lifetime accepted")
	}
	if _, err := listRequests(filepath.Join(t.TempDir(), "missing.json"), peopleNowFn()); err != nil {
		t.Fatal(err)
	}
	// Corrupt policy is a refusal, and does not accidentally grant access.
	if err := os.WriteFile(filepath.Join(filepath.Dir(paths.Registry), "config.json"), []byte(`{"durations":{"public_max":"bad"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = decideRequest(paths.Registry, args, true, peopleNowFn())
	if err == nil {
		t.Fatal("corrupt policy ignored")
	}
}

func TestRequestsMCPRemoteOwnerThroughListener(t *testing.T) {
	paths, r := commandRequest(t)
	actions := defaultMCPActions(paths, io.Discard)
	for _, login := range []string{"visitor", "admin", "owner", "revoked-owner"} {
		t.Run(login, func(t *testing.T) {
			identity := login
			if login == "revoked-owner" {
				identity = "owner"
				if _, err := registry.ChangePerson(paths.Registry, "owner", []string{"photos"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				if _, err := registry.RemovePerson(paths.Registry, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			h := mcpHTTPControlPlaneHandler(t, []string{identity}, identity, actions)
			srv := httptest.NewServer(h)
			defer srv.Close()
			for _, tool := range []string{"requests_list", "requests_approve", "requests_deny"} {
				// Deny/approve positive controls use independent requests.
				args := map[string]any{}
				if tool != "requests_list" {
					args["id"] = r.ID
				}
				if tool == "requests_approve" {
					args["for"] = "8h"
				}
				request := mcpHTTPToolCallRequest(tool)
				var wire map[string]any
				b, _ := io.ReadAll(request.Body)
				if err := json.Unmarshal(b, &wire); err != nil {
					t.Fatal(err)
				}
				wire["params"].(map[string]any)["arguments"] = args
				b, _ = json.Marshal(wire)
				request.Body = io.NopCloser(bytes.NewReader(b))
				request.ContentLength = int64(len(b))
				request.RequestURI = ""
				request.URL.Scheme = "http"
				request.URL.Host = strings.TrimPrefix(srv.URL, "http://")
				request.Host = request.URL.Host
				resp, err := srv.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatal(resp.StatusCode, string(body))
				}
				if login != "owner" {
					if !bytes.Contains(body, []byte("access_request_owner_required")) {
						t.Fatal("remote non-owner reached request tools", string(body))
					}
				} else if tool != "requests_deny" {
					if bytes.Contains(body, []byte(`"isError":true`)) || !bytes.Contains(body, []byte(r.ID)) {
						t.Fatal("owner context was not preserved through SDK", string(body))
					}
				}
			}
		})
	}
}

func TestRequestsCLIFileAndPathFailures(t *testing.T) {
	paths, r := commandRequest(t)
	restoreInviteCommandSeams(t)
	for _, args := range [][]string{{"list"}, {"approve", r.ID, "--for", "8h"}, {"deny", r.ID}} {
		inviteRegistryPathFn = func() (string, error) { return "", errors.New("isolated path unavailable") }
		c := newRequestsCmd()
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("unavailable registry path accepted", args)
		}
	}
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	c := newRequestsCmd()
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"deny", r.ID, "--reason", "Please try later\x1b[2J"})
	if err := c.Execute(); err != nil || strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "Reason: Please try later") {
		t.Fatal(out.String(), err)
	}
	if err := os.WriteFile(paths.Registry, []byte(`{"schema_version":`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"list"}, {"approve", r.ID, "--for", "8h"}} {
		c := newRequestsCmd()
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("partial registry accepted", args)
		}
	}
}
