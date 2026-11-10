package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

func startupTestRegistry(t *testing.T, names ...string) string {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	path, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := registry.Add(path, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:9"}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func startupListJSON(t *testing.T) StartupListResult {
	t.Helper()
	raw, err := runRecipeRoot(t, "startup", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var frame struct {
		OK   bool              `json:"ok"`
		Data StartupListResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &frame); err != nil || !frame.OK {
		t.Fatalf("startup list --json: %s %v", raw, err)
	}
	return frame.Data
}

func TestStartupCLISetAndList(t *testing.T) {
	regPath := startupTestRegistry(t, "alpha", "beta", "gamma")

	raw, err := runRecipeRoot(t, "startup", "set", "beta", "10", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var set struct {
		OK   bool             `json:"ok"`
		Data StartupSetResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &set); err != nil || !set.OK || set.Data != (StartupSetResult{Service: "beta", Priority: 10}) {
		t.Fatalf("startup set --json: %s %v", raw, err)
	}
	// A negative priority is passed after --, so it is not read as a flag.
	out, err := runRecipeRoot(t, "startup", "set", "gamma", "--", "-5")
	if err != nil || !strings.Contains(out, "Set gamma startup priority to -5") {
		t.Fatalf("startup set negative: %q %v", out, err)
	}

	list := startupListJSON(t)
	if list.Path != filepath.Join(filepath.Dir(regPath), "startup-priority.json") {
		t.Fatalf("path = %q", list.Path)
	}
	want := []StartupEntry{{"beta", 10}, {"alpha", 0}, {"gamma", -5}}
	if len(list.Services) != len(want) {
		t.Fatalf("services = %+v, want %+v", list.Services, want)
	}
	for i := range want {
		if list.Services[i] != want[i] {
			t.Fatalf("services = %+v, want %+v", list.Services, want)
		}
	}
	out, err = runRecipeRoot(t, "startup", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 || strings.Join(strings.Fields(lines[0]), " ") != "ORDER SERVICE PRIORITY" || strings.Join(strings.Fields(lines[1]), " ") != "1 beta 10" {
		t.Fatalf("startup list:\n%s", out)
	}

	out, err = runRecipeRoot(t, "startup", "set", "beta", "0")
	if err != nil || !strings.Contains(out, "Cleared beta startup priority") {
		t.Fatalf("startup set 0: %q %v", out, err)
	}
	if got := startupListJSON(t).Services[0]; got != (StartupEntry{"alpha", 0}) {
		t.Fatalf("after clearing beta the first service is %+v, want alpha in registry order", got)
	}
}

func TestStartupCLIRefusals(t *testing.T) {
	startupTestRegistry(t, "alpha")
	_, err := runRecipeRoot(t, "startup", "set", "ghost", "3", "--json")
	if code, _ := registry.ErrorCode(err); code != "not_found" {
		t.Fatalf("unregistered service: %v, want not_found", err)
	}
	_, err = runRecipeRoot(t, "startup", "set", "alpha", "high")
	if err == nil || !strings.Contains(err.Error(), `startup priority must be an integer, got "high"`) {
		t.Fatalf("non-integer priority: %v", err)
	}
	if got := startupListJSON(t).Services; len(got) != 1 || got[0] != (StartupEntry{"alpha", 0}) {
		t.Fatalf("refused sets changed the order: %+v", got)
	}
}

func TestStartupCLIListEmptyRegistry(t *testing.T) {
	startupTestRegistry(t)
	out, err := runRecipeRoot(t, "startup", "list")
	if err != nil || strings.TrimSpace(out) != "No services registered" {
		t.Fatalf("empty list: %q %v", out, err)
	}
	if got := startupListJSON(t).Services; got == nil || len(got) != 0 {
		t.Fatalf("empty list --json services = %#v, want []", got)
	}
}

func TestStartupCLIReportsUnreadableState(t *testing.T) {
	regPath := startupTestRegistry(t, "alpha")
	hint := registry.StartupPriorityPath(regPath)
	if err := os.WriteFile(hint, []byte(`{"schema_version":1,"services":{},"typo":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runRecipeRoot(t, "startup", "list"); err == nil || !strings.Contains(err.Error(), `unknown field "typo"`) {
		t.Fatalf("list over a malformed hint file: %v", err)
	}
	if err := os.WriteFile(regPath, []byte(`{"schema_version":2,"services":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runRecipeRoot(t, "startup", "list"); err == nil || !strings.Contains(err.Error(), "load registry") {
		t.Fatalf("list over a malformed registry: %v", err)
	}
	t.Setenv(config.ConfigDirEnv, "")
	t.Setenv("HOME", "")
	t.Setenv("APPDATA", "") // Windows uses os.UserConfigDir rather than HOME.
	if _, err := runRecipeRoot(t, "startup", "list"); err == nil {
		t.Fatal("list accepted an unavailable config directory")
	}
	if _, err := runRecipeRoot(t, "startup", "set", "alpha", "1"); err == nil {
		t.Fatal("set accepted an unavailable config directory")
	}
}
