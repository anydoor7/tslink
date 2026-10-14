package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

type gatewayInviteFixture struct {
	paths          sharePaths
	mu             sync.Mutex
	posts, deletes int
	pending        map[string]string
	accepted       map[string]bool
	fail, partial  bool
	listBody       string
	beforePost     func()
	beforeList     func()
	nodeID         string
}

func newGatewayInviteFixture(t *testing.T) *gatewayInviteFixture {
	t.Helper()
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	f := &gatewayInviteFixture{paths: paths, nodeID: "gateway-node", pending: map[string]string{}, accepted: map[string]bool{}}
	now := peopleNowFn()
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	oldMod := pidFileModTimeFn
	pidFileModTimeFn = func(string) (time.Time, error) { return now, nil }
	t.Cleanup(func() { pidFileModTimeFn = oldMod })
	portal := &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner", Gateway: &registry.GatewayConfig{Enabled: true, Ports: registry.DefaultGatewayPorts()}}
	if err := registry.SetPortal(paths.Registry, portal); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"photos", "finance"} {
		if err := registry.SetPortalRoute(context.Background(), paths.Registry, app, &registry.PortalRoute{}); err != nil {
			t.Fatal(err)
		}
	}
	ledger := filepath.Join(filepath.Dir(paths.Registry), "portal-nodes", "home", "ownership.json")
	if err := os.MkdirAll(filepath.Dir(ledger), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := tsruntime.TryUpdateOwnership(ledger, func(l *tsruntime.OwnershipLedger) (bool, error) {
		l.Nodes = append(l.Nodes, tsruntime.OwnedNode{ServiceName: "gateway", NodeID: "gateway-node", RecordedAt: now})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices"):
			fmt.Fprintf(w, `{"devices":[{"nodeId":%q,"hostname":"home"},{"nodeId":"lookalike","hostname":"home"},{"nodeId":"n1","hostname":"photos"},{"nodeId":"n2","hostname":"finance"}]}`, f.nodeID)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/user-invites"):
			io.WriteString(w, "null")
		case strings.Contains(r.URL.Path, "/device/") && strings.HasSuffix(r.URL.Path, "/device-invites"):
			parts := strings.Split(r.URL.Path, "/")
			node := parts[len(parts)-2]
			if node == "lookalike" {
				t.Error("invited lookalike node")
			}
			if r.Method == "POST" {
				p, err := readPerson(paths.Registry, "alice")
				if err != nil {
					t.Error(err)
				}
				sending := false
				for _, op := range p.Invites {
					if op.State == registry.PersonInviteSending && op.NodeID == node {
						sending = true
					}
				}
				if !sending {
					t.Error("POST preceded durable sending")
				}
				if f.beforePost != nil {
					f.beforePost()
				}
				f.posts++
				id := fmt.Sprint(1000 + f.posts)
				f.pending[id] = node
				if f.fail {
					w.WriteHeader(503)
					io.WriteString(w, `{"message":"unavailable"}`)
					return
				}
				if f.partial {
					io.WriteString(w, "[")
					return
				}
				json.NewEncoder(w).Encode([]map[string]any{{"id": id, "inviteUrl": "https://fixture.invalid/invite/" + id}})
			} else if r.Method == "GET" {
				if f.beforeList != nil {
					f.beforeList()
				}
				if f.listBody != "" {
					io.WriteString(w, f.listBody)
					return
				}
				rows := []map[string]any{}
				for id, n := range f.pending {
					if n == node {
						rows = append(rows, map[string]any{"id": id, "accepted": f.accepted[id], "inviteUrl": "https://fixture.invalid/invite/" + id})
					}
				}
				json.NewEncoder(w).Encode(rows)
			} else {
				t.Errorf("unexpected method %s", r.Method)
			}
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/device-invites/"):
			p, err := readPerson(paths.Registry, "alice")
			if err != nil || !p.Revoked {
				t.Error("DELETE before local denial", p, err)
			}
			id := filepath.Base(r.URL.Path)
			delete(f.pending, id)
			f.deletes++
			io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(api.Close)
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-gateway>")
	return f
}
func (f *gatewayInviteFixture) refresh(t *testing.T) {
	t.Helper()
	now := peopleNowFn()
	reg, err := registry.Load(f.paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	snap := tsruntime.NewSnapshot(42, now, currentRegistryFingerprint(f.paths.Registry), now, nil)
	snap.Services = []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", NodeID: "n1", RuntimeState: "running"}, {Name: "finance", Type: "proxy", NodeID: "n2", RuntimeState: "running"}}
	snap.Portal = tsruntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net", Gateway: &tsruntime.GatewayState{Enabled: true, Persistent: true, OwnershipProven: true, NodeID: f.nodeID, Ports: reg.Portal.Gateway.Ports, Routes: []tsruntime.GatewayRouteState{}}}
	if err := tsruntime.Save(f.paths.Snapshot, snap); err != nil {
		t.Fatal(err)
	}
}
func (f *gatewayInviteFixture) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts, f.deletes
}

func TestGatewayOneInviteGrantChangesAndRemoval(t *testing.T) {
	f := newGatewayInviteFixture(t)
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true, PrintLinks: true, QR: true}, false)
	if err != nil || !got.Complete || len(got.Invites) != 1 || got.Invites[0].TargetKind != "gateway" || got.Invites[0].App != "" {
		t.Fatal(got, err)
	}
	if got.QRPayload != "https://home.tailnet.ts.net" || !strings.Contains(got.Message, "gateway invitation once") || strings.Contains(got.Message, "share the home node too") {
		t.Fatal(got)
	}
	validateAgainstToolOutputSchema(t, "people_add", got)
	saved, err := os.ReadFile(f.paths.Registry)
	if err != nil || strings.Contains(string(saved), "fixture.invalid") || !strings.Contains(string(saved), `"target_kind": "gateway"`) {
		t.Fatal(string(saved), err)
	}
	for _, apps := range [][]string{{"photos"}, {"finance"}, {"photos", "finance"}} {
		_, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: apps}, true)
		if err != nil {
			t.Fatal(err)
		}
		if posts, deletes := f.counts(); posts != 1 || deletes != 0 {
			t.Fatalf("grant mutation changed gateway invitation %d/%d", posts, deletes)
		}
	}
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true}, true)
	if err != nil || !got.Complete || len(got.Invites) != 1 {
		t.Fatal(got, err)
	}
	if posts, _ := f.counts(); posts != 1 {
		t.Fatal("repeated live invite", posts)
	}
	removed, err := removePeople(f.paths.Registry, "alice")
	if err != nil || !removed.Complete || len(removed.Cleanup) != 1 || removed.Cleanup[0].TargetKind != "gateway" || removed.Cleanup[0].State != "revoked" {
		t.Fatal(removed, err)
	}
	f.refresh(t)
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !got.Complete {
		t.Fatal(got, err)
	}
	p, _ := readPerson(f.paths.Registry, "alice")
	if len(p.Invites) != 2 || p.Invites[1].Attempt != 1 {
		t.Fatal(p)
	}
}

