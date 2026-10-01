package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
)

func TestMalformedRegistryLoadReportsPathAndUsage(t *testing.T) {
	for name, raw := range map[string]string{"syntax": "{", "type": `{"services":"wrong"}`, "trailing": `{"services":[]} {}`} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			pid, snapshot := filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json")
			for command, read := range map[string]func() error{
				"registry check": func() error { _, err := registryCheck(path); return err },
				"list":           func() error { _, err := loadListResultForPaths(path, pid, snapshot, listOptions{}); return err },
				"status":         func() error { _, err := getStatus(pid, path); return err },
				"status --urls":  func() error { _, err := getStatusURLs(pid, path, snapshot); return err },
			} {
				t.Run(command, func(t *testing.T) {
					err := read()
					result := output.NewFailureForError(command, err)
					if output.ExitCode(err) != output.ExitUsage || result.Error == nil || result.Error.Code != "usage_error" || !strings.Contains(result.Error.Message, path) {
						t.Fatalf("invalid registry must be usage-class with path: exit=%d error=%+v", output.ExitCode(err), result.Error)
					}
					next := strings.Join(result.Error.Next, " ")
					if !strings.Contains(next, "repair") || !strings.Contains(next, "registry check") {
						t.Fatalf("missing check/repair guidance: %q", next)
					}
				})
			}
		})
	}
}
