package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
)

func TestMalformedRegistryLoadReportsPathAndUsage(t *testing.T) {
	for name, raw := range map[string]string{"syntax": "{", "type": `{"services":"wrong"}`, "trailing": `{"services":[]} {}`} {
		t.Run(name, func(t *testing.T) {
			// A backslash also exercises Windows path quoting on Unix runners.
			dir := filepath.Join(t.TempDir(), `registry\fixture`)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			pid, snapshot := filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json")
			for command, read := range map[string]func() error{
				"registry check": func() error { _, err := registryCheck(path); return err },
				"list": func() error {
					_, err := loadListResultForPaths(context.Background(), path, pid, snapshot, listOptions{})
					return err
				},
				"status":        func() error { _, err := getStatus(context.Background(), pid, path); return err },
				"status --urls": func() error { _, err := getStatusURLs(context.Background(), pid, path, snapshot); return err },
			} {
				t.Run(command, func(t *testing.T) {
					err := read()
					result := output.NewFailureForError(command, err)
					if output.ExitCode(err) != output.ExitUsage || result.Error == nil || result.Error.Code != "usage_error" || !strings.Contains(result.Error.Message, strconv.Quote(path)) {
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