func TestGatewayInviteUnknownReconcileAndAcceptedDedup(t *testing.T) {
	for _, mode := range []string{"partial", "api-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			f.partial = mode == "partial"
			f.fail = mode == "api-error"
			got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
			if err != nil || got.Complete || len(got.Invites) != 1 || got.Invites[0].State != "unknown" {
				t.Fatal(got, err)
			}
			f.mu.Lock()
			f.partial = false
			f.fail = false
			f.mu.Unlock()
			got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true}, true)
			if err != nil || got.Complete || got.Invites[0].Code != "invite_reconciliation_required" {
				t.Fatal(got, err)
			}
			if posts, _ := f.counts(); posts != 1 {
				t.Fatal("ambiguous POST retried", posts)
			}
			f.mu.Lock()
			f.accepted["1001"] = true
			f.mu.Unlock()
			got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{gatewayInviteKey: "1001"}}, true)
			if err != nil || !got.Complete || got.Invites[0].State != "accepted" {
				t.Fatal(got, err)
			}
			got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true}, true)
			if err != nil || !got.Complete || got.Invites[0].State != "accepted" {
				t.Fatal(got, err)
			}
			removed, err := removePeople(f.paths.Registry, "alice")
			if err != nil || !removed.Complete {
				t.Fatal(removed, err)
			}
			if posts, deletes := f.counts(); posts != 1 || deletes != 0 {
				t.Fatalf("accepted share recreated or deleted %d/%d", posts, deletes)
			}
		})
	}
}

