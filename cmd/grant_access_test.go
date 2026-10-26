package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/anydoor7/tslink/internal/output"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func TestGrantAccessCompatibilityAdapters(t *testing.T) {
	for _, entry := range []string{"cli-add", "mcp-add", "mcp-share", "invite-device-email", "mcp-invite-device", "mcp-people-add", "mcp-people-update", "mcp-people-grant", "requests-approve"} {
		t.Run(entry, func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			f.login = "alice@example.com"
			restoreShareSeams(t)
			shareIsRunningFn = func(string) bool { return true }
			oldEnsure := ensureDaemonFn
			t.Cleanup(func() { ensureDaemonFn = oldEnsure })
			ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
			actions := defaultMCPActions(f.paths, io.Discard)
			var value any
			var err error
			var requestID string
			if entry == "mcp-people-update" {
				if _, err := registry.ChangePerson(f.paths.Registry, f.login, []string{"photos"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
			}
			if entry == "requests-approve" {
				svc, e := loadPersistedService(f.paths.Registry, "photos")
				if e != nil {
					t.Fatal(e)
				}
				svc.Requestable = true
				if _, e := registry.Add(f.paths.Registry, svc); e != nil {
					t.Fatal(e)
				}
				r, e := registry.SubmitAccessRequest(f.paths.Registry, f.login, "photos", "never", "", peopleNowFn())
				if e != nil {
					t.Fatal(e)
				}
				requestID = r.ID
			}
			call := func() {
				switch entry {
				case "cli-add":
					oldReg := registryPathFn
					t.Cleanup(func() { registryPathFn = oldReg })
					registryPathFn = func() (string, error) { return f.paths.Registry, nil }

					value, err = runAddCmdOutput(t, []string{"photos"}, map[string]string{"proxy": "localhost:3000", "allow": f.login, "for": "never", "ack-never": "true"})
				case "mcp-add":
					value, err = callMCPTool(context.Background(), actions, "add", json.RawMessage(`{"name":"photos","type":"proxy","target":"localhost:3000","allow":["alice@example.com"],"for":"never","ack_never":true}`))
				case "mcp-share":
					value, err = callMCPTool(context.Background(), actions, "share", json.RawMessage(`{"name":"photos","target":"3000","ephemeral":false,"allow":["alice@example.com"],"for":"never","ack_never":true}`))
				case "invite-device-email":
					f.emailMode = true
					value, err = inviteDeviceCreate(context.Background(), f.paths.Registry, f.paths.PID, f.paths.Snapshot, "photos", f.login, false, false, false)
				case "mcp-invite-device":
					f.emailMode = true
					value, err = callMCPTool(context.Background(), actions, "invite_device", json.RawMessage(`{"service":"photos","email":"alice@example.com"}`))
				case "mcp-people-add", "mcp-people-update":
					tool := strings.ReplaceAll(strings.TrimPrefix(entry, "mcp-"), "-", "_")
					value, err = callMCPTool(context.Background(), actions, tool, json.RawMessage(`{"who":"alice@example.com","apps":["photos"],"for":"never","ack_never":true}`))
				case "mcp-people-grant":
					value, err = callMCPTool(context.Background(), actions, "people_grant", json.RawMessage(`{"who":"alice@example.com","app":"photos","for":"never","ack_never":true}`))
				case "requests-approve":
					value, err = decideRequest(f.paths.Registry, requestDecisionArguments{ID: requestID, For: "never", AckNever: true}, true, peopleNowFn())
				}
			}
			call()
			wire, _ := json.Marshal(value)
			if err != nil || bytes.Contains(wire, []byte(`"isError":true`)) {
				t.Fatalf("adapter failed: %s %v", wire, err)
			}
			person, err := readPerson(f.paths.Registry, f.login)
			if err != nil || !person.Guest || len(person.Grants) != 1 || !registry.PersonGrantActiveAt(person, "photos", peopleNowFn()) {
				t.Fatal("adapter did not grant access", person, err)
			}
			if posts, _ := f.counts(); posts != 1 {
				t.Fatal("adapter did not send exactly one gateway invitation", posts)
			}
			if bytes.Contains(wire, []byte("fixture.invalid/invite")) {
				t.Fatal("adapter disclosed invitation by default")
			}
			call()
			wire, _ = json.Marshal(value)
			if err != nil || bytes.Contains(wire, []byte(`"isError":true`)) {
				t.Fatalf("retry failed: %s %v", wire, err)
			}
			if posts, _ := f.counts(); posts != 1 {
				t.Fatal("adapter retry sent duplicate invitation", posts)
			}
		})
	}
}

