package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	tailscale "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn/ipnstate"
)

// upRecordingTSNetServer runs onUp when the node comes up, so a test can see
// which API requests were already made by then.
type upRecordingTSNetServer struct {
	fakeTSNetServer
	onUp func()
}

func (s *upRecordingTSNetServer) Up(ctx context.Context) (*ipnstate.Status, error) {
	s.onUp()
	return s.fakeTSNetServer.Up(ctx)
}

type identityResetFixture struct {
	server        *Server
	fake          *testenv.StatefulTailnet
	ownershipPath string
	stateMarker   string
	mu            sync.Mutex
	deletesAtUp   []int // DELETE requests already made when each new node came up
}

func (f *identityResetFixture) deletes() int {
	n := 0
	for _, request := range f.fake.Requests() {
		if request.Method == http.MethodDelete {
			n++
		}
	}
	return n
}

// newIdentityResetFixture enrolls before the way a Tier 2 daemon does: the
// identity record, node state on disk, a device in the tailnet and, when
// recordNodeID is set, that device's StableNodeID in the ownership ledger.
// Then it puts after in the registry.
func newIdentityResetFixture(t *testing.T, before, after registry.Service, recordNodeID bool) *identityResetFixture {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	f := &identityResetFixture{fake: testenv.NewStatefulTailnet(t)}
	t.Setenv(tailapi.APIBaseURLEnv, f.fake.URL())
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	f.fake.SetDevices([]tailscale.Device{
		{ID: "id-app-old", NodeID: "n-app-old", Hostname: before.Name, Tags: []string{"tag:web"}},
		{ID: "id-other", NodeID: "n-other", Hostname: "other", Tags: []string{"tag:web"}},
	})
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
		return &upRecordingTSNetServer{
			fakeTSNetServer: fakeTSNetServer{localClient: localapitest.NewClient(nil)},
			onUp: func() {
				f.mu.Lock()
				f.deletesAtUp = append(f.deletesAtUp, f.deletes())
				f.mu.Unlock()
			},
		}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetEnsureFunnelAttrFn(funnelPolicySatisfied)
	f.server = s
	if _, err := s.prepareNodeIdentity(context.Background(), before); err != nil {
		t.Fatalf("record the enrolled identity: %v", err)
	}
	f.stateMarker = filepath.Join(config.NodesDirIn(mustConfigDir(t)), before.Name, "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(f.stateMarker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.stateMarker, []byte("enrolled before"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.ownershipPath, err = config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	if recordNodeID {
		if err := runtimesnapshot.RecordOwnedNode(f.ownershipPath, before.Name, "n-app-old", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	writeRegistry(t, []registry.Service{after})
	return f
}

func identityResetCases(t *testing.T) map[string][2]registry.Service {
	private := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}}
	retagged := private
	retagged.Tags = []string{"tag:other"}
	expired := time.Now().Add(-time.Minute).UTC()
	public := private
	public.Funnel = true
	public.PublicAck = true
	lapsed := public
	lapsed.FunnelExpiresAt = &expired
	return map[string][2]registry.Service{
		// The automatic case: nobody touches anything and the Funnel deadline
		// passes, which drops the derived tag:tslink-funnel on Tier 2.
		"Funnel expiry": {public, lapsed},
		"tag change":    {private, retagged},
	}
}

// An identity change deletes the node state and re-enrolls. The old device is
// deleted by the exact StableNodeID the ledger holds, before the replacement
// node comes up, so the hostname is free for it; the resolved ID is forgotten.
func TestIdentityResetDeletesTheOldDeviceByExactNodeIDBeforeUp(t *testing.T) {
	for name, tc := range identityResetCases(t) {
		t.Run(name, func(t *testing.T) {
			f := newIdentityResetFixture(t, tc[0], tc[1], true)
			if err := f.server.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v", err)
			}
			if _, err := os.Stat(f.stateMarker); !os.IsNotExist(err) {
				t.Fatalf("control: the identity change did not reset the node state (stat err %v)", err)
			}
			var deleted []string
			for _, request := range f.fake.Requests() {
				if request.Method == http.MethodDelete {
					deleted = append(deleted, request.Path)
				}
			}
			if len(deleted) != 1 || deleted[0] != testenv.TailnetDevicePath("n-app-old") {
				t.Fatalf("DELETE requests = %v, want exactly %s", deleted, testenv.TailnetDevicePath("n-app-old"))
			}
			f.mu.Lock()
			atUp := append([]int(nil), f.deletesAtUp...)
			f.mu.Unlock()
			if len(atUp) != 1 || atUp[0] != 1 {
				t.Fatalf("DELETEs made when the replacement node came up = %v, want the old device already deleted", atUp)
			}
			if !f.server.nodeRunning("app") {
				t.Fatal("replacement node is not running")
			}
			ledger, err := runtimesnapshot.LoadOwnership(f.ownershipPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, node := range ledger.Nodes {
				if node.NodeID == "n-app-old" {
					t.Fatalf("ledger still holds the deleted device: %+v", ledger.Nodes)
				}
			}
			for _, device := range f.fake.Devices() {
				if device.NodeID == "n-app-old" {
					t.Fatal("old device still in the tailnet")
				}
			}
		})
	}
}

// Without a recorded NodeID nothing is deleted: a hostname match alone never
// authorizes a DELETE, as before.
func TestIdentityResetWithoutRecordedNodeIDDeletesNothing(t *testing.T) {
	for name, tc := range identityResetCases(t) {
		t.Run(name, func(t *testing.T) {
			f := newIdentityResetFixture(t, tc[0], tc[1], false)
			if err := f.server.syncNodes(context.Background()); err != nil {
				t.Fatalf("syncNodes() error = %v", err)
			}
			if _, err := os.Stat(f.stateMarker); !os.IsNotExist(err) {
				t.Fatalf("control: the identity change did not reset the node state (stat err %v)", err)
			}
			if n := f.deletes(); n != 0 {
				t.Fatalf("DELETE requests = %d, want none without ownership proof", n)
			}
			if len(f.fake.Devices()) != 2 {
				t.Fatalf("devices = %+v, want both kept", f.fake.Devices())
			}
		})
	}
}
