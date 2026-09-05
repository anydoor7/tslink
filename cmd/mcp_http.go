package cmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/server"
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
	Enabled  bool
	Allow    []string
	NodeName string
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

// buildMCPControlPlane returns the daemon-side control plane for these
// settings, or nil when it is disabled. A nil result is what keeps the default
// path free of any listener or tsnet node.
func buildMCPControlPlane(settings mcpControlPlaneSettings, actions mcpActions, tags []string) *server.MCPControlPlane {
	if !settings.Enabled {
		return nil
	}
	return &server.MCPControlPlane{
		NodeName:     settings.NodeName,
		Tags:         append([]string(nil), tags...),
		AllowedUsers: append([]string(nil), settings.Allow...),
		Handler:      newMCPStreamableHandler(actions),
	}
}
