package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const (
	mcpProtocolVersion = "2025-11-25"
	mcpMaxRecordBytes  = 1024 * 1024
)

var mcpSupportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

type mcpToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

var (
	mcpShareOutputSchema = objectSchema(map[string]any{
		"url":      map[string]any{"type": "string"},
		"name":     map[string]any{"type": "string"},
		"status":   map[string]any{"type": "string", "enum": []string{shareStatusReady, authStatusNeedsLogin}},
		"auth_url": map[string]any{"type": "string"},
	}, "status")
	mcpListOutputSchema = objectSchema(map[string]any{
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":        map[string]any{"type": "string"},
				"type":        map[string]any{"type": "string"},
				"url":         map[string]any{"type": []string{"string", "null"}},
				"url_pending": map[string]any{"type": "boolean"},
				"state":       map[string]any{"type": "string"},
			}, "name", "type", "url", "url_pending", "state"),
		},
	}, "services")
	mcpUnshareOutputSchema = objectSchema(map[string]any{
		"ok":                     map[string]any{"type": "boolean", "description": "Whether the idempotent unshare request removed the local registry entry or found the service absent (removed false); ok true does not guarantee tailnet device cleanup, so check device_cleaned and device_warning."},
		"name":                   map[string]any{"type": "string"},
		"removed":                map[string]any{"type": "boolean"},
		"device_cleaned":         map[string]any{"type": "boolean"},
		"device_cleanup_skipped": map[string]any{"type": "boolean"},
		"device_skip_reason":     map[string]any{"type": "string"},
		"device_warning":         map[string]any{"type": "string"},
	}, "ok", "name", "removed", "device_cleaned", "device_cleanup_skipped")
	mcpStatusOutputSchema = objectSchema(map[string]any{
		"authenticated":            map[string]any{"type": "boolean", "description": "Legacy alias for node_authorized; it is not a stored-credential indicator."},
		"credential_stored":        map[string]any{"type": "boolean"},
		"node_authorized":          map[string]any{"type": "boolean"},
		"authorized_service_count": map[string]any{"type": "integer", "minimum": 0},
		"daemon_running":           map[string]any{"type": "boolean"},
		"service_count":            map[string]any{"type": "integer", "minimum": 0},
		"status":                   map[string]any{"type": "string"},
		"auth_url":                 map[string]any{"type": "string"},
	}, "authenticated", "credential_stored", "node_authorized", "authorized_service_count", "daemon_running", "service_count")
)

var mcpToolDefinitions = []mcpToolDefinition{
	{
		Name:        "share",
		Description: "Expose a local directory, one file, or an HTTP port to the user's private Tailscale network. Use this after creating a local page or report that the user wants to open on another tailnet device. If status is needs_login, open auth_url in a browser and retry after authorization.",
		InputSchema: objectSchema(map[string]any{
			"target":    map[string]any{"type": "string", "minLength": 1, "description": "Existing file or directory path, bare port from 1 to 65535, or host:port HTTP target."},
			"name":      map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Optional requested DNS-label service name. A matching target is reused only if it already has this name; unrelated name collisions receive a numeric suffix."},
			"ephemeral": map[string]any{"type": "boolean", "default": true, "description": "Keep true for temporary shares; set false only when the user wants durable tailnet node state."},
		}, "target"),
		OutputSchema: mcpShareOutputSchema,
	},
	{
		Name:         "list",
		Description:  "List locally registered TSLink services with exact runtime URLs when available. Use this to discover current shares or check whether a service URL is ready.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpListOutputSchema,
	},
	{
		Name:        "unshare",
		Description: "Remove one named service from the local TSLink registry. Use this when the user asks to stop sharing a specific service; it does not expose credentials or open a network listener.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name to remove."},
		}, "name"),
		OutputSchema: mcpUnshareOutputSchema,
	},
	{
		Name:         "status",
		Description:  "Report local TSLink daemon, stored-credential, node-authorization, and service-count state. Use this before retrying a share or when diagnosing why a URL is not ready; a pending needs_login handoff includes auth_url even if the daemon stopped.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpStatusOutputSchema,
	},
}

type mcpActions struct {
	share   func(context.Context, string, string, bool) (ShareResult, error)
	list    func() (any, error)
	unshare func(string) (any, error)
	status  func() (any, error)
}

type mcpServiceSummary struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	URL        *string `json:"url"`
	URLPending bool    `json:"url_pending"`
	State      string  `json:"state"`
}

type mcpStatusSummary struct {
	Authenticated          bool   `json:"authenticated"`
	CredentialStored       bool   `json:"credential_stored"`
	NodeAuthorized         bool   `json:"node_authorized"`
	AuthorizedServiceCount int    `json:"authorized_service_count"`
	DaemonRunning          bool   `json:"daemon_running"`
	ServiceCount           int    `json:"service_count"`
	Status                 string `json:"status,omitempty"`
	AuthURL                string `json:"auth_url,omitempty"`
}

