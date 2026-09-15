package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/testenv"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type mcpHTTPRoundTripper func(*http.Request) (*http.Response, error)

func (f mcpHTTPRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// mcpHTTPWhoIsClient builds the same LocalClient shape internal/server uses to
// resolve caller identity, so a cmd-level test exercises the real WhoIs path
// rather than a stand-in for it.
func mcpHTTPWhoIsClient(t *testing.T, login string) *server.LocalClient {
	t.Helper()
	resp := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: login},
		Node:        &tailcfg.Node{},
	}
	return &server.LocalClient{
		OmitAuth: true,
		Transport: mcpHTTPRoundTripper(func(*http.Request) (*http.Response, error) {
			body, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal whois response: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}
}

// mcpHTTPCountingActions returns actions that record whether any tool ran.
// Every rejection test below asserts the counter stayed at zero.
func mcpHTTPCountingActions(calls *int) mcpActions {
	actions := fakeMCPActions()
	base := actions.list
	actions.list = func() (any, error) {
		*calls++
		return base()
	}
	baseUnshare := actions.unshare
	actions.unshare = func(name string) (any, error) {
		*calls++
		return baseUnshare(name)
	}
	return actions
}

const mcpHTTPHost = "tslink-mcp.example.ts.net"

// mcpHTTPToolCallRequest is a well-formed tools/call POST. Using a real call
// body is what makes "no tool executed" meaningful: a malformed body would be
// refused by the transport for reasons unrelated to the guard under test.
func mcpHTTPToolCallRequest(tool string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"` + mcpProtocolVersion + `","io.modelcontextprotocol/clientInfo":{"name":"probe","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := httptest.NewRequest(http.MethodPost, "https://"+mcpHTTPHost+server.MCPControlPlanePath, strings.NewReader(body))
	req.RemoteAddr = "100.64.0.9:1234"
	req.Host = mcpHTTPHost
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", mcpProtocolVersion)
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", tool)
	return req
}

func mcpHTTPControlPlaneHandler(t *testing.T, allow []string, login string, actions mcpActions) http.Handler {
	t.Helper()
	return server.NewMCPControlPlaneHandler(&server.MCPControlPlane{
		AllowedUsers: allow,
		Handler:      newMCPStreamableHandler(actions),
	}, mcpHTTPWhoIsClient(t, login))
}

// TestMCPHTTPBadOriginIsForbiddenAndNoToolRuns is the spec's Origin MUST
// asserted through the shipped control-plane handler with a real tool call in
// the body.
func TestMCPHTTPBadOriginIsForbiddenAndNoToolRuns(t *testing.T) {
	for _, origin := range []string{
		"https://evil.example.com",
		"http://" + mcpHTTPHost,
		"null",
	} {
		t.Run(origin, func(t *testing.T) {
			calls := 0
			handler := mcpHTTPControlPlaneHandler(t, []string{"alice@example.com"}, "alice@example.com", mcpHTTPCountingActions(&calls))

			req := mcpHTTPToolCallRequest("list")
			req.Header.Set("Origin", origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (body=%q)", rr.Code, http.StatusForbidden, rr.Body.String())
			}
			if calls != 0 {
				t.Fatalf("tool executions = %d, want 0 behind a rejected Origin", calls)
			}
			if strings.Contains(rr.Body.String(), "services") {
				t.Fatalf("rejection body carried a tool result: %q", rr.Body.String())
			}
		})
	}
}

// TestMCPHTTPUnauthorizedCallerRunsNoTool covers both authorization rejection
// paths through the shipped handler with a real tool call in the body.
func TestMCPHTTPUnauthorizedCallerRunsNoTool(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		login string
	}{
		{"caller not on the allow list", []string{"alice@example.com"}, "eve@example.com"},
		{"no principal configured", nil, "alice@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := mcpHTTPControlPlaneHandler(t, tc.allow, tc.login, mcpHTTPCountingActions(&calls))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, mcpHTTPToolCallRequest("unshare"))

			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d (body=%q)", rr.Code, http.StatusForbidden, rr.Body.String())
			}
			if calls != 0 {
				t.Fatalf("tool executions = %d, want 0 for an unauthorized caller", calls)
			}
		})
	}
}

