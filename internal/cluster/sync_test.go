package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// mockTransport implements Transport for testing.
type mockTransport struct {
	messages []Message
	incoming chan Message
	peerList []string
	mu       sync.Mutex
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		incoming: make(chan Message, 100),
		peerList: []string{},
	}
}

func (m *mockTransport) Broadcast(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msg)
	return nil
}

func (m *mockTransport) Receive(_ context.Context) (<-chan Message, error) {
	return m.incoming, nil
}

func (m *mockTransport) Peers() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peerList
}

func (m *mockTransport) getMessages() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.messages))
	copy(out, m.messages)
	return out
}

func regPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "registry.json")
}

func TestNewCluster(t *testing.T) {
	tr := newMockTransport()
	c := NewCluster("node-1", tr, "/tmp/fake-reg.json")

	if c.nodeID != "node-1" {
		t.Fatalf("expected nodeID %q, got %q", "node-1", c.nodeID)
	}
	if c.transport != tr {
		t.Fatal("transport not set")
	}
	if len(c.peers) != 0 {
		t.Fatalf("expected empty peers, got %d", len(c.peers))
	}
}

func TestCluster_SendHeartbeat(t *testing.T) {
	rp := regPath(t)
	// Add a service so the heartbeat includes it.
	if _, err := registry.Add(rp, registry.Service{
		Name: "web",
		Type: registry.TypeProxy,
	}); err != nil {
		t.Fatal(err)
	}

	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	if err := c.sendHeartbeat(context.Background()); err != nil {
		t.Fatalf("sendHeartbeat failed: %v", err)
	}

	msgs := tr.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0]
	if msg.Type != MsgTypeHeartbeat {
		t.Fatalf("expected heartbeat type, got %s", msg.Type)
	}
	if msg.NodeID != "node-1" {
		t.Fatalf("expected node-1, got %s", msg.NodeID)
	}

	var payload HeartbeatPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Services) != 1 || payload.Services[0] != "web" {
		t.Fatalf("expected [web], got %v", payload.Services)
	}
}

func TestCluster_HandleHeartbeat(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	payload, _ := json.Marshal(HeartbeatPayload{
		Hostname: "peer-host",
		Address:  "100.64.0.2",
		Services: []string{"api", "db"},
		Version:  "1.0.0",
	})

	msg := Message{
		Type:      MsgTypeHeartbeat,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleHeartbeat(msg); err != nil {
		t.Fatal(err)
	}

	peers := c.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	p := peers[0]
	if p.ID != "node-2" {
		t.Fatalf("expected node-2, got %s", p.ID)
	}
	if p.Hostname != "peer-host" {
		t.Fatalf("expected peer-host, got %s", p.Hostname)
	}
	if p.Address != "100.64.0.2" {
		t.Fatalf("expected 100.64.0.2, got %s", p.Address)
	}
	if len(p.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(p.Services))
	}
	if p.Version != "1.0.0" {
		t.Fatalf("expected 1.0.0, got %s", p.Version)
	}
}

