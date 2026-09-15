package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// MCPEventsPath is the control plane's server-sent event stream.
//
// It is a separate path from MCPControlPlanePath on purpose. The Streamable
// HTTP transport mounted there runs stateless, which makes the SDK answer GET
// with 405; sharing one path would mean deciding between the two by method,
// and the MCP transport — not this file — owns what GET means on /mcp.
const MCPEventsPath = "/events"

// Event stream frame types. Every frame says what it is in two places: the SSE
// `event:` field and the payload's own "type". A client is never asked to
// infer whether it is holding a full state or a delta.
const (
	MCPEventSnapshot  = "snapshot"
	MCPEventUpdate    = "update"
	MCPEventKeepalive = "keepalive"
)

const (
	// DefaultMCPEventsKeepalive is the heartbeat period when configuration
	// does not set one.
	//
	// The heartbeat is not optional. A tailnet long connection can go silent
	// without the socket reporting an error, and in that state "no events" and
	// "the connection is dead" produce byte-identical observations at the
	// client: nothing arrives either way. The heartbeat is what makes those two
	// distinguishable, so a client can reconnect on silence instead of showing
	// stale state forever.
	DefaultMCPEventsKeepalive = 20 * time.Second
	// MinMCPEventsKeepalive and MaxMCPEventsKeepalive bound the configured
	// value. Below the minimum the heartbeat becomes traffic rather than
	// liveness; above the maximum a dead connection outlives any UI that cares.
	MinMCPEventsKeepalive = 5 * time.Second
	MaxMCPEventsKeepalive = 5 * time.Minute

	// mcpEventsMaxStreams bounds concurrent event streams.
	//
	// The control-plane listener is already wrapped in newLimitedListener with
	// httpMaxActiveConns, the same bound every service listener uses. That bound
	// is sized for request/response traffic, where a connection is returned in
	// milliseconds. An event stream holds its connection for as long as the
	// client stays open, so without a second, smaller bound a handful of
	// reconnect-looping clients could park the whole listener budget and lock
	// every MCP tool call out of the daemon. This bound is therefore about
	// what a connection costs over time, not about how many exist at an
	// instant, which is why it is separate from and far below the listener's.
	mcpEventsMaxStreams = 32

	// mcpEventsWriteTimeout bounds one frame write. A phone that stops reading
	// must not be able to wedge its own handler goroutine forever; the
	// publisher is already insulated from it (see eventHub.publish), so this
	// bound only decides how long a stuck stream keeps its own slot.
	mcpEventsWriteTimeout = 10 * time.Second
)

// MCPEventPayload is the envelope every frame carries. State is the
// transport-independent body built by MCPControlPlane.EventsSnapshot; this
// package never inspects it.
type MCPEventPayload struct {
	Type       string `json:"type"`
	Sequence   uint64 `json:"sequence"`
	EmittedAt  string `json:"emitted_at"`
	Keepalive  string `json:"keepalive_interval,omitempty"`
	State      any    `json:"state,omitempty"`
	StateError string `json:"state_error,omitempty"`
}

// eventHub fans one "runtime state changed" notification out to every open
// stream.
//
// Each subscriber gets a capacity-1 channel and publish never blocks on it.
// That combination is what keeps one stalled client from reaching the
// publisher: a send that would block is dropped, and dropping is correct here
// because the pending notification already in the buffer means the same thing
// the dropped one would have — "rebuild and send current state". Notifications
// coalesce instead of queueing, so a subscriber that wakes up late sends one
// current frame rather than replaying a backlog of stale ones.
type eventHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan struct{}]struct{})}
}

// subscribe registers a listener and returns it with its own release func.
// release is idempotent, so a handler can defer it unconditionally.
func (h *eventHub) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// publish wakes every subscriber. It is safe to call while holding Server.mu:
// it takes only its own lock and performs no blocking send.
func (h *eventHub) publish() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (h *eventHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// mcpEventsKeepalive resolves the configured heartbeat period.
func mcpEventsKeepalive(configured time.Duration) time.Duration {
	if configured <= 0 {
		return DefaultMCPEventsKeepalive
	}
	if configured < MinMCPEventsKeepalive {
		return MinMCPEventsKeepalive
	}
	if configured > MaxMCPEventsKeepalive {
		return MaxMCPEventsKeepalive
	}
	return configured
}

// newMCPEventsHandler serves the control plane's event stream.
//
// It is mounted behind the same Origin and authorization middleware the MCP
// transport is mounted behind; nothing about identity is decided here.
func newMCPEventsHandler(cp *MCPControlPlane, hub *eventHub) http.Handler {
	keepalive := mcpEventsKeepalive(cp.EventsKeepalive)
	slots := make(chan struct{}, mcpEventsMaxStreams)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			mcpEventsError(w, http.StatusMethodNotAllowed, "event stream accepts GET only")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			slog.Warn("mcp event stream refused: concurrent stream limit reached",
				"limit", mcpEventsMaxStreams, "remote_addr", r.RemoteAddr)
			w.Header().Set("Retry-After", "5")
			mcpEventsError(w, http.StatusServiceUnavailable, "too many concurrent event streams")
			return
		}
		serveMCPEventStream(w, r, cp.EventsSnapshot, hub, keepalive)
	})
}

