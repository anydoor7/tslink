package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/lifecycle"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// B6a-1 (audit X4-1, release blocker). The lifecycle reconciler reads the
// registry and the ownership ledger, waits for the device API, and then
// deletes a removed service's node state. If the service is added again in
// that wait, the daemon starts a new node into the same state directory, and
// HoldsNodeState used to see only published nodes: the stale decision deleted
// the new node's state underneath a start that then reported success. The
// removal now happens under the ownership ledger's lock, after reading the
// registry and the ledger again, and a startup reserves the directory under
// the same lock before it first touches it.

// removedOrphanDaemon returns a daemon whose config directory holds a removed
// service "orphan": absent from the registry, one retired ownership row, and
// old node state on disk. The device API lists no devices, so the row
// resolves as already absent; gate, when non-nil, holds its answer until
// closed, and entered is closed when it is first asked.
func removedOrphanDaemon(t *testing.T, gate chan struct{}) (s *Server, regPath, ownershipPath, stateDir string, entered chan struct{}) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	regPath = writeRegistry(t, nil)
	var err error
	ownershipPath, err = config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := tsruntime.RecordOwnedNode(ownershipPath, "orphan", "node-orphan-old", now); err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.MarkOwnedNodeIDsRetired(ownershipPath, []string{"node-orphan-old"}, now); err != nil {
		t.Fatal(err)
	}
	stateDir = filepath.Join(config.NodesDirIn(mustConfigDir(t)), "orphan")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "tailscaled.state"), []byte("old synthetic key"), 0o600); err != nil {
		t.Fatal(err)
	}
	entered = make(chan struct{})
	var once sync.Once
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected device API mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		once.Do(func() { close(entered) })
		if gate != nil {
			<-gate
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": []any{}})
	}))
	t.Cleanup(api.Close)
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	t.Setenv("TSLINK_API_KEY", "synthetic-api-placeholder")
	s, err = New("synthetic-auth-placeholder", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	return s, regPath, ownershipPath, stateDir, entered
}

func reconcileRemovedOrphan(regPath, ownershipPath string, inUse func(string) bool) error {
	_, err := lifecycle.Reconcile(context.Background(), lifecycle.Options{
		RegistryPath:        regPath,
		OwnershipPath:       ownershipPath,
		CleanLocalNodeState: true,
		LocalNodeStateInUse: inUse,
	})
	return err
}

// enrollingNode writes new enrolled state from Up, as a tsnet node does, and
// can hold Up or ListenTLS until the test releases them.
type enrollingNode struct {
	fakeTSNetServer
	marker        string
	nodeID        string
	upEntered     chan struct{}
	upRelease     chan struct{}
	listenEntered chan struct{}
	listenRelease chan struct{}
}

func newEnrollingNode(stateDir, nodeID string) *enrollingNode {
	return &enrollingNode{marker: filepath.Join(stateDir, "new-enrolled-state"), nodeID: nodeID, upEntered: make(chan struct{}), listenEntered: make(chan struct{})}
}

func (n *enrollingNode) Up(context.Context) (*ipnstate.Status, error) {
	if err := os.WriteFile(n.marker, []byte("new synthetic enrolled state"), 0o600); err != nil {
		return nil, err
	}
	close(n.upEntered)
	if n.upRelease != nil {
		<-n.upRelease
	}
	return &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: tailcfg.StableNodeID(n.nodeID), DNSName: "orphan.example.invalid."}}, nil
}

func (n *enrollingNode) ListenTLS(string, string) (net.Listener, error) {
	close(n.listenEntered)
	if n.listenRelease != nil {
		<-n.listenRelease
	}
	return &fakeListener{}, nil
}

func useEnrollingNode(t *testing.T, node *enrollingNode) {
	t.Helper()
	old := newTSNetServerFn
	newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer { return node }
	t.Cleanup(func() { newTSNetServerFn = old })
}

func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func ownershipRecords(t *testing.T, path, nodeID string) bool {
	t.Helper()
	ledger, err := tsruntime.LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range ledger.Nodes {
		if node.NodeID == nodeID {
			return true
		}
	}
	return false
}

