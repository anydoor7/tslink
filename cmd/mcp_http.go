package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newMCPStreamableHandler builds the remote transport over the same
// *mcp.Server the stdio transport serves.
//
// There is one tool registry in this program: mcpToolDefinitions, registered
// by newMCPServer. Both transports call that constructor, so a tool cannot
// exist on one transport and not the other, and a schema cannot drift between
// them — the transports differ only in how bytes arrive.
//
// The handler is stateless. Every tool here reads or writes registry.json and
// keeps nothing between calls, so there is no server-side state for a session
// to hold; running stateless removes Mcp-Session-Id handling and idle-session
// bookkeeping entirely, and matches the sessionless direction of the current
// spec revision. In this mode the SDK answers GET and DELETE with 405.
func newMCPStreamableHandler(actions mcpActions) http.Handler {
	srv := newMCPServer(actions)
	return mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless: true,
			// Match the stdio transport's per-record bound so one oversized
			// request is refused at the same size on both transports.
			MaxRequestBodyBytes: mcpMaxRecordBytes,
		},
	)
}

// mcpControlPlaneSettings is the resolved enable decision for one serve
// process: the flag wins over the config key, exactly as --control-url does.
type mcpControlPlaneSettings struct {
	Enabled         bool
	Allow           []string
	NodeName        string
	EventsKeepalive string
}

// resolveMCPControlPlaneSettings folds the --mcp flag into the persisted
// mcp config key. Absent both, the control plane is off.
func resolveMCPControlPlaneSettings(flagEnabled bool, cfg config.GlobalConfig) mcpControlPlaneSettings {
	settings := mcpControlPlaneSettings{Enabled: flagEnabled}
	if cfg.MCP != nil {
		if cfg.MCP.Enabled {
			settings.Enabled = true
		}
		settings.Allow = append([]string(nil), cfg.MCP.Allow...)
		settings.NodeName = cfg.MCP.NodeName
		settings.EventsKeepalive = cfg.MCP.EventsKeepalive
	}
	return settings
}

// validateMCPNodeName applies the service-name grammar to the persisted mcp
// node_name. Service names are validated by `tslink add`, but node_name is
// hand-edited config that flows into the same auth-key description and tsnet
// hostname, so it is the one name that would otherwise reach the Tailscale API
// unchecked. Empty means the default node name and is always valid.
func validateMCPNodeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if err := registry.ValidateName(name); err != nil {
		return output.ErrUsage(fmt.Sprintf("invalid mcp node_name: %v", err))
	}
	return nil
}

// parseMCPEventsKeepalive validates the persisted events_keepalive value.
//
// It refuses rather than falls back. A heartbeat is what lets a client tell a
// silent-but-alive stream from a dead one, so silently substituting a default
// for a value the operator typed wrong would leave the deployment running a
// period nobody chose, with nothing to observe. An empty value is the explicit
// "use the default" and is always valid.
func parseMCPEventsKeepalive(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}
	keepalive, err := parseDuration(trimmed)
	if err != nil {
		return 0, output.ErrUsage(fmt.Sprintf("invalid mcp events_keepalive %q: %v", raw, err))
	}
	if keepalive < server.MinMCPEventsKeepalive || keepalive > server.MaxMCPEventsKeepalive {
		return 0, output.ErrUsage(fmt.Sprintf(
			"invalid mcp events_keepalive %q: must be between %s and %s",
			raw, server.MinMCPEventsKeepalive, server.MaxMCPEventsKeepalive))
	}
	return keepalive, nil
}

// buildMCPControlPlane returns the daemon-side control plane for these
// settings, or nil when it is disabled. A nil result is what keeps the default
// path free of any listener or tsnet node.
//
// keepalive is the already-validated heartbeat period; serve parses it during
// flag resolution so a bad value refuses the daemon at startup rather than
// after the control-plane node has enrolled.
func buildMCPControlPlane(settings mcpControlPlaneSettings, actions mcpActions, tags []string, keepalive time.Duration) *server.MCPControlPlane {
	if !settings.Enabled {
		return nil
	}
	return &server.MCPControlPlane{
		NodeName:     settings.NodeName,
		Tags:         append([]string(nil), tags...),
		AllowedUsers: append([]string(nil), settings.Allow...),
		Handler:      newMCPStreamableHandler(actions),
		// The event stream is mounted only because this is non-nil. It reuses
		// the control plane's own Origin and authorization middleware; there is
		// no second authorization model on this node.
		EventsSnapshot:  mcpEventsSnapshotFn(actions),
		EventsKeepalive: keepalive,
	}
}
