package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func TestMCPScopeConfigStrictAndLegacy(t *testing.T) {
	t.Setenv(ConfigDirEnv, t.TempDir())
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"mcp":{"bindings":[{"principal":"agent","role":"custom","apps":["photos"]}]}}`,
		`{"mcp":{"bindings":[{"principal":"agent","role":"viewer","apps":["photos"],"tools":["add"]}]}}`,
		`{"mcp":{"bindings":[{"principal":"agent","role":"people-manager","apps":["photos"],"max_duration":"never"}]}}`,
		`{"mcp":{"bindings":[{"principal":"agent","role":"viewer","apps":["all"]}]}}`,
		`{"mcp":{"allow":["agent"],"bindings":[{"principal":"agent","role":"viewer","apps":["photos"]}]}}`,
		`{"mcp":{"bindings":[{"principal":"agent","role":"viewer","apps":["photos"],"for":"1h"}]}}`,
		`{"mcp":{"bindings":[{"principal":"agent","role":"viewer","apps":["photos"],"expires_at":"bad"}]}}`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadGlobalConfig(); err == nil {
			t.Fatal("invalid scope config accepted", raw)
		}
	}
	legacy := GlobalConfig{MCP: &MCPConfig{Allow: []string{"owner@example.com"}}}
	if err := SaveGlobalConfig(legacy); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadGlobalConfig()
	if err != nil || len(cfg.MCP.Allow) != 1 || len(cfg.MCP.Bindings) != 0 {
		t.Fatal(cfg, err)
	}
	b := mcpscope.Binding{Principal: "agent", Scope: mcpscope.Scope{Role: "viewer", Apps: []string{"photos"}}}
	cfg.MCP.Bindings = []mcpscope.Binding{b}
	if err := SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadGlobalConfig()
	if err != nil || cfg.MCP.Bindings[0].Role != "viewer" {
		t.Fatal(cfg, err)
	}
	cfg.MCP.Bindings[0].Role = "invalid"
	if err := SaveGlobalConfig(cfg); err == nil {
		t.Fatal("saved invalid binding")
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil || len(data) == 0 {
		t.Fatal(err)
	}
}