// Legacy command fixtures state their directory and daemon assumptions explicitly.
// Resolver and exact-instance behavior are exercised separately with real files/API.
func grantTestEnvironment(t *testing.T, membership tailapi.Membership) {
	t.Helper()
	oldMembership, oldRuntime := peopleMembershipFn, peopleRuntimeFn
	t.Cleanup(func() { peopleMembershipFn, peopleRuntimeFn = oldMembership, oldRuntime })
	peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) { return membership, nil }
	peopleRuntimeFn = func(_ context.Context, paths sharePaths, apps []string) (tsruntime.PortalState, map[string]string, map[string]tailapi.DeviceTarget) {
		urls, targets := map[string]string{}, map[string]tailapi.DeviceTarget{}
		reg, err := registry.Load(paths.Registry)
		if err != nil {
			t.Fatal(err)
		}
		for i, svc := range reg.Services {
			urls[svc.Name] = "https://" + svc.Name + ".tail.test.ts.net"
			node := fmt.Sprintf("n%d", i+1)
			if svc.Name == "photos" {
				node = "n1"
			}
			if svc.Name == "finance" {
				node = "n2"
			}
			targets[svc.Name] = tailapi.DeviceTarget{Service: svc.Name, Hostname: svc.Name, NodeID: node}
		}
		pid, _ := inviteReadPIDFn(paths.PID)
		portal := readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
		return portal, urls, targets
	}
}

func TestGrantAccessTruthTable(t *testing.T) {
	for _, gateway := range []bool{true, false} {
		for _, member := range []bool{true, false} {
			for _, kind := range []string{"routed", "unrouted", "file", "tcp"} {
				t.Run(fmt.Sprintf("gateway=%v/member=%v/%s", gateway, member, kind), func(t *testing.T) {
					f := newGatewayInviteFixture(t)
					peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) {
						if member {
							return tailapi.MembershipMember, nil
						}
						return tailapi.MembershipGuest, nil
					}
					reg, _ := registry.Load(f.paths.Registry)
					svc := reg.Services[0]
					for _, s := range reg.Services {
						if s.Name == "photos" {
							svc = s
						}
					}
					if kind == "unrouted" || kind == "tcp" {
						svc.PortalRoute = nil
					}
					if kind == "file" {
						svc.Type = registry.TypeFile
						svc.Target = ""
						svc.Path = t.TempDir()
					}
					if kind == "tcp" {
						if _, err := registry.Remove(f.paths.Registry, "photos"); err != nil {
							t.Fatal(err)
						}
						svc.Type = registry.TypeTCP
						svc.Target = "localhost:3333"
						svc.Port = 3333
						svc.AllowedUsers = nil
						svc.PeopleScoped = false
					}
					if _, err := registry.Add(f.paths.Registry, svc); err != nil {
						t.Fatal(err)
					}
					if !gateway {
						if err := registry.DisablePortal(f.paths.Registry); err != nil {
							t.Fatal(err)
						}
					}
					before, _ := os.ReadFile(f.paths.Registry)
					got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false)
					posts, _ := f.counts()
					if kind == "tcp" {
						after, _ := os.ReadFile(f.paths.Registry)
						if err == nil || posts != 0 || !bytes.Equal(before, after) {
							t.Fatal("unsupported TCP mutated state", err, posts)
						}
						return
					}
					if err != nil || !got.Complete || len(got.Access) != 1 {
						t.Fatal(got, err)
					}
					want := 0
					if !member {
						want = 1
					}
					if posts != want {
						t.Fatalf("POSTs=%d want %d", posts, want)
					}
					reg, _ = registry.Load(f.paths.Registry)
					for _, s := range reg.Services {
						if s.Name == "photos" {
							svc = s
						}
					}
					if gateway {
						if svc.PortalRoute == nil || !strings.Contains(got.Access[0].URL, "home.tailnet.ts.net:") {
							t.Fatal("gateway route not ready", svc, got.Access)
						}
					} else if got.Access[0].URL != "https://photos.tail.test.ts.net" {
						t.Fatal("disabled gateway reused route URL", got.Access)
					}
					if !member {
						kind := ""
						if gateway {
							kind = registry.InviteTargetGateway
						}
						if got.Invites[0].TargetKind != kind {
							t.Fatal("wrong exact target", got.Invites)
						}
					}
					first, _ := readPerson(f.paths.Registry, "alice")
					again, e := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false)
					if e != nil || !again.Complete {
						t.Fatal(again, e)
					}
					current, _ := readPerson(f.paths.Registry, "alice")
					if !reflect.DeepEqual(first.Grants, current.Grants) {
						t.Fatal("retry moved deadline")
					}
					if n, _ := f.counts(); n != want {
						t.Fatal("retry duplicated invitation", n)
					}
				})
			}
		}
	}
}