func TestCluster_HandleServiceAdd(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	svc := registry.Service{
		Name:   "remote-api",
		Type:   registry.TypeProxy,
		Target: "localhost:8080",
	}
	payload, _ := json.Marshal(ServicePayload{Service: svc})

	msg := Message{
		Type:      MsgTypeServiceAdd,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleServiceAdd(msg); err != nil {
		t.Fatal(err)
	}

	// Verify it was added to the registry.
	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Name != "remote-api" {
		t.Fatalf("expected remote-api, got %s", reg.Services[0].Name)
	}
	if reg.Services[0].Target != "localhost:8080" {
		t.Fatalf("expected localhost:8080, got %s", reg.Services[0].Target)
	}
}

func TestCluster_HandleServiceRemove(t *testing.T) {
	rp := regPath(t)
	// Pre-populate the registry.
	if _, err := registry.Add(rp, registry.Service{
		Name: "to-remove",
		Type: registry.TypeProxy,
	}); err != nil {
		t.Fatal(err)
	}

	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	payload, _ := json.Marshal(ServicePayload{
		Service: registry.Service{Name: "to-remove"},
	})

	msg := Message{
		Type:      MsgTypeServiceRemove,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleServiceRemove(msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(reg.Services))
	}
}

func TestCluster_HandleFullSync(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Set up existing peer info so full sync can prune old services.
	c.mu.Lock()
	c.peers["node-2"] = &NodeInfo{
		ID:       "node-2",
		Services: []string{"old-svc"},
		LastSeen: time.Now().UTC(),
	}
	c.mu.Unlock()

	// Add the old service to registry so it can be removed.
	if _, err := registry.Add(rp, registry.Service{
		Name: "old-svc",
		Type: registry.TypeProxy,
	}); err != nil {
		t.Fatal(err)
	}

	services := []registry.Service{
		{Name: "svc-a", Type: registry.TypeProxy, Target: "localhost:3000"},
		{Name: "svc-b", Type: registry.TypeFile, Path: "/data"},
	}
	payload, _ := json.Marshal(FullSyncPayload{Services: services})

	msg := Message{
		Type:      MsgTypeFullSync,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleFullSync(msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(reg.Services))
	}

	names := make(map[string]bool)
	for _, s := range reg.Services {
		names[s.Name] = true
	}
	if !names["svc-a"] || !names["svc-b"] {
		t.Fatalf("expected svc-a and svc-b, got %v", names)
	}
	// old-svc should have been removed.
	if names["old-svc"] {
		t.Fatal("old-svc should have been pruned")
	}

	// Verify peer state updated.
	c.mu.RLock()
	peer := c.peers["node-2"]
	c.mu.RUnlock()
	if len(peer.Services) != 2 {
		t.Fatalf("expected peer to have 2 services, got %d", len(peer.Services))
	}
}

func TestCluster_GetPeers(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Empty initially.
	if peers := c.GetPeers(); len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}

	c.mu.Lock()
	c.peers["node-2"] = &NodeInfo{ID: "node-2", Hostname: "host-2"}
	c.peers["node-3"] = &NodeInfo{ID: "node-3", Hostname: "host-3"}
	c.mu.Unlock()

	peers := c.GetPeers()
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
}

func TestCluster_IsHealthy(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Unknown peer is not healthy.
	if c.IsHealthy("unknown", 30*time.Second) {
		t.Fatal("unknown peer should not be healthy")
	}

	// Recently seen peer is healthy.
	c.mu.Lock()
	c.peers["node-2"] = &NodeInfo{
		ID:       "node-2",
		LastSeen: time.Now().UTC(),
	}
	c.mu.Unlock()

	if !c.IsHealthy("node-2", 30*time.Second) {
		t.Fatal("recently seen peer should be healthy")
	}

	// Stale peer is not healthy.
	c.mu.Lock()
	c.peers["node-3"] = &NodeInfo{
		ID:       "node-3",
		LastSeen: time.Now().UTC().Add(-2 * time.Minute),
	}
	c.mu.Unlock()

	if c.IsHealthy("node-3", 30*time.Second) {
		t.Fatal("stale peer should not be healthy")
	}
}

func TestCluster_PruneStale(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	c.mu.Lock()
	c.peers["fresh"] = &NodeInfo{
		ID:       "fresh",
		LastSeen: time.Now().UTC(),
	}
	c.peers["stale"] = &NodeInfo{
		ID:       "stale",
		LastSeen: time.Now().UTC().Add(-5 * time.Minute),
	}
	c.mu.Unlock()

	c.pruneStale(60 * time.Second)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if _, ok := c.peers["fresh"]; !ok {
		t.Fatal("fresh peer should not be pruned")
	}
	if _, ok := c.peers["stale"]; ok {
		t.Fatal("stale peer should be pruned")
	}
}

