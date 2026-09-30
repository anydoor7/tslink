package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
)

func TestStatusEmptyConfigurationSuggestsAddOrShare(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprintf("running=%v", running), func(t *testing.T) {
			t.Setenv(config.ConfigDirEnv, t.TempDir())
			var out bytes.Buffer
			formatStatus(StatusResult{DaemonRunning: running, AuthStatus: authStatusNotAuthenticated}, &out)
			for _, want := range []string{"tslink add", "tslink share", "no service nodes configured"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("empty status missing %q: %s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "tslink install") || strings.Contains(out.String(), "obtain the login URL") {
				t.Fatalf("empty status must not promise enrollment after install: %s", out.String())
			}
		})
	}
}

func TestStatusEmptyRegistryWithMCPStillExplainsEnrollment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"mcp":{"enabled":true,"allow":["alice@example.com"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	formatStatus(StatusResult{AuthStatus: authStatusNotAuthenticated}, &out)
	if !strings.Contains(out.String(), "obtain the login URL") {
		t.Fatalf("MCP node still needs enrollment: %s", out.String())
	}
	out.Reset()
	formatStatus(StatusResult{AuthStatus: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/pending"}, &out)
	if !strings.Contains(out.String(), "login URL: https://login.tailscale.com/a/pending") {
		t.Fatalf("pending enrollment lost its URL: %s", out.String())
	}
}