type mcpUnshareSummary struct {
	OK                   bool   `json:"ok"`
	Name                 string `json:"name"`
	Removed              bool   `json:"removed"`
	DeviceCleaned        bool   `json:"device_cleaned"`
	DeviceCleanupSkipped bool   `json:"device_cleanup_skipped"`
	DeviceSkipReason     string `json:"device_skip_reason,omitempty"`
	DeviceWarning        string `json:"device_warning,omitempty"`
}

func defaultMCPActions(paths sharePaths, errOut io.Writer) mcpActions {
	return mcpActions{
		share: func(ctx context.Context, target, name string, ephemeral bool) (ShareResult, error) {
			return executeShare(ctx, paths, target, name, ephemeral, defaultURLWait, errOut)
		},
		list: func() (any, error) {
			result, err := loadListResultForPaths(paths.Registry, paths.PID, paths.Snapshot, listOptions{})
			if err != nil {
				return nil, err
			}
			summaries, ok := result.Services.([]ListServiceSummary)
			if !ok {
				return nil, fmt.Errorf("unexpected list result type %T", result.Services)
			}
			services := make([]mcpServiceSummary, 0, len(summaries))
			for _, service := range summaries {
				services = append(services, mcpServiceSummary(service))
			}
			return map[string]any{"services": services}, nil
		},
		unshare: func(name string) (any, error) {
			if err := registry.ValidateName(name); err != nil {
				return nil, err
			}
			removed, err := removeServiceResult(paths.Registry, name)
			if err != nil {
				return nil, err
			}
			return mcpUnshareSummary{
				OK:                   true,
				Name:                 removed.Name,
				Removed:              removed.Removed,
				DeviceCleaned:        removed.DeviceCleaned,
				DeviceCleanupSkipped: removed.DeviceCleanupSkipped,
				DeviceSkipReason:     removed.DeviceSkipReason,
				DeviceWarning:        removed.DeviceWarning,
			}, nil
		},
		status: func() (any, error) {
			status, err := sharePollableStatusFn(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
			if err != nil {
				return nil, err
			}
			result := mcpStatusSummary{
				Authenticated:          status.NodeAuthorized,
				CredentialStored:       status.CredentialStored,
				NodeAuthorized:         status.NodeAuthorized,
				AuthorizedServiceCount: status.AuthorizedServiceCount,
				DaemonRunning:          status.DaemonRunning,
				ServiceCount:           status.ServiceCount,
			}
			if status.AuthStatus == authStatusNeedsLogin && status.AuthURL != "" {
				result.Status = authStatusNeedsLogin
				result.AuthURL = status.AuthURL
			}
			return result, nil
		},
	}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content           []mcpContent   `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

type mcpServer struct {
	in             io.Reader
	out            io.Writer
	actions        mcpActions
	initializeSeen bool
	initialized    bool
}

func newMCPServer(in io.Reader, out io.Writer, actions mcpActions) *mcpServer {
	return &mcpServer{in: in, out: out, actions: actions}
}

func validMCPRequestID(id json.RawMessage) bool {
	if len(id) == 0 || bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return false
	}
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

func (s *mcpServer) write(response mcpResponse) error {
	return json.NewEncoder(s.out).Encode(response)
}

func (s *mcpServer) writeError(id json.RawMessage, code int, message string) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return s.write(mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message}})
}

func (s *mcpServer) serve(ctx context.Context) error {
	reader := bufio.NewReaderSize(s.in, 64*1024)
	record := make([]byte, 0, 64*1024)
	discardingOversize := false
	for {
		fragment, continued, readErr := reader.ReadLine()
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			_ = s.writeError(nil, -32600, "Failed to read JSON-RPC message")
			return readErr
		}
		if !discardingOversize {
			if len(fragment) > mcpMaxRecordBytes-len(record) {
				discardingOversize = true
				record = record[:0]
			} else {
				record = append(record, fragment...)
			}
		}
		if continued {
			continue
		}
		if discardingOversize {
			if err := s.writeError(nil, -32600, "JSON-RPC message exceeds maximum size"); err != nil {
				return err
			}
			discardingOversize = false
			continue
		}

		line := bytes.TrimSpace(record)
		record = record[:0]
		if len(line) == 0 {
			continue
		}
		if err := s.handleLine(ctx, line); err != nil {
			return err
		}
	}
}

func (s *mcpServer) handleLine(ctx context.Context, line []byte) error {
	if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '[' {
		return s.writeError(nil, -32600, "Batch requests are not supported")
	}
	var request mcpRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return s.writeError(nil, -32700, "Parse error")
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return s.writeError(request.ID, -32600, "Invalid Request")
	}
	if len(request.ID) == 0 {
		s.handleNotification(request)
		return nil
	}
	if !validMCPRequestID(request.ID) {
		return s.writeError(nil, -32600, "Invalid Request")
	}
	return s.handleRequest(ctx, request)
}

func (s *mcpServer) handleNotification(request mcpRequest) {
	if request.Method == "notifications/initialized" && s.initializeSeen {
		s.initialized = true
	}
}

func (s *mcpServer) handleRequest(ctx context.Context, request mcpRequest) error {
	switch request.Method {
	case "initialize":
		return s.initialize(request)
	case "ping":
		return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
	}
	if !s.initialized {
		return s.writeError(request.ID, -32002, "Server not initialized")
	}
	switch request.Method {
	case "tools/list":
		return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": mcpToolDefinitions}})
	case "tools/call":
		return s.callTool(ctx, request)
	default:
		return s.writeError(request.ID, -32601, "Method not found")
	}
}

func (s *mcpServer) initialize(request mcpRequest) error {
	if s.initializeSeen {
		return s.writeError(request.ID, -32600, "Server already initialized")
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    any    `json:"capabilities,omitempty"`
		ClientInfo      any    `json:"clientInfo,omitempty"`
		Meta            any    `json:"_meta,omitempty"`
	}
	if err := decodeMCPParams(request.Params, &params); err != nil || params.ProtocolVersion == "" {
		return s.writeError(request.ID, -32602, "Invalid initialize parameters")
	}
	version := mcpProtocolVersion
	if mcpSupportedProtocolVersions[params.ProtocolVersion] {
		version = params.ProtocolVersion
	}
	serverVersion := Version
	if serverVersion == "" {
		serverVersion = "dev"
	}
	s.initializeSeen = true
	result := map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    "tslink",
			"version": serverVersion,
		},
		"instructions": "Use share to expose a local page to the private tailnet. A needs_login tool result is successful: open auth_url and retry after authorization.",
	}
	return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func decodeMCPParams(raw json.RawMessage, target any) error {
	return decodeMCPObject(raw, target, false)
}

func decodeMCPArguments(raw json.RawMessage, target any) error {
	return decodeMCPObject(raw, target, true)
}

func decodeMCPObject(raw json.RawMessage, target any, strict bool) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func (s *mcpServer) callTool(ctx context.Context, request mcpRequest) error {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeMCPParams(request.Params, &call); err != nil || call.Name == "" {
		return s.writeError(request.ID, -32602, "Invalid tools/call parameters")
	}
	var data any
	var err error
	switch call.Name {
	case "share":
		var args struct {
			Target    string `json:"target"`
			Name      string `json:"name,omitempty"`
			Ephemeral *bool  `json:"ephemeral,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Target == "" {
			return s.writeError(request.ID, -32602, "Invalid share arguments")
		}
		ephemeral := true
		if args.Ephemeral != nil {
			ephemeral = *args.Ephemeral
		}
		data, err = s.actions.share(ctx, args.Target, args.Name, ephemeral)
	case "list":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid list arguments")
		}
		data, err = s.actions.list()
	case "unshare":
		var args struct {
			Name string `json:"name"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" {
			return s.writeError(request.ID, -32602, "Invalid unshare arguments")
		}
		data, err = s.actions.unshare(args.Name)
	case "status":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid status arguments")
		}
		data, err = s.actions.status()
	default:
		return s.writeError(request.ID, -32602, "Unknown tool: "+call.Name)
	}
	result := makeMCPToolResult(data, err)
	return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func makeMCPToolResult(data any, callErr error) mcpToolResult {
	if callErr != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: callErr.Error()}}, IsError: true}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}
	}
	structured := map[string]any{}
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: err.Error()}}, IsError: true}
	}
	return mcpToolResult{
		Content:           []mcpContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: structured,
	}
}

func init() {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the TSLink MCP server over stdio",
		Long: `Run a local Model Context Protocol server using newline-delimited JSON-RPC
over stdin/stdout. The server exposes four tools: share, list, unshare, and
status. The MCP process itself opens no network listener; invoking share may
start the separate TSLink daemon and its requested tsnet service. Protocol
frames are written only to stdout; diagnostics and logs are written only to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput(cmd) {
				fmt.Fprintln(cmd.ErrOrStderr(), "Error: --json is not valid with tslink mcp; stdout is reserved for JSON-RPC frames")
				return output.SilentExit(output.ExitUsage)
			}
			if err := shareEnsureDirFn(); err != nil {
				return err
			}
			paths, err := resolveSharePaths()
			if err != nil {
				return err
			}
			server := newMCPServer(cmd.InOrStdin(), cmd.OutOrStdout(), defaultMCPActions(paths, cmd.ErrOrStderr()))
			if err := server.serve(cmd.Context()); err != nil {
				return fmt.Errorf("mcp stdio: %w", err)
			}
			return nil
		},
	}
	rootCmd.AddCommand(mcpCmd)
}