// X4's reproduction: the service is added again while the reconciler waits
// for the device API, and the daemon's start of the new node has enrolled,
// recorded its NodeID and is activating its listener when the stale
// reconciliation resumes.
func TestReconcileKeepsTheStateOfANodeStartingForAReaddedService(t *testing.T) {
	gate := make(chan struct{})
	s, regPath, ownershipPath, stateDir, apiEntered := removedOrphanDaemon(t, gate)
	released := false
	release := func() {
		if !released {
			released = true
			close(gate)
		}
	}
	defer release()
	recDone := make(chan error, 1)
	go func() { recDone <- reconcileRemovedOrphan(regPath, ownershipPath, s.HoldsNodeState) }()
	waitFor(t, apiEntered, "the reconciler's device API call")

	svc := registry.Service{Name: "orphan", Type: registry.TypeFile, Path: t.TempDir()}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatal(err)
	}
	node := newEnrollingNode(stateDir, "node-orphan-new")
	node.listenRelease = make(chan struct{})
	useEnrollingNode(t, node)
	started := make(chan error, 1)
	go func() { started <- s.startNodeLocked(context.Background(), svc) }()
	waitFor(t, node.listenEntered, "the new node's listener activation")
	if !ownershipRecords(t, ownershipPath, "node-orphan-new") {
		t.Fatal("control: the new node's NodeID was not recorded before the reconciler resumed")
	}

	release()
	recErr := <-recDone
	_, statErr := os.Stat(node.marker)
	close(node.listenRelease)
	startErr := <-started

	if recErr != nil || startErr != nil {
		t.Fatalf("reconcile error = %v, start error = %v", recErr, startErr)
	}
	if statErr != nil {
		t.Fatalf("the reconciler deleted the new node's enrolled state: %v", statErr)
	}
	if !s.nodeRunning("orphan") {
		t.Fatal("control: the new node is not running")
	}
}

// The reservation on its own: the start has written enrolled state but has not
// returned from Up, so no NodeID is recorded, and the registry does not list
// the service. Only the reservation tells the reconciler the directory is in
// use.
func TestReconcileKeepsTheStateOfANodeStartingBeforeItRecordsAnything(t *testing.T) {
	s, regPath, ownershipPath, stateDir, _ := removedOrphanDaemon(t, nil)
	node := newEnrollingNode(stateDir, "node-orphan-new")
	node.upRelease = make(chan struct{})
	useEnrollingNode(t, node)
	svc := registry.Service{Name: "orphan", Type: registry.TypeFile, Path: t.TempDir()}
	started := make(chan error, 1)
	go func() { started <- s.startNodeLocked(context.Background(), svc) }()
	waitFor(t, node.upEntered, "the new node's Up")

	recErr := reconcileRemovedOrphan(regPath, ownershipPath, s.HoldsNodeState)
	_, statErr := os.Stat(node.marker)
	close(node.upRelease)
	startErr := <-started

	if recErr != nil || startErr != nil {
		t.Fatalf("reconcile error = %v, start error = %v", recErr, startErr)
	}
	if statErr != nil {
		t.Fatalf("the reconciler deleted the state of a node still starting: %v", statErr)
	}
	if !ownershipRecords(t, ownershipPath, "node-orphan-old") {
		t.Fatal("the old row was forgotten although its state directory was kept")
	}
}

// The serialization: once the reconciler has decided, under the ledger lock,
// that nothing holds the directory, a start must not write into it until the
// removal is done. The in-use predicate pauses inside the lock after answering;
// the start must not reach Up meanwhile, and after the removal it enrolls into
// a fresh directory that survives.
func TestNodeStartWaitsForAReconcilerRemovalInProgress(t *testing.T) {
	s, regPath, ownershipPath, stateDir, _ := removedOrphanDaemon(t, nil)
	answered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	inUse := func(name string) bool {
		held := s.HoldsNodeState(name)
		if name == "orphan" {
			once.Do(func() {
				close(answered)
				<-proceed
			})
		}
		return held
	}
	recDone := make(chan error, 1)
	go func() { recDone <- reconcileRemovedOrphan(regPath, ownershipPath, inUse) }()
	waitFor(t, answered, "the reconciler's in-use check")

	node := newEnrollingNode(stateDir, "node-orphan-new")
	useEnrollingNode(t, node)
	svc := registry.Service{Name: "orphan", Type: registry.TypeFile, Path: t.TempDir()}
	started := make(chan error, 1)
	go func() { started <- s.startNodeLocked(context.Background(), svc) }()
	select {
	case <-node.upEntered:
		t.Error("the start wrote enrolled state while the reconciler was between its in-use check and its removal")
	case <-time.After(300 * time.Millisecond):
	}
	close(proceed)
	recErr := <-recDone
	startErr := <-started

	if recErr != nil || startErr != nil {
		t.Fatalf("reconcile error = %v, start error = %v", recErr, startErr)
	}
	if _, err := os.Stat(node.marker); err != nil {
		t.Fatalf("the new node's enrolled state is gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tailscaled.state")); !os.IsNotExist(err) {
		t.Fatalf("control: the removed service's old state is still there (stat err = %v); the reconciler did not remove it", err)
	}
	if !ownershipRecords(t, ownershipPath, "node-orphan-new") || ownershipRecords(t, ownershipPath, "node-orphan-old") {
		t.Fatal("ledger does not hold exactly the new node after the removal and the start")
	}
}
