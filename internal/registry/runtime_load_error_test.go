package registry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A missing registry is a valid empty start; any other read failure is not.
// That single distinction is what stands between "no services configured yet"
// and "the registry cannot be read, so treat every configured service as
// deleted", and both runtime loaders depend on it.
func TestRuntimeLoadersSeparateMissingFromUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")

	t.Run("missing_is_empty", func(t *testing.T) {
		reg, issues, err := LoadForRuntime(path)
		if err != nil || len(issues) != 0 || reg == nil || len(reg.Services) != 0 {
			t.Fatalf("LoadForRuntime(missing) reg=%+v issues=%v err=%v", reg, issues, err)
		}
		reg, state, err := LoadWithFileState(path)
		if err != nil || state != RegistryFileMissing || reg == nil || len(reg.Services) != 0 {
			t.Fatalf("LoadWithFileState(missing) reg=%+v state=%q err=%v", reg, state, err)
		}
	})

	t.Run("unreadable_is_an_error", func(t *testing.T) {
		if _, err := Add(path, Service{Name: "app", Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
		// A regular file the caller owns cannot be made unreadable to that
		// caller on Unix, so the read itself is the injection point.
		denied := &fs.PathError{Op: "open", Path: path, Err: os.ErrPermission}
		old := readRegistryFile
		t.Cleanup(func() { readRegistryFile = old })
		readRegistryFile = func(string) ([]byte, error) { return nil, denied }

		reg, _, err := LoadForRuntime(path)
		if !errors.Is(err, os.ErrPermission) || reg != nil {
			t.Fatalf("LoadForRuntime turned a permission error into reg=%+v err=%v", reg, err)
		}
		reg, state, err := LoadWithFileState(path)
		if !errors.Is(err, os.ErrPermission) || reg != nil || state != "" {
			t.Fatalf("LoadWithFileState turned a permission error into reg=%+v state=%q err=%v", reg, state, err)
		}
	})

	t.Run("empty_file_stays_a_hard_error", func(t *testing.T) {
		blank := filepath.Join(t.TempDir(), "registry.json")
		if err := os.WriteFile(blank, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadForRuntime(blank); err == nil || !strings.Contains(err.Error(), "registry.json is empty") {
			t.Fatalf("empty registry err=%v", err)
		}
	})
}
