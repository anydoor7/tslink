package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// NodeInfo represents a TSLink instance in the cluster.
type NodeInfo struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname"`
	Address  string    `json:"address"`  // Tailscale IP
	Services []string  `json:"services"` // service names managed by this node
	LastSeen time.Time `json:"last_seen"`
	Version  string    `json:"version"`
}

// Transport abstracts the communication layer between cluster nodes.
// In production, this would use Tailscale's tsnet for node-to-node communication.
type Transport interface {
	// Broadcast sends a message to all known cluster peers.
	Broadcast(ctx context.Context, msg Message) error
	// Receive returns a channel of incoming messages from peers.
	Receive(ctx context.Context) (<-chan Message, error)
	// Peers returns currently known peer addresses.
	Peers() []string
}

// MessageType identifies the type of cluster message.
type MessageType string

const (
	MsgTypeHeartbeat     MessageType = "heartbeat"
	MsgTypeServiceAdd    MessageType = "service_add"
	MsgTypeServiceRemove MessageType = "service_remove"
	MsgTypeFullSync      MessageType = "full_sync"
)

// Message is a cluster communication envelope.
type Message struct {
	Type      MessageType     `json:"type"`
	NodeID    string          `json:"node_id"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// HeartbeatPayload is the payload for heartbeat messages.
type HeartbeatPayload struct {
	Hostname string   `json:"hostname"`
	Address  string   `json:"address"`
	Services []string `json:"services"`
	Version  string   `json:"version"`
}

// ServicePayload is the payload for service add/remove messages.
type ServicePayload struct {
	Service registry.Service `json:"service"`
}

// FullSyncPayload is the payload for full sync messages.
type FullSyncPayload struct {
	Services []registry.Service `json:"services"`
}

var (
	heartbeatInterval = 10 * time.Second
	pruneInterval     = 30 * time.Second
	jsonMarshal       = json.Marshal
)

// Cluster manages the multi-node service sharing.
type Cluster struct {
	nodeID    string
	transport Transport
	peers     map[string]*NodeInfo
	mu        sync.RWMutex
	regPath   string
}

// NewCluster creates a new cluster manager.
func NewCluster(nodeID string, transport Transport, regPath string) *Cluster {
	return &Cluster{
		nodeID:    nodeID,
		transport: transport,
		peers:     make(map[string]*NodeInfo),
		regPath:   regPath,
	}
}

// Run starts the cluster sync loop (heartbeats + message processing).
func (c *Cluster) Run(ctx context.Context) error {
	incoming, err := c.transport.Receive(ctx)
	if err != nil {
		return fmt.Errorf("cluster: failed to start receiving: %w", err)
	}

	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()

	pruneTicker := time.NewTicker(pruneInterval)
	defer pruneTicker.Stop()

	// Send initial heartbeat.
	if err := c.sendHeartbeat(ctx); err != nil {
		return fmt.Errorf("cluster: initial heartbeat failed: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-incoming:
			if !ok {
				return nil
			}
			if err := c.processMessage(ctx, msg); err != nil {
				// Log but don't fail the loop.
				_ = err
			}
		case <-heartbeatTicker.C:
			_ = c.sendHeartbeat(ctx)
		case <-pruneTicker.C:
			c.pruneStale(60 * time.Second)
		}
	}
}

// processMessage handles an incoming cluster message.
func (c *Cluster) processMessage(_ context.Context, msg Message) error {
	// Ignore messages from ourselves.
	if msg.NodeID == c.nodeID {
		return nil
	}

	switch msg.Type {
	case MsgTypeHeartbeat:
		return c.handleHeartbeat(msg)
	case MsgTypeServiceAdd:
		return c.handleServiceAdd(msg)
	case MsgTypeServiceRemove:
		return c.handleServiceRemove(msg)
	case MsgTypeFullSync:
		return c.handleFullSync(msg)
	default:
		return fmt.Errorf("cluster: unknown message type: %s", msg.Type)
	}
}

// handleHeartbeat updates the peer's last-seen timestamp and service list.
func (c *Cluster) handleHeartbeat(msg Message) error {
	var payload HeartbeatPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("cluster: failed to unmarshal heartbeat: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.peers[msg.NodeID] = &NodeInfo{
		ID:       msg.NodeID,
		Hostname: payload.Hostname,
		Address:  payload.Address,
		Services: payload.Services,
		LastSeen: msg.Timestamp,
		Version:  payload.Version,
	}

	return nil
}

// handleServiceAdd adds a remote service to the local registry.
func (c *Cluster) handleServiceAdd(msg Message) error {
	var payload ServicePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("cluster: failed to unmarshal service_add: %w", err)
	}

	_, addErr := registry.Add(c.regPath, payload.Service)
	return addErr
}

// handleServiceRemove removes a remote service from the local registry.
func (c *Cluster) handleServiceRemove(msg Message) error {
	var payload ServicePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("cluster: failed to unmarshal service_remove: %w", err)
	}

	return registry.Remove(c.regPath, payload.Service.Name)
}

// handleFullSync replaces the peer's services with the provided list.
func (c *Cluster) handleFullSync(msg Message) error {
	var payload FullSyncPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("cluster: failed to unmarshal full_sync: %w", err)
	}

	// Load current registry to find services owned by the sender (tracked in peers).
	reg, err := registry.Load(c.regPath)
	if err != nil {
		return fmt.Errorf("cluster: failed to load registry: %w", err)
	}

	// Build a set of service names from the sender's previous state.
	c.mu.RLock()
	peer, hasPeer := c.peers[msg.NodeID]
	var oldServices map[string]bool
	if hasPeer {
		oldServices = make(map[string]bool, len(peer.Services))
		for _, name := range peer.Services {
			oldServices[name] = true
		}
	}
	c.mu.RUnlock()

	// Remove old services from this peer that are no longer in the sync.
	newNames := make(map[string]bool, len(payload.Services))
	for _, svc := range payload.Services {
		newNames[svc.Name] = true
	}

	if hasPeer {
		for _, existing := range reg.Services {
			if oldServices[existing.Name] && !newNames[existing.Name] {
				_ = registry.Remove(c.regPath, existing.Name)
			}
		}
	}

	// Add/update all services from the sync.
	for _, svc := range payload.Services {
		if _, err := registry.Add(c.regPath, svc); err != nil {
			return fmt.Errorf("cluster: failed to add service %q during full_sync: %w", svc.Name, err)
		}
	}

	// Update the peer's service list.
	serviceNames := make([]string, len(payload.Services))
	for i, svc := range payload.Services {
		serviceNames[i] = svc.Name
	}
	c.mu.Lock()
	if _, ok := c.peers[msg.NodeID]; ok {
		c.peers[msg.NodeID].Services = serviceNames
		c.peers[msg.NodeID].LastSeen = msg.Timestamp
	} else {
		c.peers[msg.NodeID] = &NodeInfo{
			ID:       msg.NodeID,
			Services: serviceNames,
			LastSeen: msg.Timestamp,
		}
	}
	c.mu.Unlock()

	return nil
}

// GetPeers returns info about all known cluster peers.
func (c *Cluster) GetPeers() []NodeInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := make([]NodeInfo, 0, len(c.peers))
	for _, p := range c.peers {
		peers = append(peers, *p)
	}
	return peers
}

// IsHealthy checks if a peer has been seen within the timeout.
func (c *Cluster) IsHealthy(nodeID string, timeout time.Duration) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peer, ok := c.peers[nodeID]
	if !ok {
		return false
	}
	return time.Since(peer.LastSeen) <= timeout
}

// sendHeartbeat broadcasts this node's status.
func (c *Cluster) sendHeartbeat(ctx context.Context) error {
	// Load local services to include in heartbeat.
	reg, err := registry.Load(c.regPath)
	if err != nil {
		return fmt.Errorf("cluster: failed to load registry for heartbeat: %w", err)
	}

	serviceNames := make([]string, len(reg.Services))
	for i, svc := range reg.Services {
		serviceNames[i] = svc.Name
	}

	payload := HeartbeatPayload{
		Services: serviceNames,
	}

	payloadBytes, err := jsonMarshal(payload)
	if err != nil {
		return fmt.Errorf("cluster: failed to marshal heartbeat payload: %w", err)
	}

	msg := Message{
		Type:      MsgTypeHeartbeat,
		NodeID:    c.nodeID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}

	return c.transport.Broadcast(ctx, msg)
}

// pruneStale removes peers that haven't been seen within the timeout.
func (c *Cluster) pruneStale(timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for id, peer := range c.peers {
		if time.Since(peer.LastSeen) > timeout {
			delete(c.peers, id)
		}
	}
}
