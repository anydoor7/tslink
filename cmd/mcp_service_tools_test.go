package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

// This file holds the CLI-versus-MCP contract. Two independent problems live
// here and the split matters:
//
//   - Surface drift. `cmd/mcp.go` declares its own input schemas, so a flag
//     added to a Cobra command has no mechanical reason to appear as a tool
//     parameter. Nothing failed when share's schema shipped without allow,
//     which is how the MCP share tool came to create services with no
//     allow-list — readable by every member of the tailnet. TestMCPToolSurfaceCoversTheManifest is the gate:
//     every command in docs/cli-manifest.json is either mapped onto MCP tools
//     with each of its flags accounted for, or explicitly excluded with a
//     reason, and every MCP tool is claimed by exactly one command.
//
//   - Data drift. A tool can exist, take the right arguments, and still return
//     something the CLI would not. TestCompiledCLIAndMCPServiceDataAgree runs
//     both surfaces of the shipped binary against one config directory and
//     compares the payload bytes.
//
// Neither test is allowed to reach a real tailnet. The read paths below touch
// the local registry, PID file and runtime snapshot only; the invite tools,
// which do call the Tailscale API, are exercised through the package's existing
// invite*Fn seams.

// mcpCoveredCommand maps one docs/cli-manifest.json command onto the MCP tools
// that carry it.
//
// Flags maps a manifest flag name to the input-schema property that carries it.
// ExcludedFlags records, per flag, why the tool surface deliberately does not
// carry it. A flag must appear in exactly one of the two; a flag in neither
// fails the test, which is the point — a new CLI flag becomes a decision
// somebody has to write down rather than a silent gap.
//
// Args lists the input-schema properties standing in for the command's
// positional arguments. docs/cli-manifest.json records flags only, so this
// column is hand-written and checked against the live schemas.
type mcpCoveredCommand struct {
	Tools         []string
	Args          []string
	Flags         map[string]string
	ExcludedFlags map[string]string
}

const mcpJSONFlagExclusion = "MCP tool results are always structured JSON; --json has no analogue in the protocol"