func TestCluster_Run(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	ctx, cancel := context.WithCancel(context.Background())

	// Send a heartbeat message from a peer after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		payload, _ := json.Marshal(HeartbeatPayload{
			Hostname: "peer-host",
			Address:  "100.64.0.1",
			Services: []string{"svc-x"},
			Version:  "2.0.0",
		})
		tr.incoming <- Message{
			Type:      MsgTypeHeartbeat,
			NodeID:    "node-2",
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		}
		// Give time for processing.
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Verify the initial heartbeat was sent.
	msgs := tr.getMessages()
	if len(msgs) == 0 {
		t.Fatal("expected at least one heartbeat message")
	}
	if msgs[0].Type != MsgTypeHeartbeat {
		t.Fatalf("expected heartbeat, got %s", msgs[0].Type)
	}

	// Verify the peer heartbeat was processed.
	peers := c.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].ID != "node-2" {
		t.Fatalf("expected node-2, got %s", peers[0].ID)
	}
}

func TestCluster_Run_IgnoresOwnMessages(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		payload, _ := json.Marshal(HeartbeatPayload{
			Hostname: "self",
			Services: []string{},
		})
		// Send a message from ourselves - should be ignored.
		tr.incoming <- Message{
			Type:      MsgTypeHeartbeat,
			NodeID:    "node-1",
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Should have no peers since own message is ignored.
	if len(c.GetPeers()) != 0 {
		t.Fatal("own messages should be ignored")
	}
}

func TestMessage_JSON(t *testing.T) {
	payload, _ := json.Marshal(HeartbeatPayload{
		Hostname: "test-host",
		Address:  "100.64.0.1",
		Services: []string{"web", "api"},
		Version:  "1.0.0",
	})

	msg := Message{
		Type:      MsgTypeHeartbeat,
		NodeID:    "node-1",
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Payload:   payload,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded Message
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.Type != MsgTypeHeartbeat {
		t.Fatalf("expected heartbeat, got %s", decoded.Type)
	}
	if decoded.NodeID != "node-1" {
		t.Fatalf("expected node-1, got %s", decoded.NodeID)
	}
	if !decoded.Timestamp.Equal(msg.Timestamp) {
		t.Fatalf("timestamp mismatch: %v vs %v", decoded.Timestamp, msg.Timestamp)
	}

	var hb HeartbeatPayload
	if err := json.Unmarshal(decoded.Payload, &hb); err != nil {
		t.Fatal(err)
	}
	if hb.Hostname != "test-host" {
		t.Fatalf("expected test-host, got %s", hb.Hostname)
	}
	if len(hb.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(hb.Services))
	}
}

func TestCluster_ProcessMessage_UnknownType(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	msg := Message{
		Type:   "unknown_type",
		NodeID: "node-2",
	}

	err := c.processMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error for unknown message type")
	}
}

func TestCluster_HandleHeartbeat_UpdateExisting(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// First heartbeat.
	payload1, _ := json.Marshal(HeartbeatPayload{
		Hostname: "host-v1",
		Services: []string{"svc-a"},
	})
	msg1 := Message{
		Type:      MsgTypeHeartbeat,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC().Add(-time.Minute),
		Payload:   payload1,
	}
	if err := c.handleHeartbeat(msg1); err != nil {
		t.Fatal(err)
	}

	// Second heartbeat from same node with updated info.
	payload2, _ := json.Marshal(HeartbeatPayload{
		Hostname: "host-v2",
		Services: []string{"svc-a", "svc-b"},
	})
	msg2 := Message{
		Type:      MsgTypeHeartbeat,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload2,
	}
	if err := c.handleHeartbeat(msg2); err != nil {
		t.Fatal(err)
	}

	peers := c.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if peers[0].Hostname != "host-v2" {
		t.Fatalf("expected host-v2, got %s", peers[0].Hostname)
	}
	if len(peers[0].Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(peers[0].Services))
	}
}