func TestGrantAccessBundlePermanentAndDisclosure(t *testing.T) {
	for _, gateway := range []bool{true, false} {
		t.Run(fmt.Sprint(gateway), func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			if _, err := registry.Add(f.paths.Registry, registry.Service{Name: "files", Type: registry.TypeFile, Path: t.TempDir(), AllowedUsers: []string{"owner"}}); err != nil {
				t.Fatal(err)
			}
			if !gateway {
				if err := registry.DisablePortal(f.paths.Registry); err != nil {
					t.Fatal(err)
				}
			}
			got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance", "files"}, For: ptrString("never"), AckNever: true}, false)
			if err != nil || !got.Complete {
				t.Fatal(got, err)
			}
			want := 3
			if gateway {
				want = 1
			}
			if posts, _ := f.counts(); posts != want {
				t.Fatal("bundle target dedup", posts, want)
			}
			p, _ := readPerson(f.paths.Registry, "alice")
			for _, g := range p.Grants {
				if g.ExpiresAt != nil {
					t.Fatal("guest permanent lost", p)
				}
			}
			wire, _ := json.Marshal(got)
			disk, _ := os.ReadFile(f.paths.Registry)
			if bytes.Contains(wire, []byte("fixture.invalid/invite")) || bytes.Contains(disk, []byte("fixture.invalid/invite")) {
				t.Fatal("bearer disclosed by default")
			}
			validateAgainstToolOutputSchema(t, "people_add", got)
			shown, e := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance", "files"}, PrintLinks: true}, false)
			if e != nil || shown.Invites[0].InviteURL == "" {
				t.Fatal("explicit disclosure unavailable", shown, e)
			}
			if posts, _ := f.counts(); posts != want {
				t.Fatal("disclosure sent another invitation", posts)
			}
			if _, e := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", For: ptrString("never")}, true); e == nil {
				t.Fatal("never without ack accepted")
			}
		})
	}
}

func TestGrantAccessUnknownMembershipHasNoEffects(t *testing.T) {
	f := newGatewayInviteFixture(t)
	peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) {
		return "", registry.CodedError{Code: "membership_unknown", Message: "directory unavailable"}
	}
	before, _ := os.ReadFile(f.paths.Registry)
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false)
	after, _ := os.ReadFile(f.paths.Registry)
	if err == nil || got.Complete || !bytes.Equal(before, after) {
		t.Fatal("unknown directory mutated authority", got, err)
	}
	if posts, _ := f.counts(); posts != 0 {
		t.Fatal("unknown directory sent invite", posts)
	}
	failure := output.NewFailureForError("people add", err)
	if failure.OK || failure.Error.Data == nil {
		t.Fatal("partial state absent from error envelope", failure)
	}
}