// mcpCoveredCommands is the mapping half of the partition.
var mcpCoveredCommands = map[string]mcpCoveredCommand{
	"tslink share": {
		Tools: []string{"share"},
		Args:  []string{"target"},
		Flags: map[string]string{
			"no-daemon-install": "no_daemon_install",
			"ephemeral":         "ephemeral",
			"name":              "name",
		},
		ExcludedFlags: map[string]string{
			"json": mcpJSONFlagExclusion,
			"wait": "the share tool always waits the CLI's own 30s default, because an agent has no way to poll a stdio tool call it has not returned from",
		},
	},
	"tslink add": {
		Tools: []string{"add"},
		Args:  []string{"name", "type"},
		Flags: map[string]string{
			"allow":             "allow",
			"control-url":       "control_url",
			"dir":               "dir",
			"ephemeral":         "ephemeral",
			"funnel":            "funnel",
			"funnel-ttl":        "funnel_ttl",
			"no-auto-provision": "no_auto_provision",
			"no-daemon-install": "no_daemon_install",
			"proxy":             "target",
			"public":            "public_ack",
			"tags":              "tags",
			"tcp":               "target",
		},
		ExcludedFlags: map[string]string{

			"acme-email": "reserved and rejected with feature_unavailable; exposing it would only offer a parameter that always fails",
			"domain":     "reserved and rejected with feature_unavailable; exposing it would only offer a parameter that always fails",
			"dry-run":    "the add tool has one closed output schema for the write path and does not model the dry-run service preview",
			"json":       mcpJSONFlagExclusion,
			"wait":       "the add tool returns immediately with url_pending; an agent polls with the url tool, which takes its own wait",
		},
	},
	"tslink list": {
		Tools: []string{"list"},
		ExcludedFlags: map[string]string{
			"fields":  "the list tool always returns the default slim projection; an agent selects fields from the structured result itself",
			"json":    mcpJSONFlagExclusion,
			"name":    "the list tool always returns every service; an agent filters the structured result itself, and url covers the single-service lookup",
			"type":    "the list tool always returns every service; an agent filters the structured result itself",
			"tailnet": "the list tool answers from this machine's registry alone; --tailnet performs a live Tailscale API device read, and the tool contract keeps remote reads out of the local-inventory surface",
			"verbose": "the owner-only diagnostic view is not part of the tool contract",
		},
	},
	"tslink remove": {
		Tools: []string{"unshare"},
		Args:  []string{"name"},
		ExcludedFlags: map[string]string{
			"json":   mcpJSONFlagExclusion,
			"strict": "the unshare tool is idempotent by contract; its output schema reports removed=false for an absent service instead of failing",
		},
	},
	"tslink status": {
		Tools: []string{"status"},
		ExcludedFlags: map[string]string{
			"json": mcpJSONFlagExclusion,
			"name": "the status tool reports the daemon, credential and service-count summary only; per-service state comes from list and url",
			"urls": "the status tool reports the daemon, credential and service-count summary only; per-service URLs come from list and url",
		},
	},
	"tslink url": {
		Tools: []string{"url"},
		Args:  []string{"name"},
		Flags: map[string]string{"wait": "wait"},
		ExcludedFlags: map[string]string{
			"json": mcpJSONFlagExclusion,
			"raw":  "a human output format; the tool always returns the structured URLResult",
		},
	},
	"tslink tags list": {
		Tools:         []string{"tags_list"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink tags set": {
		Tools:         []string{"tags_set"},
		Args:          []string{"service", "tag"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink access explain": {
		Tools:         []string{"access_explain"},
		Args:          []string{"service"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink doctor": {
		Tools: []string{"doctor"},
		Flags: map[string]string{"probe-external": "probe_external"},
		ExcludedFlags: map[string]string{
			"json":         mcpJSONFlagExclusion,
			"probe-remote": "it reads the Tailscale API once per stored credential and writes last_verified into credential-meta.json; a remote read plus a local credential-metadata write is not something a tool call should do implicitly",
		},
	},
	"tslink logs": {
		Tools: []string{"logs"},
		Flags: map[string]string{
			"last":   "last",
			"level":  "level",
			"source": "source",
		},
		ExcludedFlags: map[string]string{
			"json": mcpJSONFlagExclusion,
		},
	},
	"tslink invite user": {
		Tools: []string{"invite_user"},
		Args:  []string{"email"},
		Flags: map[string]string{
			"print-link": "print_link",
			"role":       "role",
		},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink invite device": {
		Tools: []string{"invite_device"},
		Args:  []string{"service", "email"},
		Flags: map[string]string{
			"allow-exit-node": "allow_exit_node",
			"multi-use":       "multi_use",
			"print-link":      "print_link",
		},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink invite list": {
		Tools:         []string{"invite_list"},
		Flags:         map[string]string{"show-urls": "show_urls"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink invite revoke": {
		Tools:         []string{"invite_revoke"},
		Args:          []string{"invite_id"},
		Flags:         map[string]string{"kind": "kind"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink invite resend": {
		Tools:         []string{"invite_resend"},
		Args:          []string{"invite_id"},
		Flags:         map[string]string{"kind": "kind"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink template list": {
		Tools:         []string{"template_list"},
		ExcludedFlags: map[string]string{"json": mcpJSONFlagExclusion},
	},
	"tslink template apply": {
		Tools: []string{"template_plan", "template_apply"},
		Flags: map[string]string{"no-daemon-install": "no_daemon_install"},
		Args:  []string{"name"},
		ExcludedFlags: map[string]string{

			"dry-run": "carried by the tool split: template_plan is the dry run, template_apply is the write",
			"json":    mcpJSONFlagExclusion,
			"yes":     "carried by the tool split: calling template_apply is the confirmation",
		},
	},
}

// mcpUncoveredCommands is the exclusion half of the partition. Each entry says
// why the command has no MCP tool. The owner scoped this surface to what an
// agent does *to a service*; daemon lifecycle, installation, credentials,
// global configuration and log reading stay on the CLI.
var mcpUncoveredCommands = map[string]string{
	"tslink":                    "the root command carries only the global --json flag and runs no action of its own",
	"tslink access":             "command group; its only leaf, access explain, is covered",
	"tslink invite":             "command group; every leaf is covered",
	"tslink tags":               "command group; the covered leaves are tags list and tags set",
	"tslink template":           "command group; the covered leaves are template list and template apply",
	"tslink registry":           "command group; its only leaf, registry check, is excluded",
	"tslink config":             "global configuration rather than a service; excluded from this tool surface by the owner",
	"tslink config get":         "global configuration rather than a service; excluded from this tool surface by the owner",
	"tslink config list":        "global configuration rather than a service; excluded from this tool surface by the owner",
	"tslink config set":         "global configuration rather than a service; excluded from this tool surface by the owner",
	"tslink login":              "credential entry; excluded from this tool surface by the owner",
	"tslink logout":             "credential removal; excluded from this tool surface by the owner",
	"tslink install":            "writes a real LaunchAgent/systemd unit into the operator's live user domain; excluded from this tool surface by the owner",
	"tslink uninstall":          "removes a real LaunchAgent/systemd unit from the operator's live user domain; excluded from this tool surface by the owner",
	"tslink serve":              "daemon lifecycle; excluded from this tool surface by the owner. The share tool starts the daemon as a side effect when one is needed",
	"tslink stop":               "daemon lifecycle; excluded from this tool surface by the owner",
	"tslink cleanup":            "reconciles and can delete real tailnet devices; excluded from this tool surface by the owner",
	"tslink registry check":     "registry file forensics; excluded from this tool surface by the owner. The doctor tool reports registry health",
	"tslink manifest":           "describes the CLI itself; the MCP client reads tools/list instead",
	"tslink mcp":                "this server itself",
	"tslink tags add":           "not requested for this surface; tags_list plus tags_set reach the same end state, and set is the operation that can change reachability",
	"tslink tags set-default":   "changes the global default tag rather than one service",
	"tslink tags pull":          "reads the remote Tailscale ACL policy",
	"tslink tags delete-remote": "deletes a remote Tailscale ACL tag owner rule globally",
	"tslink template show":      "template_plan returns the same per-service view and additionally says which services already exist",
}

func mcpManifestFixture(t *testing.T) CLIManifest {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root for docs/cli-manifest.json")
	}
	path := filepath.Join(filepath.Dir(filepath.Dir(file)), "docs", "cli-manifest.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var manifest CLIManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(manifest.Commands) == 0 {
		t.Fatalf("%s declares no commands", path)
	}
	return manifest
}

// mcpToolInputProperties returns the union of the named tools' input-schema
// property names.
func mcpToolInputProperties(t *testing.T, tools []string) map[string]struct{} {
	t.Helper()
	properties := map[string]struct{}{}
	for _, name := range tools {
		for property := range mcpToolByName(t, name).InputSchema["properties"].(map[string]any) {
			properties[property] = struct{}{}
		}
	}
	return properties
}

func TestMCPToolSurfaceCoversTheManifest(t *testing.T) {
	manifest := mcpManifestFixture(t)

	manifestCommands := map[string]CommandInfo{}
	for _, command := range manifest.Commands {
		manifestCommands[command.Path] = command
	}

	// 1. The two tables partition the manifest exactly. A command in neither
	//    (a new one) and a command in both are both failures.
	for path := range manifestCommands {
		_, covered := mcpCoveredCommands[path]
		reason, excluded := mcpUncoveredCommands[path]
		switch {
		case covered && excluded:
			t.Errorf("%q is both mapped to MCP tools and excluded", path)
		case !covered && !excluded:
			t.Errorf("%q is in docs/cli-manifest.json but neither mapped to an MCP tool nor excluded with a reason", path)
		case excluded && strings.TrimSpace(reason) == "":
			t.Errorf("%q is excluded with an empty reason", path)
		}
	}
	for path := range mcpCoveredCommands {
		if _, ok := manifestCommands[path]; !ok {
			t.Errorf("mapped command %q is not in docs/cli-manifest.json", path)
		}
	}
	for path := range mcpUncoveredCommands {
		if _, ok := manifestCommands[path]; !ok {
			t.Errorf("excluded command %q is not in docs/cli-manifest.json", path)
		}
	}

	// 2. Every declared tool is claimed by exactly one covered command, and
	//    every covered command names tools that exist. Together these stop a
	//    tool from being added without a CLI counterpart.
	claimed := map[string]string{}
	for path, covered := range mcpCoveredCommands {
		for _, tool := range covered.Tools {
			mcpToolByName(t, tool)
			if previous, ok := claimed[tool]; ok {
				t.Errorf("tool %q is claimed by both %q and %q", tool, previous, path)
				continue
			}
			claimed[tool] = path
		}
	}
	for _, tool := range mcpToolDefinitions {
		if _, ok := claimed[tool.Name]; !ok {
			t.Errorf("MCP tool %q is declared but no manifest command claims it", tool.Name)
		}
	}

	// 3. Every flag of a covered command is either carried by a tool parameter
	//    that actually exists, or excluded with a reason.
	for path, covered := range mcpCoveredCommands {
		command := manifestCommands[path]
		properties := mcpToolInputProperties(t, covered.Tools)
		for _, arg := range covered.Args {
			if _, ok := properties[arg]; !ok {
				t.Errorf("%q positional argument %q has no input-schema property in %v", path, arg, covered.Tools)
			}
		}
		for _, flag := range command.Flags {
			property, mapped := covered.Flags[flag.Name]
			reason, excluded := covered.ExcludedFlags[flag.Name]
			switch {
			case mapped && excluded:
				t.Errorf("%q flag --%s is both mapped to %q and excluded", path, flag.Name, property)
			case !mapped && !excluded:
				t.Errorf("%q flag --%s is in docs/cli-manifest.json but neither carried by an MCP tool parameter nor excluded with a reason", path, flag.Name)
			case excluded && strings.TrimSpace(reason) == "":
				t.Errorf("%q flag --%s is excluded with an empty reason", path, flag.Name)
			case mapped:
				if _, ok := properties[property]; !ok {
					t.Errorf("%q flag --%s maps to input-schema property %q, which %v does not declare", path, flag.Name, property, covered.Tools)
				}
			}
		}
		manifestFlags := map[string]struct{}{}
		for _, flag := range command.Flags {
			manifestFlags[flag.Name] = struct{}{}
		}
		for flag := range covered.Flags {
			if _, ok := manifestFlags[flag]; !ok {
				t.Errorf("%q maps flag --%s, which docs/cli-manifest.json does not declare", path, flag)
			}
		}
		for flag := range covered.ExcludedFlags {
			if _, ok := manifestFlags[flag]; !ok {
				t.Errorf("%q excludes flag --%s, which docs/cli-manifest.json does not declare", path, flag)
			}
		}
	}

	names := make([]string, 0, len(mcpToolDefinitions))
	for _, tool := range mcpToolDefinitions {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	t.Logf("MCP tools covering %d manifest commands: %s", len(mcpCoveredCommands), strings.Join(names, ", "))
}

// --- CLI versus MCP data isomorphism -------------------------------------

// compiledMCPToolResult drives one tools/call against the shipped binary's
// stdio MCP server and returns the tool result. The text content is the exact
// json.Marshal of the action's return value, so it is byte-comparable with the
// same struct inside a --json result envelope. structuredContent is not: it
// round-trips through map[string]any and comes back with sorted keys.
func compiledMCPToolResult(t *testing.T, configDir, tool, arguments string) (text string, isError bool) {
	t.Helper()
	if arguments == "" {
		arguments = "{}"
	}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`,
	}, "\n") + "\n"
	stdout, stderr, exitCode := runCompiledTSLinkWithConfigDir(t, configDir, input, "mcp")
	if exitCode != 0 || stderr != "" {
		t.Fatalf("tslink mcp %s: exit=%d stderr=%q stdout=%s", tool, exitCode, stderr, stdout)
	}
	var last struct {
		ID     json.RawMessage `json:"id"`
		Result *struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	found := false
	for scanner.Scan() {
		if !bytes.Contains(scanner.Bytes(), []byte(`"id":2`)) {
			continue
		}
		if err := json.Unmarshal(scanner.Bytes(), &last); err != nil {
			t.Fatalf("decode %s tool frame: %v (%s)", tool, err, scanner.Text())
		}
		found = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s frames: %v", tool, err)
	}
	if !found {
		t.Fatalf("no tools/call response for %s in:\n%s", tool, stdout)
	}
	if last.Error != nil {
		t.Fatalf("%s returned a protocol error: %+v", tool, last.Error)
	}
	if last.Result == nil || len(last.Result.Content) != 1 {
		t.Fatalf("%s returned %+v, want one content item", tool, last.Result)
	}
	return last.Result.Content[0].Text, last.Result.IsError
}

func mcpJSONField(t *testing.T, body []byte, field string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatalf("decode object for field %q: %v\n%s", field, err, body)
	}
	value, ok := object[field]
	if !ok {
		t.Fatalf("field %q missing from %s", field, body)
	}
	return value
}

// seedCompiledDaemonEvidence starts the module-identity daemon fixture and
// writes a runtime snapshot that matches it, so the shipped binary resolves an
// exact URL for name. It contacts nothing: the fixture only writes a PID file.
func seedCompiledDaemonEvidence(t *testing.T, configDir, name string) {
	t.Helper()
	regPath := filepath.Join(configDir, "registry.json")
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	fixture := exec.Command(compiledDaemonIdentityFixture(t), "serve")
	fixture.Env = append(os.Environ(), "TSLINK_CONFIG_DIR="+configDir, "TSLINK_DISABLE_KEYRING=1")
	fixtureInput, err := fixture.StdinPipe()
	if err != nil {
		t.Fatalf("create daemon fixture input: %v", err)
	}
	fixtureOutput, err := fixture.StdoutPipe()
	if err != nil {
		t.Fatalf("create daemon fixture output: %v", err)
	}
	var fixtureStderr bytes.Buffer
	fixture.Stderr = &fixtureStderr
	if err := fixture.Start(); err != nil {
		t.Fatalf("start daemon identity fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = fixtureInput.Close()
		_ = fixture.Wait()
	})
	ready, readyErr := bufio.NewReader(fixtureOutput).ReadString('\n')
	if readyErr != nil || ready != "ready\n" {
		t.Fatalf("daemon identity fixture ready=%q err=%v stderr=%q", ready, readyErr, fixtureStderr.String())
	}
	pidPath := filepath.Join(configDir, "tslink.pid")
	if !daemon.IsRunning(pidPath) {
		t.Fatal("daemon identity fixture was not accepted as a running TSLink serve daemon")
	}
	pidInfo, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("stat pid fixture: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	states := make([]tsruntime.ServiceState, 0, len(reg.Services))
	for _, svc := range reg.Services {
		state := tsruntime.ServiceState{Service: svc}
		if svc.Name == name {
			state.RuntimeHost = name + ".tailnet-example.ts.net"
		}
		states = append(states, state)
	}
	snapshot := tsruntime.NewSnapshot(fixture.Process.Pid, pidInfo.ModTime(), fingerprint, pidInfo.ModTime().Add(time.Second), states)
	if err := tsruntime.Save(filepath.Join(configDir, "runtime.json"), snapshot); err != nil {
		t.Fatalf("save runtime fixture: %v", err)
	}
}

// TestCompiledCLIAndMCPServiceDataAgree is the data half of the contract, and
// the successor to the deleted TestCompiledCLIAndAPIStatusDataAreByteIsomorphic.
// It runs the shipped binary twice per case against one config directory: once
// as the CLI with --json, once as the stdio MCP server. url and access_explain
// return the identical struct on both surfaces, so their payload bytes must
// match exactly. list wraps its services array differently, so the array is
// compared. status is a deliberate projection, so its fields are compared
// against the CLI's.
func TestCompiledCLIAndMCPServiceDataAgree(t *testing.T) {
	configDir := t.TempDir()
	regPath := filepath.Join(configDir, "registry.json")
	for _, svc := range []registry.Service{
		{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, AllowedUsers: []string{"person@example.com"}},
		{Name: "docs", Type: registry.TypeProxy, Target: "http://localhost:4000", Tags: []string{"tag:tsmain"}},
	} {
		if _, err := registry.Add(regPath, svc); err != nil {
			t.Fatalf("registry.Add(%s): %v", svc.Name, err)
		}
	}
	seedCompiledDaemonEvidence(t, configDir, "web")

	byteIdentical := []struct {
		name      string
		cliArgs   []string
		tool      string
		arguments string
	}{
		{"url", []string{"url", "web", "--json"}, "url", `{"name":"web"}`},
		{"access explain", []string{"access", "explain", "web", "--json"}, "access_explain", `{"service":"web"}`},
	}
	for _, tc := range byteIdentical {
		t.Run(tc.name, func(t *testing.T) {
			cliOut, cliErr, cliExit := runCompiledTSLinkWithConfigDir(t, configDir, "", tc.cliArgs...)
			if cliExit != 0 || cliErr != "" {
				t.Fatalf("cli exit=%d stderr=%q stdout=%s", cliExit, cliErr, cliOut)
			}
			cliData := compiledResultDataJSON(t, cliOut)
			mcpText, isError := compiledMCPToolResult(t, configDir, tc.tool, tc.arguments)
			if isError {
				t.Fatalf("%s tool result is an error: %s", tc.tool, mcpText)
			}
			if !bytes.Equal(cliData, []byte(mcpText)) {
				t.Fatalf("%s payload mismatch\ncli=%s\nmcp=%s", tc.name, cliData, mcpText)
			}
			t.Logf("%s payload is byte-identical on both surfaces: %s", tc.name, mcpText)
		})
	}

	t.Run("list services", func(t *testing.T) {
		cliOut, cliErr, cliExit := runCompiledTSLinkWithConfigDir(t, configDir, "", "list", "--json")
		if cliExit != 0 || cliErr != "" {
			t.Fatalf("cli exit=%d stderr=%q stdout=%s", cliExit, cliErr, cliOut)
		}
		cliServices := mcpJSONField(t, compiledResultDataJSON(t, cliOut), "services")
		mcpText, isError := compiledMCPToolResult(t, configDir, "list", "{}")
		if isError {
			t.Fatalf("list tool result is an error: %s", mcpText)
		}
		mcpServices := mcpJSONField(t, []byte(mcpText), "services")
		if !bytes.Equal(cliServices, mcpServices) {
			t.Fatalf("list services mismatch\ncli=%s\nmcp=%s", cliServices, mcpServices)
		}
		if !bytes.Contains(mcpServices, []byte(`"https://web.tailnet-example.ts.net"`)) {
			t.Fatalf("list services did not carry the exact seeded URL: %s", mcpServices)
		}
	})

	t.Run("status projection", func(t *testing.T) {
		cliOut, cliErr, cliExit := runCompiledTSLinkWithConfigDir(t, configDir, "", "status", "--json")
		if cliExit != 0 || cliErr != "" {
			t.Fatalf("cli exit=%d stderr=%q stdout=%s", cliExit, cliErr, cliOut)
		}
		var cli StatusResult
		if err := json.Unmarshal(compiledResultDataJSON(t, cliOut), &cli); err != nil {
			t.Fatalf("decode CLI status: %v", err)
		}
		mcpText, isError := compiledMCPToolResult(t, configDir, "status", "{}")
		if isError {
			t.Fatalf("status tool result is an error: %s", mcpText)
		}
		var mcp mcpStatusSummary
		if err := json.Unmarshal([]byte(mcpText), &mcp); err != nil {
			t.Fatalf("decode MCP status: %v", err)
		}
		if mcp.Authenticated != cli.NodeAuthorized ||
			mcp.CredentialStored != cli.CredentialStored ||
			mcp.NodeAuthorized != cli.NodeAuthorized ||
			mcp.AuthorizedServiceCount != cli.AuthorizedServiceCount ||
			mcp.DaemonRunning != cli.DaemonRunning ||
			mcp.ServiceCount != cli.ServiceCount {
			t.Fatalf("status projection disagrees with the CLI\ncli=%+v\nmcp=%+v", cli, mcp)
		}
		if !mcp.DaemonRunning || mcp.ServiceCount != 2 {
			t.Fatalf("status projection = %+v, want the seeded daemon and two services", mcp)
		}
		if strings.Contains(mcpText, "credentials") || strings.Contains(mcpText, "daemon_pid") {
			t.Fatalf("status projection leaked owner-only fields: %s", mcpText)
		}
	})
}

// --- share exposure ------------------------------------------------------

func mcpSharePaths(t *testing.T) sharePaths {
	t.Helper()
	dir := t.TempDir()
	return sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		Ownership:   filepath.Join(dir, "node-ownership.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth.json"),
	}
}

func mcpLoadService(t *testing.T, regPath, name string) registry.Service {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	for _, svc := range reg.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("service %q not in registry %+v", name, reg.Services)
	return registry.Service{}
}

// TestMCPSharePersistsAllowListAndTags closes the gap this whole change exists
// for: before it, an MCP share always produced a service with an empty
// allow-list, which leaves the service readable by every tailnet member. The assertion is on the persisted registry entry, not
// on the tool's reply.
func TestMCPSharePersistsAllowListAndTags(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net", State: inspect.EndpointStateExact}}, nil
	}
	actions := defaultMCPActions(paths, io.Discard)

	result, err := actions.share(context.Background(), shareRequest{
		Target:    "3000",
		Name:      "guarded",
		Ephemeral: true,
		Allow:     []string{"Person@Example.com", "tag:ops"},
		Tags:      []string{"tag:tslink"},
	})
	if err != nil {
		t.Fatalf("share with allow: %v", err)
	}
	if result.Status != shareStatusReady || result.URL != "https://guarded.tail.ts.net" {
		t.Fatalf("share result = %+v", result)
	}
	svc := mcpLoadService(t, paths.Registry, "guarded")
	if len(svc.AllowedUsers) != 2 || svc.AllowedUsers[0] != "person@example.com" || svc.AllowedUsers[1] != "tag:ops" {
		t.Fatalf("persisted allow-list = %v, want the normalized principals", svc.AllowedUsers)
	}
	if len(svc.Tags) != 1 || svc.Tags[0] != "tag:tslink" {
		t.Fatalf("persisted tags = %v", svc.Tags)
	}
	if svc.Funnel || svc.PublicAck {
		t.Fatalf("private share persisted public exposure: %+v", svc)
	}

	// An unqualified share still records no allow-list, so the audit's
	// "readable by every tailnet member" reading of that state stays true and
	// the allow parameter is demonstrably the thing that changes it.
	if _, err := actions.share(context.Background(), shareRequest{Target: "3001", Name: "open", Ephemeral: true}); err != nil {
		t.Fatalf("share without allow: %v", err)
	}
	if open := mcpLoadService(t, paths.Registry, "open"); len(open.AllowedUsers) != 0 {
		t.Fatalf("share without allow persisted %v", open.AllowedUsers)
	}
}

// TestMCPShareFunnelGuardrailsStayInTheDomainLayer proves the refusals an MCP
// client meets are registry.ValidateFunnelGuardrails' own, reached through
// registry.AddIfMissing on the write, and not a second copy of the rules in
// cmd/mcp.go. Each case is checked twice: once through the tool, once by
// calling the domain validator directly with the service the tool would have
// written, and the stable codes must agree.
func TestMCPShareFunnelGuardrailsStayInTheDomainLayer(t *testing.T) {
	cases := []struct {
		name    string
		request shareRequest
		service registry.Service
		want    string
	}{
		{
			name:    "funnel without public_ack",
			request: shareRequest{Target: "3000", Name: "public-attempt", Ephemeral: true, Funnel: true},
			service: registry.Service{Name: "public-attempt", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true},
			want:    registry.CodeFunnelPublicAckRequired,
		},
		{
			name:    "funnel with an allow-list",
			request: shareRequest{Target: "3000", Name: "public-attempt", Ephemeral: true, Funnel: true, PublicAck: true, Allow: []string{"person@example.com"}},
			service: registry.Service{Name: "public-attempt", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, AllowedUsers: []string{"person@example.com"}},
			want:    registry.CodeFunnelAllowConflict,
		},
		{
			name:    "funnel on a file share",
			request: shareRequest{Target: t.TempDir(), Name: "public-dir", Ephemeral: true, Funnel: true, PublicAck: true},
			service: registry.Service{Name: "public-dir", Type: registry.TypeFile, Path: t.TempDir(), Funnel: true, PublicAck: true},
			want:    registry.CodeFunnelTypeConflict,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restoreShareSeams(t)
			paths := mcpSharePaths(t)
			shareIsRunningFn = func(string) bool { return true }
			shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
				return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
			}
			_, err := defaultMCPActions(paths, io.Discard).share(context.Background(), tc.request)
			if err == nil {
				t.Fatal("share was accepted")
			}
			code, ok := registry.ErrorCode(err)
			if !ok || code != tc.want {
				t.Fatalf("share error = %v (code %q), want %q", err, code, tc.want)
			}
			domainErr := registry.ValidateService(tc.service)
			domainCode, domainOK := registry.ErrorCode(domainErr)
			if !domainOK || domainCode != tc.want {
				t.Fatalf("registry.ValidateService returned %v (code %q); the tool must be refused by this same rule", domainErr, domainCode)
			}
			if reg, loadErr := registry.Load(paths.Registry); loadErr != nil || len(reg.Services) != 0 {
				t.Fatalf("rejected share left registry %+v (load error %v)", reg, loadErr)
			}
		})
	}
}

// TestMCPShareRejectsFunnelOptionsWithoutFunnel covers the two options that are
// meaningless without funnel. buildService applies the same rule to
// `tslink add`, through the same helper.
func TestMCPShareRejectsFunnelOptionsWithoutFunnel(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	actions := defaultMCPActions(paths, io.Discard)
	for _, tc := range []struct {
		name    string
		request shareRequest
		want    string
	}{
		{"public_ack", shareRequest{Target: "3000", Ephemeral: true, PublicAck: true}, "public_ack can only be used with funnel"},
		{"funnel_ttl", shareRequest{Target: "3000", Ephemeral: true, FunnelTTL: "1h", FunnelTTLSet: true}, "funnel_ttl can only be used with funnel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := actions.share(context.Background(), tc.request)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("share error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestMCPShareRejectsMalformedPrincipalsAndTags checks that the allow and tags
// parameters are validated by the same registry.ValidateTag the `tslink add`
// flags go through, before anything is written.
func TestMCPShareRejectsMalformedPrincipalsAndTags(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	actions := defaultMCPActions(paths, io.Discard)
	for _, tc := range []struct {
		name    string
		request shareRequest
	}{
		{"allow tag without a valid name", shareRequest{Target: "3000", Ephemeral: true, Allow: []string{"tag:NotLower"}}},
		{"node tag without the prefix", shareRequest{Target: "3000", Ephemeral: true, Tags: []string{"web"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := actions.share(context.Background(), tc.request)
			if err == nil {
				t.Fatal("share was accepted")
			}
			if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeInvalidTag {
				t.Fatalf("share error = %v (code %q), want %q", err, code, registry.CodeInvalidTag)
			}
			if reg, loadErr := registry.Load(paths.Registry); loadErr != nil || len(reg.Services) != 0 {
				t.Fatalf("rejected share left registry %+v (load error %v)", reg, loadErr)
			}
		})
	}
}

func TestMCPShareFunnelTTLReachesTheRegistry(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
	}
	before := time.Now().UTC()
	if _, err := defaultMCPActions(paths, io.Discard).share(context.Background(), shareRequest{
		Target: "3000", Name: "publicshare", Ephemeral: true,
		Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true,
	}); err != nil {
		t.Fatalf("public share: %v", err)
	}
	svc := mcpLoadService(t, paths.Registry, "publicshare")
	if !svc.Funnel || !svc.PublicAck || svc.FunnelExpiresAt == nil {
		t.Fatalf("persisted public share = %+v", svc)
	}
	if delta := svc.FunnelExpiresAt.Sub(before); delta < 59*time.Minute || delta > 61*time.Minute {
		t.Fatalf("funnel_ttl 1h produced expiry %v after %v", svc.FunnelExpiresAt, before)
	}

	// "never" must persist no deadline at all rather than a far-future one.
	if _, err := defaultMCPActions(paths, io.Discard).share(context.Background(), shareRequest{
		Target: "3001", Name: "forever", Ephemeral: true,
		Funnel: true, PublicAck: true, FunnelTTL: "never", FunnelTTLSet: true,
	}); err != nil {
		t.Fatalf("never share: %v", err)
	}
	if svc := mcpLoadService(t, paths.Registry, "forever"); svc.FunnelExpiresAt != nil {
		t.Fatalf("funnel_ttl never persisted expiry %v", svc.FunnelExpiresAt)
	}
}

// --- add -----------------------------------------------------------------

func TestMCPAddArgumentTranslation(t *testing.T) {
	ttl := "7d"
	cases := []struct {
		name      string
		args      mcpAddArguments
		wantErr   string
		wantCode  string
		check     func(*testing.T, AddParams, bool)
		wantPanic bool
	}{
		{
			name: "proxy",
			args: mcpAddArguments{Name: "web", Type: registry.TypeProxy, Target: "localhost:3000", Allow: []string{"a@example.com", "tag:ops"}, Tags: []string{"tag:web", "tag:internal"}},
			check: func(t *testing.T, params AddParams, preserve bool) {
				if params.Proxy != "localhost:3000" || params.Dir != "" || params.TCP != "" {
					t.Fatalf("params = %+v", params)
				}
				if params.Allow != "a@example.com,tag:ops" || params.Tags != "tag:web,tag:internal" {
					t.Fatalf("joined params = %+v", params)
				}
				if !preserve {
					t.Fatal("absent funnel_ttl must preserve an existing entry's deadline")
				}
			},
		},
		{
			name: "file",
			args: mcpAddArguments{Name: "docs", Type: registry.TypeFile, Dir: "/tmp/docs"},
			check: func(t *testing.T, params AddParams, _ bool) {
				if params.Dir != "/tmp/docs" || params.Proxy != "" || params.TCP != "" {
					t.Fatalf("params = %+v", params)
				}
			},
		},
		{
			name: "tcp",
			args: mcpAddArguments{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432"},
			check: func(t *testing.T, params AddParams, _ bool) {
				if params.TCP != "localhost:5432" {
					t.Fatalf("params = %+v", params)
				}
			},
		},
		{
			name: "explicit funnel ttl stops preserving",
			args: mcpAddArguments{Name: "web", Type: registry.TypeProxy, Target: "localhost:3000", Funnel: true, PublicAck: true, FunnelTTL: &ttl},
			check: func(t *testing.T, params AddParams, preserve bool) {
				if params.FunnelTTL != "7d" || !params.FunnelTTLSet || preserve {
					t.Fatalf("params = %+v preserve=%v", params, preserve)
				}
			},
		},
		{"proxy without target", mcpAddArguments{Name: "web", Type: registry.TypeProxy}, "target is required for proxy type", "", nil, false},
		{"proxy with dir", mcpAddArguments{Name: "web", Type: registry.TypeProxy, Target: "localhost:3000", Dir: "/tmp"}, "dir is not supported for proxy type", "", nil, false},
		{"file without dir", mcpAddArguments{Name: "docs", Type: registry.TypeFile}, "dir is required for file type", "", nil, false},
		{"file with target", mcpAddArguments{Name: "docs", Type: registry.TypeFile, Dir: "/tmp", Target: "localhost:1"}, "target is not supported for file type", "", nil, false},
		{"tcp without target", mcpAddArguments{Name: "db", Type: registry.TypeTCP}, "target is required for tcp type", "", nil, false},
		{"tcp with dir", mcpAddArguments{Name: "db", Type: registry.TypeTCP, Target: "localhost:5432", Dir: "/tmp"}, "dir is not supported for tcp type", "", nil, false},
		{"unknown type", mcpAddArguments{Name: "web", Type: "vpn"}, "", registry.CodeServiceTypeAmbiguous, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, preserve, err := addParamsFromMCPArguments(tc.args)
			switch {
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
			case tc.wantCode != "":
				code, ok := registry.ErrorCode(err)
				if !ok || code != tc.wantCode {
					t.Fatalf("error = %v (code %q), want %q", err, code, tc.wantCode)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if params.Name != tc.args.Name {
					t.Fatalf("params.Name = %q", params.Name)
				}
				tc.check(t, params, preserve)
			}
		})
	}
}

// TestMCPAddSharesTheCLIAdmissionGate runs the MCP add action and `tslink add`'s
// own path over the same params and requires the same verdict, because both
// call buildService and resolveAddService.
func TestMCPAddSharesTheCLIAdmissionGate(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	actions := defaultMCPActions(paths, io.Discard)

	value, err := actions.add(context.Background(), AddParams{Name: "web", Proxy: "localhost:3000", Allow: "person@example.com"}, true)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	result, ok := value.(AddResult)
	if !ok || result.Name != "web" || !result.Created || !result.URLPending {
		t.Fatalf("add result = %T(%+v)", value, value)
	}
	svc := mcpLoadService(t, paths.Registry, "web")
	if len(svc.AllowedUsers) != 1 || svc.AllowedUsers[0] != "person@example.com" {
		t.Fatalf("persisted allow-list = %v", svc.AllowedUsers)
	}

	// A rejected add must be rejected by the same domain rule the CLI meets,
	// and must leave the registry alone.
	rejected := []struct {
		name   string
		params AddParams
		want   string
	}{
		{"funnel without public", AddParams{Name: "pub", Proxy: "localhost:3000", Funnel: true}, registry.CodeFunnelPublicAckRequired},
		{"allow on tcp", AddParams{Name: "db", TCP: "localhost:5432", Allow: "person@example.com"}, registry.CodeAllowUnsupportedTCP},
		{"relative dir", AddParams{Name: "docs", Dir: "relative/path"}, registry.CodePathMustBeAbsolute},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := actions.add(context.Background(), tc.params, true); err == nil {
				t.Fatal("add was accepted")
			} else if code, ok := registry.ErrorCode(err); !ok || code != tc.want {
				t.Fatalf("add error = %v (code %q), want %q", err, code, tc.want)
			}
			reg, loadErr := registry.Load(paths.Registry)
			if loadErr != nil {
				t.Fatalf("load registry: %v", loadErr)
			}
			for _, svc := range reg.Services {
				if svc.Name == tc.params.Name {
					t.Fatalf("rejected add persisted %+v", svc)
				}
			}
		})
	}
}

// --- the remaining local read/write tools --------------------------------

func TestMCPLocalToolsReadAndWriteTheGivenRegistry(t *testing.T) {
	restoreShareSeams(t)
	// The doctor action below reads Tailscale SSH through this seam.
	stubDoctorTailscaleSSH(t, false, nil)
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	actions := defaultMCPActions(paths, io.Discard)

	tagsValue, err := actions.tagsList()
	if err != nil {
		t.Fatalf("tags_list: %v", err)
	}
	tags := tagsValue.(TagsListResult)
	if len(tags.Services) != 1 || tags.Services[0].Name != "web" || tags.Services[0].Tags[0] != "tag:tsmain" {
		t.Fatalf("tags_list = %+v", tags)
	}

	setValue, err := actions.tagsSet("web", "tag:replaced")
	if err != nil {
		t.Fatalf("tags_set: %v", err)
	}
	if set := setValue.(TagsSetResult); set.Service != "web" || len(set.Tags) != 1 || set.Tags[0] != "tag:replaced" {
		t.Fatalf("tags_set = %+v", set)
	}
	if svc := mcpLoadService(t, paths.Registry, "web"); len(svc.Tags) != 1 || svc.Tags[0] != "tag:replaced" {
		t.Fatalf("persisted tags = %v", svc.Tags)
	}
	if _, err := actions.tagsSet("absent", "tag:x"); err == nil {
		t.Fatal("tags_set on an absent service was accepted")
	} else if failure := output.NewFailureForError("", err); failure.Code != output.ExitNotFound || failure.Error.Code != output.StableErrorCode(output.ExitNotFound) {
		t.Fatalf("tags_set error = %v, envelope = %+v; want the not_found code the CLI --json path reports", err, failure.Error)
	}
	if _, err := actions.tagsSet("web", "not-a-tag"); err == nil {
		t.Fatal("tags_set accepted a tag without the tag: prefix")
	}

	explainValue, err := actions.accessExplain("web")
	if err != nil {
		t.Fatalf("access_explain: %v", err)
	}
	explain := explainValue.(AccessExplainResult)
	if explain.Service != "web" || explain.TSLinkLocalEnforcement.Kind != "no_local_allow_list" {
		t.Fatalf("access_explain = %+v", explain)
	}
	if _, err := actions.accessExplain("absent"); err == nil {
		t.Fatal("access_explain on an absent service was accepted")
	}

	doctorValue, err := actions.doctor(false)
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	doctorResult := doctorValue.(DoctorResult)
	if doctorResult.ExecutionStatus != doctorExecutionCompleted || doctorResult.Paths.Registry != paths.Registry {
		t.Fatalf("doctor = %+v", doctorResult)
	}
	if doctorResult.Counts.Services != 1 {
		t.Fatalf("doctor counted %d services", doctorResult.Counts.Services)
	}

	urlValue, err := actions.url(context.Background(), "web", 0)
	if err == nil {
		t.Fatalf("url returned %+v with no runtime evidence", urlValue)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
		t.Fatalf("url error = %v (code %q), want %q", err, code, registry.CodeURLNotReady)
	}

	templatesValue, err := actions.templateList()
	if err != nil {
		t.Fatalf("template_list: %v", err)
	}
	templates := templatesValue.(TemplateListResult)
	if templates.Count == 0 || templates.Count != len(templates.Templates) {
		t.Fatalf("template_list = %+v", templates)
	}

	planValue, err := actions.templatePlan("local-web")
	if err != nil {
		t.Fatalf("template_plan: %v", err)
	}
	plan := planValue.(TemplateApplyResult)
	if !plan.DryRun || plan.Applied || plan.Created == 0 {
		t.Fatalf("template_plan = %+v", plan)
	}
	if reg, loadErr := registry.Load(paths.Registry); loadErr != nil || len(reg.Services) != 1 {
		t.Fatalf("template_plan wrote the registry: %+v (load error %v)", reg, loadErr)
	}

	applyValue, err := actions.templateApply(context.Background(), "local-web", true)
	if err != nil {
		t.Fatalf("template_apply: %v", err)
	}
	apply := applyValue.(TemplateApplyResult)
	if apply.DryRun || !apply.Applied || apply.Created != plan.Created {
		t.Fatalf("template_apply = %+v", apply)
	}
	if reg, loadErr := registry.Load(paths.Registry); loadErr != nil || len(reg.Services) != 1+apply.Created {
		t.Fatalf("template_apply persisted %+v (load error %v)", reg, loadErr)
	}
	if _, err := actions.templatePlan("no-such-template"); err == nil {
		t.Fatal("template_plan accepted an unknown template")
	}
	if _, err := actions.templateApply(context.Background(), "no-such-template", true); err == nil {
		t.Fatal("template_apply accepted an unknown template")
	}
}

func TestMCPURLWaitParsing(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{{"", 0}, {"0s", 0}, {"30s", 30 * time.Second}, {"2m", 2 * time.Minute}} {
		got, err := parseMCPWait(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("parseMCPWait(%q) = %v, %v; want %v", tc.raw, got, err, tc.want)
		}
	}
	if _, err := parseMCPWait("soon"); err == nil || !strings.Contains(err.Error(), "invalid wait duration") {
		t.Fatalf("parseMCPWait(\"soon\") error = %v", err)
	}
}

func TestMCPURLActionResolvesRuntimeEvidence(t *testing.T) {
	restoreShareSeams(t)
	withStatusURLSeams(t, false, 0, time.Time{})
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	value, err := defaultMCPActions(paths, io.Discard).url(context.Background(), "web", 0)
	if err == nil {
		t.Fatalf("url returned %+v without an exact snapshot", value)
	}
	if _, err := defaultMCPActions(paths, io.Discard).url(context.Background(), "Bad_Name", 0); err == nil {
		t.Fatal("url accepted an invalid service name")
	}
}

// --- invite tools --------------------------------------------------------

// The invite tools are the ones with real external effect. They are exercised
// only through the package's invite*Fn seams; no Tailscale API call is made.
func TestMCPInviteToolsUseTheCLIInviteFunctions(t *testing.T) {
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	oldUser, oldDevice, oldList, oldRevoke, oldResend := inviteCreateUserFn, inviteCreateDeviceFn, inviteListFn, inviteRevokeFn, inviteResendFn
	t.Cleanup(func() {
		inviteCreateUserFn, inviteCreateDeviceFn, inviteListFn, inviteRevokeFn, inviteResendFn = oldUser, oldDevice, oldList, oldRevoke, oldResend
	})

	var gotRole string
	var gotPrintLink bool
	inviteCreateUserFn = func(_ context.Context, email, role string, printLink bool) (tailapi.Invite, error) {
		gotRole, gotPrintLink = role, printLink
		return tailapi.Invite{Kind: tailapi.InviteKindUser, ID: "10", Email: email, Role: role, Emailed: !printLink}, nil
	}
	var gotTarget tailapi.DeviceTarget
	var gotMultiUse, gotExitNode bool
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, email string, printLink, multiUse, allowExitNode bool) (tailapi.Invite, error) {
		gotTarget, gotMultiUse, gotExitNode = target, multiUse, allowExitNode
		return tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "11", Email: email, Service: target.Service, Emailed: !printLink}, nil
	}
	inviteListFn = func(_ context.Context, targets []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{
			Complete:      true,
			Count:         1,
			UserInvites:   []tailapi.Invite{{Kind: tailapi.InviteKindUser, ID: "10", Email: "a@example.com", InviteURL: "https://login.tailscale.com/admin/invite/secret"}},
			DeviceInvites: []tailapi.Invite{},
			DeviceTargets: []tailapi.InviteTargetStatus{{Service: targets[0].Service, Checked: true}},
		}, nil
	}
	inviteRevokeFn = func(_ context.Context, kind, id string, _ []tailapi.DeviceTarget) (tailapi.Invite, error) {
		return tailapi.Invite{Kind: kind, ID: id}, nil
	}
	inviteResendFn = func(_ context.Context, kind, id string, _ []tailapi.DeviceTarget) (tailapi.Invite, error) {
		return tailapi.Invite{Kind: kind, ID: id, Email: "a@example.com", Emailed: true}, nil
	}

	actions := defaultMCPActions(paths, io.Discard)

	userValue, err := actions.inviteUser(context.Background(), "a@example.com", "", false)
	if err != nil {
		t.Fatalf("invite_user: %v", err)
	}
	if gotRole != tailapi.InviteRoleMember || gotPrintLink {
		t.Fatalf("invite_user forwarded role=%q print_link=%v", gotRole, gotPrintLink)
	}
	if created := userValue.(InviteMutationResult); created.ID != "10" || created.RemoteSideEffectPlan.Operation != "create_user_invite" || !created.RemoteSideEffectPlan.Mutates {
		t.Fatalf("invite_user = %+v", created)
	}
	if _, err := actions.inviteUser(context.Background(), "a@example.com", "sysadmin", false); err == nil {
		t.Fatal("invite_user accepted an unknown role")
	}
	if _, err := actions.inviteUser(context.Background(), "   ", "", false); err == nil {
		t.Fatal("invite_user accepted a blank email")
	}

	deviceValue, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "web", Email: "a@example.com", MultiUse: true, AllowExitNode: true})
	if err != nil {
		t.Fatalf("invite_device: %v", err)
	}
	if gotTarget.Service != "web" || !gotMultiUse || !gotExitNode {
		t.Fatalf("invite_device forwarded target=%+v multi_use=%v exit_node=%v", gotTarget, gotMultiUse, gotExitNode)
	}
	if created := deviceValue.(InviteMutationResult); created.Service != "web" || created.RemoteSideEffectPlan.RemoteSystem != "tailscale_device_invites" {
		t.Fatalf("invite_device = %+v", created)
	}
	if _, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "absent", Email: "a@example.com"}); err == nil {
		t.Fatal("invite_device accepted an unregistered service")
	}
	if _, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "", Email: "a@example.com"}); err == nil {
		t.Fatal("invite_device accepted a blank service")
	}
	if _, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "web", Email: " "}); err == nil {
		t.Fatal("invite_device accepted a blank email")
	}

	redacted, err := actions.inviteList(context.Background(), false)
	if err != nil {
		t.Fatalf("invite_list: %v", err)
	}
	if url := redacted.(tailapi.InviteList).UserInvites[0].InviteURL; url != "" {
		t.Fatalf("invite_list leaked a bearer URL by default: %q", url)
	}
	shown, err := actions.inviteList(context.Background(), true)
	if err != nil {
		t.Fatalf("invite_list show_urls: %v", err)
	}
	if url := shown.(tailapi.InviteList).UserInvites[0].InviteURL; url == "" {
		t.Fatal("invite_list show_urls withheld the bearer URL")
	}

	revoked, err := actions.inviteRevoke(context.Background(), tailapi.InviteKindUser, "10")
	if err != nil {
		t.Fatalf("invite_revoke: %v", err)
	}
	if result := revoked.(InviteRevokeResult); !result.Revoked || result.RemoteSideEffectPlan.Operation != "revoke_user_invite" {
		t.Fatalf("invite_revoke = %+v", result)
	}
	if _, err := actions.inviteRevoke(context.Background(), "group", "10"); err == nil {
		t.Fatal("invite_revoke accepted an unknown kind")
	}
	if _, err := actions.inviteRevoke(context.Background(), tailapi.InviteKindUser, "not an id"); err == nil {
		t.Fatal("invite_revoke accepted a malformed id")
	}

	resent, err := actions.inviteResend(context.Background(), tailapi.InviteKindDevice, "11")
	if err != nil {
		t.Fatalf("invite_resend: %v", err)
	}
	if result := resent.(InviteResendResult); !result.Emailed || result.RemoteSideEffectPlan.Operation != "resend_device_invite" {
		t.Fatalf("invite_resend = %+v", result)
	}
	if _, err := actions.inviteResend(context.Background(), "group", "11"); err == nil {
		t.Fatal("invite_resend accepted an unknown kind")
	}
	if _, err := actions.inviteResend(context.Background(), tailapi.InviteKindUser, "not an id"); err == nil {
		t.Fatal("invite_resend accepted a malformed id")
	}
}

// --- output schemas ------------------------------------------------------

// TestMCPToolOutputSchemasAcceptRealPayloads marshals the value each action
// actually returns and validates it against the declared outputSchema. The
// schemas are closed at the top level, so a field added to one of these structs
// without a schema update fails here rather than in a client.
func TestMCPToolOutputSchemasAcceptRealPayloads(t *testing.T) {
	stubDoctorTailscaleSSH(t, false, nil)
	invite := tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "7", Recipient: "a@example.com", Email: "a@example.com", Emailed: true, Service: "web", DeviceID: 42, MultiUse: true, AllowExitNode: true, Accepted: false, Created: "2026-01-01T00:00:00Z", LastEmailSentAt: "2026-01-02T00:00:00Z", InviteURL: "https://example.invalid/i", Role: tailapi.InviteRoleMember}
	svc := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}, AllowedUsers: []string{"person@example.com"}}
	view := inspect.ServiceViewFor(svc)

	cases := []struct {
		tool  string
		value any
	}{
		{"add", AddResult{
			Name: "web", Type: registry.TypeProxy, Created: true, FunnelRearmed: false, URLPending: true,
			Endpoint: view.Endpoint, Exposure: view.Exposure,
			Warnings: []inspect.WarningView{{Code: "invalid_allow_entry", Severity: "warning", Message: "bad", Source: "cmd.add"}},
		}},
		{"url", URLResult{Name: "web", URL: "https://web.tail.ts.net", State: inspect.EndpointStateExact}},
		{"tags_list", TagsListResult{Services: []TagsServiceEntry{{Name: "web", Tags: []string{"tag:tsmain"}}, {Name: "bare", Tags: nil}}}},
		{"tags_set", TagsSetResult{Service: "web", Tags: []string{"tag:tsmain"}}},
		{"access_explain", buildAccessExplainResult(svc)},
		{"doctor", buildDoctorResult(doctorOptions{RegistryPath: filepath.Join(t.TempDir(), "registry.json")})},
		{"invite_user", InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}},
		{"invite_device", InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}},
		{"invite_list", tailapi.InviteList{
			Complete: false, Count: 1,
			UserInvites:   []tailapi.Invite{invite},
			DeviceInvites: []tailapi.Invite{},
			DeviceTargets: []tailapi.InviteTargetStatus{
				{Service: "web", Checked: true, InviteCount: 1},
				{Service: "docs", Checked: false, Error: &tailapi.InviteTargetError{Code: "auth_error", Message: "no client", Next: []string{"tslink login"}}},
			},
		}},
		{"invite_revoke", InviteRevokeResult{Kind: tailapi.InviteKindDevice, ID: "7", Service: "web", Revoked: true, RemoteSideEffectPlan: invitePlan(invite, "revoke")}},
		{"invite_resend", inviteResendResult(invite)},
		{"template_list", listTemplatesResult()},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			validateAgainstToolOutputSchema(t, tc.tool, tc.value)
		})
	}

	t.Run("template_plan and template_apply", func(t *testing.T) {
		regPath := filepath.Join(t.TempDir(), "registry.json")
		plan, err := applyTemplate("dev-suite", regPath, true)
		if err != nil {
			t.Fatalf("template plan: %v", err)
		}
		validateAgainstToolOutputSchema(t, "template_plan", plan)
		applied, err := applyTemplate("dev-suite", regPath, false)
		if err != nil {
			t.Fatalf("template apply: %v", err)
		}
		validateAgainstToolOutputSchema(t, "template_apply", applied)
		reapplied, err := applyTemplate("dev-suite", regPath, false)
		if err != nil {
			t.Fatalf("template reapply: %v", err)
		}
		if reapplied.Skipped != len(reapplied.Services) {
			t.Fatalf("re-apply = %+v, want every service skipped", reapplied)
		}
		validateAgainstToolOutputSchema(t, "template_apply", reapplied)
	})
}

func validateAgainstToolOutputSchema(t *testing.T, tool string, value any) {
	t.Helper()
	wire, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", tool, err)
	}
	var payload any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatalf("decode %s payload: %v", tool, err)
	}
	if err := validateMCPJSONSchema(mcpToolByName(t, tool).OutputSchema, payload, "$"); err != nil {
		t.Fatalf("%s payload=%s schema error: %v", tool, wire, err)
	}
}

// --- protocol wiring for the new tools -----------------------------------

func TestMCPNewToolsAreReachableOverTheProtocol(t *testing.T) {
	calls := []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"web","no_daemon_install":true,"type":"proxy","target":"localhost:3000","allow":["a@example.com"],"tags":["tag:web"],"ephemeral":true}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"0s"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tags_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tags_set","arguments":{"service":"web","tag":"tag:web"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"access_explain","arguments":{"service":"web"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"doctor","arguments":{"probe_external":false}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"invite_user","arguments":{"email":"a@example.com","role":"member","print_link":true}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"invite_device","arguments":{"service":"web","email":"a@example.com","print_link":true,"multi_use":true,"allow_exit_node":true}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"invite_list","arguments":{"show_urls":true}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"invite_revoke","arguments":{"kind":"user","invite_id":"1"}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"invite_resend","arguments":{"kind":"user","invite_id":"1"}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"template_list","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"template_plan","arguments":{"name":"local-web"}}}`,
		`{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"template_apply","arguments":{"name":"local-web","no_daemon_install":true}}}`,
		`{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","allow":["a@example.com"],"tags":["tag:web"],"funnel":false,"public_ack":false}}}`,
	}
	stdout := runMCPSession(t, initializedMCPInput(strings.Join(calls, "\n")), fakeMCPActions())
	frames := decodeMCPResponses(t, stdout)
	if len(frames) != len(calls)+1 {
		t.Fatalf("frames = %d, want %d: %s", len(frames), len(calls)+1, stdout)
	}
	for _, frame := range frames[1:] {
		if frame["error"] != nil {
			t.Fatalf("tool call returned a protocol error: %+v", frame)
		}
		result := frame["result"].(map[string]any)
		if result["isError"] != nil {
			t.Fatalf("tool call returned an execution error: %+v", result)
		}
		if _, ok := result["structuredContent"].(map[string]any); !ok {
			t.Fatalf("tool call returned no structured content: %+v", result)
		}
	}
}

func TestMCPNewToolsRejectMalformedArguments(t *testing.T) {
	cases := []struct {
		name string
		call string
	}{
		{"add without name", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"type":"proxy","target":"localhost:3000"}}}`},
		{"add without type", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"web","target":"localhost:3000"}}}`},
		{"add unknown field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"web","type":"proxy","target":"x","extra":1}}}`},
		{"url without name", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{}}}`},
		{"url unknown field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","extra":1}}}`},
		{"tags_list arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tags_list","arguments":{"extra":1}}}`},
		{"tags_set without tag", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tags_set","arguments":{"service":"web"}}}`},
		{"tags_set without service", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tags_set","arguments":{"tag":"tag:web"}}}`},
		{"access_explain without service", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"access_explain","arguments":{}}}`},
		{"doctor unknown field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"doctor","arguments":{"probe_remote":true}}}`},
		{"invite_user without email", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invite_user","arguments":{"role":"member"}}}`},
		{"invite_device without email", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invite_device","arguments":{"service":"web"}}}`},
		{"invite_list unknown field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invite_list","arguments":{"extra":1}}}`},
		{"invite_revoke without kind", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invite_revoke","arguments":{"invite_id":"1"}}}`},
		{"invite_resend without id", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invite_resend","arguments":{"kind":"user"}}}`},
		{"template_list arguments", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"template_list","arguments":{"name":"x"}}}`},
		{"template_plan without name", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"template_plan","arguments":{}}}`},
		{"template_apply without name", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"template_apply","arguments":{}}}`},
		{"share unknown exposure field", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":{"target":"3000","public":true}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(tc.call), fakeMCPActions()))
			last := frames[len(frames)-1]
			errorObject, ok := last["error"].(map[string]any)
			if !ok || errorObject["code"] != float64(-32602) {
				t.Fatalf("response = %+v, want an invalid-params protocol error", last)
			}
		})
	}
}

// TestMCPToolExecutionErrorsSurviveAsToolResults keeps translation failures out
// of the protocol error channel: an agent must see them as a tool result it can
// act on, with the CLI's stable error code.
func TestMCPToolExecutionErrorsSurviveAsToolResults(t *testing.T) {
	cases := []struct {
		name     string
		call     string
		wantCode string
	}{
		{"add with an unknown type", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"web","type":"vpn"}}}`, registry.CodeServiceTypeAmbiguous},
		{"add proxy without target", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add","arguments":{"name":"web","type":"proxy"}}}`, output.StableErrorCode(output.ExitUsage)},
		{"url with a bad wait", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"url","arguments":{"name":"web","wait":"soon"}}}`, output.StableErrorCode(output.ExitUsage)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames := decodeMCPResponses(t, runMCPSession(t, initializedMCPInput(tc.call), fakeMCPActions()))
			last := frames[len(frames)-1]
			if last["error"] != nil {
				t.Fatalf("execution failure became a protocol error: %+v", last)
			}
			result := last["result"].(map[string]any)
			if result["isError"] != true {
				t.Fatalf("result = %+v, want isError", result)
			}
			structured := result["structuredContent"].(map[string]any)
			errorObject := structured["error"].(map[string]any)
			if errorObject["code"] != tc.wantCode {
				t.Fatalf("error code = %v, want %q", errorObject["code"], tc.wantCode)
			}
		})
	}
}