func TestGatewayInviteOwnershipRefusalAndMixedApps(t *testing.T) {
	f := newGatewayInviteFixture(t)
	ledger := filepath.Join(filepath.Dir(f.paths.Registry), "portal-nodes", "home", "ownership.json")
	if err := os.Rename(ledger, ledger+".saved"); err != nil {
		t.Fatal(err)
	}
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || got.Complete || got.Invites[0].Code != registry.CodeInviteOwnershipUnproven {
		t.Fatal(got, err)
	}
	if posts, _ := f.counts(); posts != 0 {
		t.Fatal("missing ledger POST", posts)
	}
	if err := os.Rename(ledger+".saved", ledger); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortalRoute(context.Background(), f.paths.Registry, "finance", nil); err != nil {
		t.Fatal(err)
	}
	f.refresh(t)
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, true)
	if err != nil || !got.Complete || len(got.Invites) != 2 || len(got.Warnings) != 1 {
		t.Fatal(got, err)
	}
	if posts, _ := f.counts(); posts != 2 {
		t.Fatal("mixed invite count", posts)
	}
	validateAgainstToolOutputSchema(t, "people_update", got)
}

func TestGatewayInviteListAndReplacement(t *testing.T) {
	f := newGatewayInviteFixture(t)
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || !got.Complete {
		t.Fatal(got, err)
	}
	f.refresh(t)
	targets, err := inviteDeviceTargetsForListPaths(f.paths.Registry, f.paths.PID, f.paths.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := tailapi.ListInvites(context.Background(), targets)
	if err != nil || !listed.Complete || len(listed.DeviceInvites) != 1 || listed.DeviceInvites[0].TargetKind != "gateway" || listed.DeviceInvites[0].Service != "" {
		t.Fatal(listed, err)
	}
	validateAgainstToolOutputSchema(t, "invite_list", listed)
	f.mu.Lock()
	delete(f.pending, "1001")
	f.mu.Unlock()
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{gatewayInviteKey: "1001"}}, true)
	if err != nil || !got.Complete || got.Invites[0].ID != "1002" {
		t.Fatal(got, err)
	}
	p, _ := readPerson(f.paths.Registry, "alice")
	if len(p.Invites) != 2 || p.Invites[0].State != "replaced" || p.Invites[1].Attempt != 1 {
		t.Fatal(p)
	}
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{gatewayInviteKey: "1001"}}, true)
	if err != nil || !got.Complete {
		t.Fatal(got, err)
	}
	if posts, _ := f.counts(); posts != 2 {
		t.Fatal("replacement retried POST", posts)
	}
}

func TestGatewayInviteConcurrentRevocationAndPartialList(t *testing.T) {
	f := newGatewayInviteFixture(t)
	f.beforePost = func() {
		if _, err := registry.RemovePerson(f.paths.Registry, "alice"); err != nil {
			t.Error(err)
		}
	}
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || got.Complete || !got.Person.Revoked {
		t.Fatal(got, err)
	}
	p, _ := readPerson(f.paths.Registry, "alice")
	if len(p.Invites) != 1 || p.Invites[0].State != "complete" {
		t.Fatal("lost late outcome", p)
	}
	f.mu.Lock()
	f.listBody = "["
	f.mu.Unlock()
	removed, err := removePeople(f.paths.Registry, "alice")
	if err != nil || removed.Complete {
		t.Fatal(removed, err)
	}
	if _, deletes := f.counts(); deletes != 0 {
		t.Fatal("partial listing authorized DELETE", deletes)
	}
	f.mu.Lock()
	f.listBody = ""
	f.mu.Unlock()
	removed, err = removePeople(f.paths.Registry, "alice")
	if err != nil || !removed.Complete {
		t.Fatal(removed, err)
	}
	if _, deletes := f.counts(); deletes != 1 {
		t.Fatal("late invite cleanup missing", deletes)
	}
}
