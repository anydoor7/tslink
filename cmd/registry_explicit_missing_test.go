package cmd

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

func TestRegistryCheckExplicitMissingAndImplicitFirstRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	check, _, err := rootCmd.Find([]string{"registry", "check"})
	if err != nil || check == nil {
		t.Fatalf("find registry check: %v", err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().Bool("json", false, "")
	cmd.SetOut(io.Discard)
	if err := check.RunE(cmd, nil); err != nil {
		t.Fatalf("implicit first run: %v", err)
	}
	for _, path := range []string{filepath.Join(dir, "backup.json"), filepath.Join(dir, "registry.json")} {
		err := check.RunE(cmd, []string{path})
		if output.ExitCode(err) != output.ExitNotFound || !strings.Contains(err.Error(), path) {
			t.Fatalf("explicit missing file %s: exit=%d err=%v; want exit 5 naming path", path, output.ExitCode(err), err)
		}
	}
}
