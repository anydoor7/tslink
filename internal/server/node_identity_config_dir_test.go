package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
)

// TestNodeIdentityPathComesFromConfig pins one name for the node-identities
// directory: internal/config owns it (NodeIdentitiesDirIn), as it owns
// nodes/, so the daemon and the CLI readers cannot drift apart. The daemon's
// own paths are built from it, and no product file outside internal/config
// spells the directory name.
func TestNodeIdentityPathComesFromConfig(t *testing.T) {
	cfgDir := t.TempDir()
	s := &Server{cfgDir: cfgDir}
	got, err := s.nodeIdentityPath("app")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(config.NodeIdentitiesDirIn(cfgDir), "app.json"); got != want {
		t.Fatalf("nodeIdentityPath = %q, want %q", got, want)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var spelled []string
	seen := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), `"node-identities"`) {
			seen++
			rel, _ := filepath.Rel(root, path)
			if filepath.ToSlash(rel) != "internal/config/config.go" {
				spelled = append(spelled, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Control: the scan finds the one legitimate spelling.
	if seen == 0 {
		t.Fatal("scan found no spelling of the directory name at all; the probe is blind")
	}
	if len(spelled) > 0 {
		t.Fatalf("product files spell the node-identities directory by hand instead of config.NodeIdentitiesDirIn: %v", spelled)
	}
}
