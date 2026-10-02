package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReview3InvalidUTF8CLIAndMCP(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	if _, err := registry.ChangePerson(paths.Registry, "alice\ufffd@example.com", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"add", "update", "remove"} {
		t.Run(op, func(t *testing.T) {
			login := "alice\xff@example.com"
			c := newPeopleCmd()
			c.SetOut(io.Discard)
			c.SetErr(io.Discard)
			args := []string{op, login}
			if op != "remove" {
				args = append(args, "--apps", "photos")
			}
			c.SetArgs(args)
			var coded registry.CodedError
			if err := c.Execute(); !errors.As(err, &coded) || coded.Code != "usage_error" || !strings.Contains(coded.Message, "UTF-8") {
				t.Error("CLI must reject invalid UTF-8 as usage_error", err)
			}
			// Raw JSON bytes must be checked before encoding/json repairs them.
			raw := json.RawMessage(`{"who":"` + login + `"`)
			if op != "remove" {
				raw = append(raw, `,"apps":["photos"]`...)
			}
			raw = append(raw, '}')
			res, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "people_"+op, raw)
			if err != nil || res == nil {
				t.Fatal(res, err)
			}
			content, ok := res.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatal("missing MCP text result")
			}
			var envelope output.ErrorObject
			if err := json.Unmarshal([]byte(content.Text), &envelope); err != nil {
				t.Fatal(err)
			}
			if !res.IsError || envelope.Code != "usage_error" || !strings.Contains(envelope.Message, "UTF-8") {
				t.Error("MCP must reject malformed wire identity as usage_error", content.Text)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid input mutated registry", err)
			}
		})
	}
	// A genuine replacement-character login remains a supported identity.
	res, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "people_update", json.RawMessage(`{"who":"alice\ufffd@example.com","apps":["photos"]}`))
	if err != nil || res.IsError {
		t.Fatal("valid Unicode control failed", res, err)
	}
}

func TestReview3CompiledCLIInvalidUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows UTF-16 argv cannot represent invalid UTF-8 bytes")
	}
	binary := compiledTSLinkBinary(t)
	for _, op := range []string{"add", "update", "remove"} {
		t.Run(op, func(t *testing.T) {
			paths := peopleTestPaths(t)
			valid := "alice\ufffd@example.com"
			if _, err := registry.ChangePerson(paths.Registry, valid, []string{"photos"}, nil, false, false); err != nil {
				t.Fatal(err)
			}
			control, stderr, exit := runTSLinkBinaryWithConfigDir(t, binary, filepath.Dir(paths.Registry), "", "people", "update", valid, "--apps", "photos", "--json")
			if exit != 0 {
				t.Fatal("valid Unicode compiled control failed", control, stderr, exit)
			}
			before, err := os.ReadFile(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"people", op, "alice\xff@example.com", "--json"}
			if op != "remove" {
				args = append(args, "--apps", "photos")
			}
			stdout, stderr, exit := runTSLinkBinaryWithConfigDir(t, binary, filepath.Dir(paths.Registry), "", args...)
			var result output.Result
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(stdout, stderr, exit, err)
			}
			if exit != 2 || result.OK || result.Error == nil || result.Error.Code != "usage_error" || !strings.Contains(result.Error.Message, "UTF-8") {
				t.Error("invalid argv was not a UTF-8 usage_error", stdout, stderr, exit)
			}
			after, err := os.ReadFile(paths.Registry)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid argv wrote registry", err)
			}
		})
	}
}

func TestReview3NoneRequiresCompleteListing(t *testing.T) {
	for _, state := range []string{registry.PersonInviteComplete, registry.PersonInviteUnknown} {
		for _, remove := range []bool{false, true} {
			for _, tc := range []struct {
				name, endpoint, body string
				status               int
			}{
				{"null-invites", "/device-invites", "null", 200},
				{"blank-invites", "/device-invites", "", 200},
				{"no-content-invites", "/device-invites", "", 204},
				{"partial-invites", "/device-invites", "[]", 206},
				{"partial-devices", "/devices", `{"devices":[]}`, 206},
			} {
				t.Run(state+"/"+map[bool]string{false: "update", true: "remove"}[remove]+"/"+tc.name, func(t *testing.T) {
					paths := peopleTestPaths(t)
					api := reviewPeopleAPI(t, paths)
					if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false); err != nil {
						t.Fatal(err)
					}
					op := registry.PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: state}
					if state == registry.PersonInviteComplete {
						op.ID = "1001"
					}
					if err := registry.SavePersonInvite(paths.Registry, "alice", op); err != nil {
						t.Fatal(err)
					}
					r3ListingOverlay(t, tc.endpoint, tc.status, tc.body)
					reviewRefreshProof(t, paths)
					complete := false
					if remove {
						result, err := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "none"})
						if err != nil {
							t.Fatal(err)
						}
						complete = result.Complete
					} else {
						result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "none"}}, true)
						if err != nil {
							t.Fatal(err)
						}
						complete = result.Complete
					}
					p, err := readPerson(paths.Registry, "alice")
					if err != nil {
						t.Fatal(err)
					}
					api.mu.Lock()
					defer api.mu.Unlock()
					if complete || !reflect.DeepEqual(p.Invites, []registry.PersonInvite{op}) || api.posts["photos"] != 0 || api.deletes != 0 {
						t.Fatal("incomplete evidence changed none reconciliation", complete, p.Invites, api.posts, api.deletes)
					}
				})
			}
		}
	}
}