// mcpEventsError writes a refusal in the same JSON-RPC error shape the rest of
// the control plane uses, so a client parses one error format on this node.
func mcpEventsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32600, "message": message},
	})
}

// serveMCPEventStream runs one stream until the client disconnects, a write
// fails, or the daemon shuts the request context down.
//
// Ordering is load-bearing: the hub subscription is taken *before* the first
// snapshot is built. Subscribing afterwards would leave a window in which a
// change that landed between building and subscribing is never delivered and
// never superseded, and the client would sit on state it believes is current.
func serveMCPEventStream(
	w http.ResponseWriter,
	r *http.Request,
	snapshot func(context.Context) (any, error),
	hub *eventHub,
	keepalive time.Duration,
) {
	changed, release := hub.subscribe()
	defer release()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Defensive against an intermediary that buffers responses. tsnet serves
	// this directly, so it is a statement of intent rather than a fix.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	stream := &mcpEventStream{w: w, controller: controller}

	ctx := r.Context()
	var sequence uint64

	sequence++
	if err := stream.write(mcpEventFrame(ctx, MCPEventSnapshot, sequence, keepalive, snapshot)); err != nil {
		slog.Warn("mcp event stream ended writing the initial snapshot", "remote_addr", r.RemoteAddr, "error", err)
		return
	}

	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			sequence++
			if err := stream.write(mcpEventFrame(ctx, MCPEventUpdate, sequence, keepalive, snapshot)); err != nil {
				slog.Warn("mcp event stream ended writing an update", "remote_addr", r.RemoteAddr, "error", err)
				return
			}
		case <-ticker.C:
			sequence++
			if err := stream.write(mcpEventFrame(ctx, MCPEventKeepalive, sequence, keepalive, nil)); err != nil {
				slog.Warn("mcp event stream ended writing a keepalive", "remote_addr", r.RemoteAddr, "error", err)
				return
			}
		}
	}
}

// mcpEventFrame renders one frame. A snapshot builder that fails produces a
// frame carrying state_error rather than silence, so a client can tell "the
// daemon could not read its own state" from "nothing has changed".
func mcpEventFrame(
	ctx context.Context,
	kind string,
	sequence uint64,
	keepalive time.Duration,
	snapshot func(context.Context) (any, error),
) []byte {
	payload := MCPEventPayload{
		Type:      kind,
		Sequence:  sequence,
		EmittedAt: serverNowFn().UTC().Format(time.RFC3339Nano),
		Keepalive: keepalive.String(),
	}
	if snapshot != nil {
		state, err := snapshot(ctx)
		if err != nil {
			payload.StateError = err.Error()
		} else {
			payload.State = state
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshal can only fail on the injected state, so fall back to an
		// envelope that still names the frame and reports the failure.
		body, _ = json.Marshal(MCPEventPayload{
			Type:       kind,
			Sequence:   sequence,
			EmittedAt:  payload.EmittedAt,
			Keepalive:  payload.Keepalive,
			StateError: "state is not serializable",
		})
	}
	return []byte(fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", kind, sequence, body))
}

// mcpEventStream writes frames under a deadline.
type mcpEventStream struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	// deadlineUnsupported records that this ResponseWriter chain cannot carry
	// a write deadline, so the warning is logged once per stream rather than
	// once per frame.
	deadlineUnsupported bool
}

func (s *mcpEventStream) write(frame []byte) error {
	deadline := serverNowFn().Add(mcpEventsWriteTimeout)
	if err := s.controller.SetWriteDeadline(deadline); err != nil {
		if !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if !s.deadlineUnsupported {
			s.deadlineUnsupported = true
			slog.Warn("mcp event stream has no write deadline support; a stalled client holds its stream slot until it disconnects")
		}
	}
	if _, err := s.w.Write(frame); err != nil {
		return err
	}
	return s.controller.Flush()
}