func TestCluster_FullSync_NoPriorPeer(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Full sync from an unknown peer (no prior state).
	services := []registry.Service{
		{Name: "new-svc", Type: registry.TypeProxy, Target: "localhost:9000"},
	}
	payload, _ := json.Marshal(FullSyncPayload{Services: services})

	msg := Message{
		Type:      MsgTypeFullSync,
		NodeID:    "node-3",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleFullSync(msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Name != "new-svc" {
		t.Fatalf("expected new-svc, got %s", reg.Services[0].Name)
	}

	// Peer should now be tracked.
	c.mu.RLock()
	peer, ok := c.peers["node-3"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("peer node-3 should be tracked")
	}
	if len(peer.Services) != 1 || peer.Services[0] != "new-svc" {
		t.Fatalf("expected [new-svc], got %v", peer.Services)
	}
}

func TestCluster_Run_ChannelClose(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Close the incoming channel to trigger the !ok path.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(tr.incoming)
	}()

	err := c.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil error on channel close, got %v", err)
	}
}

// failTransport returns an error from Receive.
type failTransport struct{}

func (f *failTransport) Broadcast(_ context.Context, _ Message) error { return nil }
func (f *failTransport) Receive(_ context.Context) (<-chan Message, error) {
	return nil, fmt.Errorf("transport failed")
}
func (f *failTransport) Peers() []string { return nil }

func TestCluster_Run_ReceiveError(t *testing.T) {
	c := NewCluster("node-1", &failTransport{}, "/tmp/fake.json")
	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from Receive")
	}
}

