package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSingleFileShareNotYetCreatedInsideConfigDirIsRefused is B2V-4: a
// single-file share whose file does not exist yet was admitted before the
// config-directory check ran, and the daemon serves the file once it
// appears. The check now runs on the file's parent whether or not the file
// exists, so a file the config directory will hold later is refused today.
func TestSingleFileShareNotYetCreatedInsideConfigDirIsRefused(t *testing.T) {
	home, configDir := configDirFixture(t)
	newNode := filepath.Join(configDir, "nodes", "later")
	if err := os.MkdirAll(newNode, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]Service{
		"a file the config directory will hold": {Name: "later", Type: TypeFile, Path: configDir, File: "later.json"},
		"a node's state before it enrolls":      {Name: "state", Type: TypeFile, Path: newNode, File: "tailscaled.state"},
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(t.TempDir(), "config-link")
		if err := os.Symlink(configDir, link); err != nil {
			t.Fatal(err)
		}
		cases["a symlinked parent"] = Service{Name: "linked", Type: TypeFile, Path: link, File: "later.json"}
	}
	for name, svc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(svc.Path, svc.File)); !os.IsNotExist(err) {
				t.Fatalf("fixture file exists (%v); the case would not test a missing file", err)
			}
			assertConfigDirRefusal(t, ValidateService(svc), svc.Path, configDir)
		})
	}

	// Control: a file that does not exist yet outside the config directory
	// is still a valid share; it is served once it appears.
	elsewhere := Service{Name: "notes", Type: TypeFile, Path: home, File: "not-yet.txt"}
	if err := ValidateService(elsewhere); err != nil {
		t.Fatalf("missing file outside the config directory refused: %v", err)
	}
}
