package cmd

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// TestShareAndAddRefuseTSLinkConfigDirectory is A2's probe through both
// writers: `tslink share <config dir>` and `tslink add --dir <config dir>`
// used to register a whole-tree file share of the node keys.
func TestShareAndAddRefuseTSLinkConfigDirectory(t *testing.T) {
	configDir := testenv.SetHome(t, t.TempDir())
	if err := os.MkdirAll(filepath.Join(configDir, "nodes", "svc"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, regPath := shareMCPWireActions(t)
	for _, root := range []string{configDir, filepath.Join(configDir, "nodes")} {
		_, err := executeShare(context.Background(), sharePaths{Registry: regPath}, shareRequest{Target: root, Ephemeral: true}, time.Second, io.Discard)
		if code, _ := registry.ErrorCode(err); code != registry.CodePathExposesConfigDir {
			t.Fatalf("share %s error = %v, want %s", root, err, registry.CodePathExposesConfigDir)
		}
	}
	if _, err := os.Stat(regPath); !os.IsNotExist(err) {
		t.Fatalf("a refused share wrote %s: %v", regPath, err)
	}

	addRegPath := stubAddWritePaths(t)
	err := runAddCmd(t, []string{"cfg"}, map[string]string{"dir": configDir})
	if code, _ := registry.ErrorCode(err); code != registry.CodePathExposesConfigDir {
		t.Fatalf("add --dir %s error = %v, want %s", configDir, err, registry.CodePathExposesConfigDir)
	}
	if _, err := os.Stat(addRegPath); !os.IsNotExist(err) {
		t.Fatalf("a refused add wrote %s: %v", addRegPath, err)
	}
}

// TestHomeShareWarnsWhenConfigDirectoryLivesElsewhere: with TSLINK_CONFIG_DIR
// outside home, a directory share of home no longer exposes TSLink's state
// and is accepted, with a warning in both results.
func TestHomeShareWarnsWhenConfigDirectoryLivesElsewhere(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv(config.ConfigDirEnv, filepath.Join(t.TempDir(), "tslink-config"))

	actions, _ := shareMCPWireActions(t)
	encodedHome, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	shared := callMCPShare(t, actions, `{"target":`+string(encodedHome)+`}`)
	if !resultHasWarning(shared, inspect.WarningCodeFileRootHomeDirectory) {
		t.Fatalf("share of home = %v, want a %s warning", shared, inspect.WarningCodeFileRootHomeDirectory)
	}

	regPath := stubAddWritePaths(t)
	paths := mcpSharePaths(t)
	paths.Registry = regPath
	result, err := defaultMCPActions(paths, io.Discard).add(context.Background(), AddParams{Name: "home", Dir: home, NoDaemonInstall: true}, true)
	if err != nil {
		t.Fatalf("add --dir home with the config directory elsewhere: %v", err)
	}
	added, ok := result.(AddResult)
	if !ok {
		t.Fatalf("add result type %T", result)
	}
	found := false
	for _, warning := range added.Warnings {
		found = found || warning.Code == inspect.WarningCodeFileRootHomeDirectory
	}
	if !found {
		t.Fatalf("add result warnings = %+v, want %s", added.Warnings, inspect.WarningCodeFileRootHomeDirectory)
	}

	// An unrelated directory gets no such warning.
	plain := callMCPShare(t, actions, `{"target":`+string(mustJSON(t, t.TempDir()))+`}`)
	if resultHasWarning(plain, inspect.WarningCodeFileRootHomeDirectory) {
		t.Fatalf("share of an unrelated directory warns about home: %v", plain)
	}
}

func resultHasWarning(result map[string]any, code string) bool {
	warnings, _ := result["warnings"].([]any)
	for _, raw := range warnings {
		if warning, ok := raw.(map[string]any); ok && warning["code"] == code {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
