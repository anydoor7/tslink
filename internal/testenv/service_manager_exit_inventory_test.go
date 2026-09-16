package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// serviceManagerBinaries are the OS service managers whose process exits the
// guard has to own. A source file that names one of them is either a seam the
// guard installs over, or a hole in the guard.
var serviceManagerBinaries = []string{`"launchctl"`, `"systemctl"`, `"loginctl"`}

// serviceManagerExitInventory is the reviewed set of non-test files allowed to
// name a service manager binary. Adding a file here is a deliberate act that
// says: this exit is registered in cmd's osServiceManagerSeams(), or it is not
// an exit at all.
var serviceManagerExitInventory = map[string]string{
	// The guarded write path. launchctl bootout/bootstrap against gui/<uid>.
	"cmd/install_darwin.go": "seam: launchctlCombinedOutput",
	// The guarded write path plus the loginctl lingering probe.
	"cmd/install_linux.go": "seams: systemctlCombinedOutput, loginctlCombinedOutputFn",
	// Read paths. These call through managerOutputFn, which cmd's TestMain
	// already replaces with a fixture for the whole package.
	"cmd/supervision_darwin.go": "reads via managerOutputFn",
	"cmd/supervision_linux.go":  "reads via managerOutputFn",
	// Not an exit: a vocabulary list used to validate release-note wording.
	"cmd/manifest.go": "documentation vocabulary, no exec",
}

// TestServiceManagerExitInventoryIsComplete fails when a new non-test file
// starts naming a service manager binary. The guard can only close exits it
// knows about, and a hole added in some future package would otherwise be
// invisible until it took production down.
//
// The assertion is set equality, not "no unexpected files". If the scanner
// itself broke, every known entry would go missing and this test goes red, so
// a green result cannot come from finding nothing.
func TestServiceManagerExitInventoryIsComplete(t *testing.T) {
	root := repoRootForTest(t)

	found := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, binary := range serviceManagerBinaries {
			if strings.Contains(string(data), binary) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				found[filepath.ToSlash(rel)] = true
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	var unexpected, missing []string
	for file := range found {
		if _, ok := serviceManagerExitInventory[file]; !ok {
			unexpected = append(unexpected, file)
		}
	}
	for file := range serviceManagerExitInventory {
		if !found[file] {
			missing = append(missing, file)
		}
	}
	sort.Strings(unexpected)
	sort.Strings(missing)

	if len(unexpected) > 0 {
		t.Errorf("these files name an OS service manager but are not in serviceManagerExitInventory: %v\n"+
			"If the file adds a process exit, expose it as a package-level seam and register it in cmd's osServiceManagerSeams() "+
			"so testenv.RunWithServiceManagerGuard can close it; then add the file here.", unexpected)
	}
	if len(missing) > 0 {
		t.Errorf("inventory entries no longer name an OS service manager: %v\n"+
			"Either the exit moved (re-point the inventory and the seam) or this scanner stopped matching, "+
			"in which case it would silently pass on a real hole.", missing)
	}
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/testenv/<file> -> repo root
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved repo root %s has no go.mod: %v", root, err)
	}
	return root
}