// TestMCPHTTPAuthorizedCallerExecutesTheTool is the positive control for the
// two rejection tests above: the same request shape, with an authorized
// caller and no Origin, does reach a tool.
func TestMCPHTTPAuthorizedCallerExecutesTheTool(t *testing.T) {
	calls := 0
	handler := mcpHTTPControlPlaneHandler(t, []string{"alice@example.com"}, "alice@example.com", mcpHTTPCountingActions(&calls))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, mcpHTTPToolCallRequest("list"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if calls != 1 {
		t.Fatalf("tool executions = %d, want 1 for an authorized caller", calls)
	}
}

// mcpHTTPSession dials the streamable handler with the SDK's own client, so
// the parity assertions below travel the real wire protocol rather than an
// in-process shortcut.
func mcpHTTPSession(t *testing.T, actions mcpActions) *mcp.ClientSession {
	t.Helper()
	srv := httptest.NewServer(newMCPStreamableHandler(actions))
	t.Cleanup(srv.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "tslink-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: srv.Client(),
		// A stateless server answers GET with 405, so the client must not try
		// to open the optional standalone notification stream.
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect over streamable HTTP: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestMCPHTTPExposesTheSameToolNamesAsStdio pins the one-registry contract:
// the remote transport advertises exactly the stdio tool set.
func TestMCPHTTPExposesTheSameToolNamesAsStdio(t *testing.T) {
	session := mcpHTTPSession(t, fakeMCPActions())
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list over HTTP: %v", err)
	}
	httpNames := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		httpNames = append(httpNames, tool.Name)
	}

	stdioNames := make([]string, 0, len(mcpToolDefinitions))
	for _, definition := range mcpToolDefinitions {
		stdioNames = append(stdioNames, definition.Name)
	}

	if len(httpNames) != len(stdioNames) {
		t.Fatalf("HTTP advertised %d tools, stdio registry has %d", len(httpNames), len(stdioNames))
	}
	stdioSet := make(map[string]struct{}, len(stdioNames))
	for _, name := range stdioNames {
		stdioSet[name] = struct{}{}
	}
	for _, name := range httpNames {
		if _, ok := stdioSet[name]; !ok {
			t.Fatalf("HTTP advertised tool %q that the stdio registry does not define", name)
		}
	}
}

// TestMCPHTTPToolResultMatchesStdio is the payload-parity assertion. The same
// tool, called over both transports against the same actions, must produce the
// same structured result.
func TestMCPHTTPToolResultMatchesStdio(t *testing.T) {
	tools := []struct {
		name string
		args map[string]any
	}{
		{"list", map[string]any{}},
		{"status", map[string]any{}},
		{"url", map[string]any{"name": "demo"}},
		{"access_explain", map[string]any{"service": "demo"}},
		{"tags_list", map[string]any{}},
	}
	session := mcpHTTPSession(t, fakeMCPActions())

	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			httpResult, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      tool.name,
				Arguments: tool.args,
			})
			if err != nil {
				t.Fatalf("tools/call %q over HTTP: %v", tool.name, err)
			}
			if httpResult.IsError {
				t.Fatalf("tools/call %q over HTTP returned an error result: %+v", tool.name, httpResult.Content)
			}

			stdioResult := mcpStdioToolResult(t, tool.name, tool.args)
			if !reflect.DeepEqual(httpResult.StructuredContent, stdioResult) {
				t.Fatalf("tool %q payload differs between transports:\n http  = %#v\n stdio = %#v", tool.name, httpResult.StructuredContent, stdioResult)
			}
		})
	}
}