func TestCluster_HandleHeartbeat_InvalidPayload(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	msg := Message{
		Type:    MsgTypeHeartbeat,
		NodeID:  "node-2",
		Payload: json.RawMessage(`{invalid`),
	}
	if err := c.handleHeartbeat(msg); err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestCluster_HandleServiceAdd_InvalidPayload(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	msg := Message{
		Type:    MsgTypeServiceAdd,
		NodeID:  "node-2",
		Payload: json.RawMessage(`{bad`),
	}
	if err := c.handleServiceAdd(msg); err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestCluster_HandleServiceRemove_InvalidPayload(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	msg := Message{
		Type:    MsgTypeServiceRemove,
		NodeID:  "node-2",
		Payload: json.RawMessage(`{bad`),
	}
	if err := c.handleServiceRemove(msg); err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestCluster_HandleFullSync_InvalidPayload(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	msg := Message{
		Type:    MsgTypeFullSync,
		NodeID:  "node-2",
		Payload: json.RawMessage(`{bad`),
	}
	if err := c.handleFullSync(msg); err == nil {
		t.Fatal("expected error for invalid payload")
	}
}

func TestCluster_ProcessMessage_ServiceAdd(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	payload, _ := json.Marshal(ServicePayload{
		Service: registry.Service{Name: "via-process", Type: registry.TypeProxy},
	})
	msg := Message{
		Type:      MsgTypeServiceAdd,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "via-process" {
		t.Fatal("service should have been added via processMessage")
	}
}

func TestCluster_ProcessMessage_ServiceRemove(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Add a service first.
	_, _ = registry.Add(rp, registry.Service{Name: "to-del", Type: registry.TypeProxy})

	payload, _ := json.Marshal(ServicePayload{
		Service: registry.Service{Name: "to-del"},
	})
	msg := Message{
		Type:      MsgTypeServiceRemove,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatal("service should have been removed via processMessage")
	}
}

func TestCluster_ProcessMessage_FullSync(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	payload, _ := json.Marshal(FullSyncPayload{
		Services: []registry.Service{
			{Name: "synced", Type: registry.TypeProxy},
		},
	})
	msg := Message{
		Type:      MsgTypeFullSync,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "synced" {
		t.Fatal("service should have been synced via processMessage")
	}
}

func TestCluster_SendHeartbeat_EmptyRegistry(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	if err := c.sendHeartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}

	msgs := tr.getMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	var payload HeartbeatPayload
	if err := json.Unmarshal(msgs[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(payload.Services))
	}
}

func TestCluster_Run_ProcessesServiceMessages(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)

		// Send service_add via the Run loop.
		payload, _ := json.Marshal(ServicePayload{
			Service: registry.Service{Name: "run-svc", Type: registry.TypeProxy},
		})
		tr.incoming <- Message{
			Type:      MsgTypeServiceAdd,
			NodeID:    "node-2",
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		}

		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != "run-svc" {
		t.Fatal("service should have been added through Run loop")
	}
}

func TestCluster_SendHeartbeat_BadRegistryPath(t *testing.T) {
	// Use a path that exists as a directory, so ReadFile fails with a non-NotExist error.
	dir := t.TempDir()
	badPath := filepath.Join(dir, "subdir")
	// Create subdir as a directory, then use it as if it were a file.
	if err := mkdir(badPath); err != nil {
		t.Fatal(err)
	}

	tr := newMockTransport()
	c := NewCluster("node-1", tr, badPath)

	err := c.sendHeartbeat(context.Background())
	if err == nil {
		t.Fatal("expected error for bad registry path")
	}
}

func TestCluster_HandleFullSync_BadRegistryPath(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "subdir")
	if err := mkdir(badPath); err != nil {
		t.Fatal(err)
	}

	tr := newMockTransport()
	c := NewCluster("node-1", tr, badPath)

	payload, _ := json.Marshal(FullSyncPayload{
		Services: []registry.Service{{Name: "svc", Type: registry.TypeProxy}},
	})
	msg := Message{
		Type:    MsgTypeFullSync,
		NodeID:  "node-2",
		Payload: payload,
	}

	err := c.handleFullSync(msg)
	if err == nil {
		t.Fatal("expected error for bad registry path")
	}
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o700)
}

// failBroadcastTransport fails Broadcast but succeeds Receive.
type failBroadcastTransport struct {
	incoming chan Message
}

func (f *failBroadcastTransport) Broadcast(_ context.Context, _ Message) error {
	return fmt.Errorf("broadcast failed")
}
func (f *failBroadcastTransport) Receive(_ context.Context) (<-chan Message, error) {
	return f.incoming, nil
}
func (f *failBroadcastTransport) Peers() []string { return nil }

func TestCluster_Run_MessageProcessError(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		// Send a message with invalid payload to trigger processMessage error.
		tr.incoming <- Message{
			Type:    MsgTypeHeartbeat,
			NodeID:  "node-2",
			Payload: json.RawMessage(`{invalid`),
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	// Run should not fail on message processing errors.
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCluster_Run_InitialHeartbeatError(t *testing.T) {
	rp := regPath(t)
	tr := &failBroadcastTransport{incoming: make(chan Message, 10)}
	c := NewCluster("node-1", tr, rp)

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error from initial heartbeat failure")
	}
	if !contains(err.Error(), "initial heartbeat failed") {
		t.Fatalf("expected 'initial heartbeat failed' in error, got: %v", err)
	}
}

func TestCluster_Run_HeartbeatTicker(t *testing.T) {
	// Shorten intervals so tickers fire quickly.
	oldHB := heartbeatInterval
	heartbeatInterval = 20 * time.Millisecond
	defer func() { heartbeatInterval = oldHB }()

	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// Wait enough for the ticker to fire at least once.
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have initial heartbeat + at least one ticker heartbeat.
	msgs := tr.getMessages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 heartbeat messages (initial + ticker), got %d", len(msgs))
	}
}

func TestCluster_Run_PruneTicker(t *testing.T) {
	// Shorten prune interval so it fires quickly.
	oldPrune := pruneInterval
	pruneInterval = 20 * time.Millisecond
	defer func() { pruneInterval = oldPrune }()

	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Add a stale peer that should be pruned.
	c.mu.Lock()
	c.peers["stale-peer"] = &NodeInfo{
		ID:       "stale-peer",
		LastSeen: time.Now().UTC().Add(-5 * time.Minute),
	}
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// Wait enough for the prune ticker to fire.
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	err := c.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stale peer should have been pruned.
	c.mu.RLock()
	_, ok := c.peers["stale-peer"]
	c.mu.RUnlock()
	if ok {
		t.Fatal("stale-peer should have been pruned by prune ticker")
	}
}

func TestCluster_HandleFullSync_ServiceRemoval(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	// Set up a peer that previously had services A and B.
	c.mu.Lock()
	c.peers["node-2"] = &NodeInfo{
		ID:       "node-2",
		Services: []string{"svc-a", "svc-b"},
		LastSeen: time.Now().UTC(),
	}
	c.mu.Unlock()

	// Add both services to registry (simulating prior sync).
	_, _ = registry.Add(rp, registry.Service{Name: "svc-a", Type: registry.TypeProxy, Target: "localhost:3000"})
	_, _ = registry.Add(rp, registry.Service{Name: "svc-b", Type: registry.TypeProxy, Target: "localhost:3001"})

	// Now peer only has svc-a (svc-b was removed).
	payload, _ := json.Marshal(FullSyncPayload{
		Services: []registry.Service{
			{Name: "svc-a", Type: registry.TypeProxy, Target: "localhost:3000"},
		},
	})
	msg := Message{
		Type:      MsgTypeFullSync,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	if err := c.handleFullSync(msg); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(rp)
	if err != nil {
		t.Fatal(err)
	}

	// svc-b should have been removed, only svc-a remains.
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Name != "svc-a" {
		t.Fatalf("expected svc-a, got %s", reg.Services[0].Name)
	}

	// Peer should now only list svc-a.
	c.mu.RLock()
	peer := c.peers["node-2"]
	c.mu.RUnlock()
	if len(peer.Services) != 1 || peer.Services[0] != "svc-a" {
		t.Fatalf("expected peer to have [svc-a], got %v", peer.Services)
	}
}

func TestCluster_HandleFullSync_AddError(t *testing.T) {
	// Use a directory as registry path so registry.Add fails.
	dir := t.TempDir()
	badPath := filepath.Join(dir, "baddir")
	if err := mkdir(badPath); err != nil {
		t.Fatal(err)
	}
	// Create a valid registry file first so Load succeeds, but then make Add fail.
	// Actually, we need Load to succeed but Add to fail.
	// Use a path where the registry file exists but is a directory for the Add call.
	// A simpler approach: use a read-only file that Load can read but Add can't write.
	goodPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(goodPath, registry.Service{Name: "existing", Type: registry.TypeProxy})
	// Make the file read-only so Add fails.
	_ = os.Chmod(goodPath, 0o444)
	// Also make the directory read-only to prevent temp file creation.
	_ = os.Chmod(dir, 0o555)
	defer func() {
		_ = os.Chmod(dir, 0o755)
	}()

	tr := newMockTransport()
	c := NewCluster("node-1", tr, goodPath)

	payload, _ := json.Marshal(FullSyncPayload{
		Services: []registry.Service{
			{Name: "new-svc", Type: registry.TypeProxy, Target: "localhost:9000"},
		},
	})
	msg := Message{
		Type:      MsgTypeFullSync,
		NodeID:    "node-2",
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}

	err := c.handleFullSync(msg)
	if err == nil {
		t.Fatal("expected error when registry.Add fails during full sync")
	}
}

func TestCluster_SendHeartbeat_MarshalError(t *testing.T) {
	rp := regPath(t)
	tr := newMockTransport()
	c := NewCluster("node-1", tr, rp)

	old := jsonMarshal
	jsonMarshal = func(_ any) ([]byte, error) {
		return nil, fmt.Errorf("marshal boom")
	}
	defer func() { jsonMarshal = old }()

	err := c.sendHeartbeat(context.Background())
	if err == nil {
		t.Fatal("expected error from marshal failure")
	}
	if !contains(err.Error(), "failed to marshal heartbeat payload") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsHelper(s, substr)
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