// mcpStdioToolResult calls one tool over the shipped stdio transport and
// returns the decoded structuredContent of its result.
func mcpStdioToolResult(t *testing.T, tool string, args map[string]any) any {
	t.Helper()
	arguments, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcpLegacyHandshakeVersion + `","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + string(arguments) + `}}`,
		"",
	}, "\n")
	frames := decodeMCPResponses(t, runMCPSession(t, input, fakeMCPActions()))
	frame := mcpFrameByID(t, frames, float64(2))
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("stdio tools/call %q returned no result: %+v", tool, frame)
	}
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("stdio tools/call %q returned an error result: %+v", tool, result)
	}
	return result["structuredContent"]
}

// TestMCPHTTPRejectsNonPostMethods records the stateless transport's shape:
// the current spec revision has a single POST endpoint.
func TestMCPHTTPRejectsNonPostMethods(t *testing.T) {
	calls := 0
	handler := mcpHTTPControlPlaneHandler(t, []string{"alice@example.com"}, "alice@example.com", mcpHTTPCountingActions(&calls))
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(method, "https://"+mcpHTTPHost+server.MCPControlPlanePath, nil)
		req.RemoteAddr = "100.64.0.9:1234"
		req.Host = mcpHTTPHost
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want %d", method, rr.Code, http.StatusMethodNotAllowed)
		}
	}
	if calls != 0 {
		t.Fatalf("tool executions = %d, want 0", calls)
	}
}

// TestResolveMCPControlPlaneSettingsIsOffByDefault covers the resolution rule:
// off unless the flag or the config key says otherwise, flag winning.
func TestResolveMCPControlPlaneSettingsIsOffByDefault(t *testing.T) {
	cases := []struct {
		name        string
		flag        bool
		cfg         config.GlobalConfig
		wantEnabled bool
		wantAllow   []string
	}{
		{"nothing configured", false, config.GlobalConfig{}, false, nil},
		{"config present but disabled", false, config.GlobalConfig{MCP: &config.MCPConfig{Allow: []string{"alice@example.com"}}}, false, []string{"alice@example.com"}},
		{"config enables", false, config.GlobalConfig{MCP: &config.MCPConfig{Enabled: true, Allow: []string{"alice@example.com"}}}, true, []string{"alice@example.com"}},
		{"flag enables without config", true, config.GlobalConfig{}, true, nil},
		{"flag enables over a disabled config", true, config.GlobalConfig{MCP: &config.MCPConfig{Allow: []string{"alice@example.com"}}}, true, []string{"alice@example.com"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := resolveMCPControlPlaneSettings(tc.flag, tc.cfg)
			if settings.Enabled != tc.wantEnabled {
				t.Fatalf("Enabled = %v, want %v", settings.Enabled, tc.wantEnabled)
			}
			if !reflect.DeepEqual(settings.Allow, tc.wantAllow) {
				t.Fatalf("Allow = %#v, want %#v", settings.Allow, tc.wantAllow)
			}
		})
	}
}

// TestBuildMCPControlPlaneIsNilWhenDisabled is the other half of the
// default-off proof: with the feature off, no control plane object — and
// therefore no handler and no node — is ever built.
func TestBuildMCPControlPlaneIsNilWhenDisabled(t *testing.T) {
	if cp := buildMCPControlPlane(mcpControlPlaneSettings{}, fakeMCPActions(), []string{"tag:tsmain"}, 0); cp != nil {
		t.Fatalf("buildMCPControlPlane() = %+v, want nil when disabled", cp)
	}
	cp := buildMCPControlPlane(mcpControlPlaneSettings{Enabled: true}, fakeMCPActions(), []string{"tag:tsmain"}, 0)
	if cp == nil {
		t.Fatal("buildMCPControlPlane() = nil when enabled")
	}
	// Enabled with no principal is a start-time refusal, not a permissive
	// endpoint.
	if err := cp.Validate(); err == nil {
		t.Fatal("Validate() = nil for an enabled control plane with no principal")
	}
	withPrincipal := buildMCPControlPlane(mcpControlPlaneSettings{Enabled: true, Allow: []string{"alice@example.com"}}, fakeMCPActions(), []string{"tag:tsmain"}, 0)
	if err := withPrincipal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v for a configured control plane", err)
	}
	if !reflect.DeepEqual(withPrincipal.Tags, []string{"tag:tsmain"}) {
		t.Fatalf("Tags = %#v, want the default tag", withPrincipal.Tags)
	}
}

// TestServeCommandDeclaresTheControlPlaneOffByDefault reads the shipped flag
// definition. A flag that defaulted to true, or help text that did not name
// the exposure, would both be silent product failures.
func TestServeCommandDeclaresTheControlPlaneOffByDefault(t *testing.T) {
	serveCmd, _, err := rootCmd.Find([]string{"serve"})
	if err != nil {
		t.Fatalf("find serve command: %v", err)
	}
	flag := serveCmd.Flags().Lookup("mcp")
	if flag == nil {
		t.Fatal("serve has no --mcp flag")
	}
	if flag.DefValue != "false" {
		t.Fatalf("--mcp default = %q, want \"false\"", flag.DefValue)
	}
	for _, phrase := range []string{"off by default", "Authorization is mandatory", "refuses to start", "mcp.allow", "public internet"} {
		if !strings.Contains(serveCmd.Long, phrase) {
			t.Fatalf("serve --help does not state %q", phrase)
		}
	}
}

// mcpHTTPRecordingServer is a serverRunner that records the control plane the
// serve wiring hands it.
type mcpHTTPRecordingServer struct {
	setCalls     int
	controlPlane *server.MCPControlPlane
	runErr       error
}

func (m *mcpHTTPRecordingServer) SetMCPControlPlane(cp *server.MCPControlPlane) {
	m.setCalls++
	m.controlPlane = cp
}

func (m *mcpHTTPRecordingServer) Run(context.Context) error { return m.runErr }

// TestServeWiringLeavesTheControlPlaneUnsetByDefault is the default-off proof
// at the command layer: a serve run with no --mcp and no config key never even
// tells the daemon that a control plane exists, so no handler is built and no
// tsnet node can be created for it.
func TestServeWiringLeavesTheControlPlaneUnsetByDefault(t *testing.T) {
	recorder := mcpHTTPRunForeground(t, mcpControlPlaneSettings{})
	if recorder.setCalls != 0 {
		t.Fatalf("SetMCPControlPlane called %d times with the feature off, want 0", recorder.setCalls)
	}
	if recorder.controlPlane != nil {
		t.Fatalf("control plane = %+v with the feature off, want nil", recorder.controlPlane)
	}
}

// TestServeWiringInstallsTheControlPlaneWhenEnabled proves the enable actually
// reaches the daemon, and that the object it installs carries the configured
// principals.
func TestServeWiringInstallsTheControlPlaneWhenEnabled(t *testing.T) {
	recorder := mcpHTTPRunForeground(t, mcpControlPlaneSettings{Enabled: true, Allow: []string{"alice@example.com"}})
	if recorder.setCalls != 1 {
		t.Fatalf("SetMCPControlPlane called %d times, want 1", recorder.setCalls)
	}
	cp := recorder.controlPlane
	if cp == nil {
		t.Fatal("control plane = nil with the feature enabled")
	}
	if err := cp.Validate(); err != nil {
		t.Fatalf("Validate() error = %v for a configured control plane", err)
	}
	if !reflect.DeepEqual(cp.AllowedUsers, []string{"alice@example.com"}) {
		t.Fatalf("AllowedUsers = %#v, want the configured principal", cp.AllowedUsers)
	}
	if cp.Handler == nil {
		t.Fatal("control plane has no handler")
	}
}

// TestServeWiringEnabledWithoutPrincipalIsARefusal shows what the daemon
// receives when the feature is enabled with nothing authorized: an object
// whose own validation is the documented refusal, which Server.Run turns into
// a failed start rather than an open endpoint.
func TestServeWiringEnabledWithoutPrincipalIsARefusal(t *testing.T) {
	recorder := mcpHTTPRunForeground(t, mcpControlPlaneSettings{Enabled: true})
	if recorder.controlPlane == nil {
		t.Fatal("control plane = nil with the feature enabled")
	}
	if err := recorder.controlPlane.Validate(); !errors.Is(err, server.ErrMCPNoPrincipal) {
		t.Fatalf("Validate() error = %v, want ErrMCPNoPrincipal", err)
	}
}

func mcpHTTPRunForeground(t *testing.T, settings mcpControlPlaneSettings) *mcpHTTPRecordingServer {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	if err := config.EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	saveServeState(t)
	recorder := &mcpHTTPRecordingServer{}
	serveWritePIDFn = func(path string) error { return os.WriteFile(path, []byte("1"), 0o600) }
	serveRemovePIDFn = func(path string) { _ = os.Remove(path) }
	serveWithPIDLockFn = func(_ string, fn func() error) error { return fn() }
	serveNewServerFn = func(string, string) (serverRunner, error) { return recorder, nil }

	if err := runForegroundWithOptions(filepath.Join(home, "test.pid"), "fake-key", "", foregroundOptions{
		Credentialed: true,
		MCP:          settings,
	}); err != nil {
		t.Fatalf("runForegroundWithOptions() error = %v", err)
	}
	return recorder
}
