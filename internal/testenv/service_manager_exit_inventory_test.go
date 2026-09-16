package testenv

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// serviceManagerBinaries are the OS service managers whose process exits the
// guard has to own. A source file that names one of them is either a seam the
// guard installs over, or a hole in the guard.
var serviceManagerBinaries = []string{`"launchctl"`, `"systemctl"`, `"loginctl"`}

// serviceManagerTestExecPattern matches a test file building its own process
// exit to a service manager, e.g. exec.Command with the binary named inline.
// It deliberately also matches the same text inside a comment or a string: a
// lexical scanner that tried to be clever about context would be one more
// thing that can silently stop matching.
var serviceManagerTestExecPattern = regexp.MustCompile(
	`exec\.Command(?:Context)?\([^)\n]*"(?:launchctl|systemctl|loginctl)"`)

// serviceManagerExitEntry is one reviewed file and the exact number of service
// manager mentions it is allowed to contain.
//
// The count is the point. A file-granular "this file may name launchctl" check
// goes green when a second, unwired exec.Command to that same binary is added to
// a file that is already on the list, which is the cheapest way to reopen the
// hole this whole mechanism exists to close.
type serviceManagerExitEntry struct {
	note        string
	occurrences int
}

// serviceManagerExitInventory is the reviewed set of non-test files allowed to
// name a service manager binary. Adding a file here is a deliberate act that
// says: this exit is registered in cmd's osServiceManagerSeams(), or it is not
// an exit at all. Changing a count is the same kind of act.
var serviceManagerExitInventory = map[string]serviceManagerExitEntry{
	// The guarded write path. launchctl bootout/bootstrap against gui/<uid>.
	// One mention: the seam variable's default implementation. Every other
	// call site in this file goes through launchctlCombinedOutput by name.
	"cmd/install_darwin.go": {note: "seam: launchctlCombinedOutput", occurrences: 1},
	// The guarded write path plus the loginctl lingering probe: one mention
	// each, both seam defaults registered in osServiceManagerSeams().
	"cmd/install_linux.go": {note: "seams: systemctlCombinedOutput, loginctlCombinedOutputFn", occurrences: 2},
	// Read paths. These call through managerOutputFn, which cmd's TestMain
	// already replaces with a fixture for the whole package. The mentions are
	// the manager name passed to that fixture-able seam, not process exits.
	"cmd/supervision_darwin.go": {note: "reads via managerOutputFn", occurrences: 4},
	"cmd/supervision_linux.go":  {note: "reads via managerOutputFn", occurrences: 1},
	// Not an exit: a vocabulary list used to validate release-note wording.
	"cmd/manifest.go": {note: "documentation vocabulary, no exec", occurrences: 1},
}

// serviceManagerTestExecAllowlist is the reviewed set of _test.go files allowed
// to build a real process exit to a service manager.
//
// Test files are the quieter half of the hole. The guard replaces a seam
// variable, so a test that never touches the seam and calls exec.Command
// itself reaches the real binary with the guard none the wiser, and the
// non-test scan above skips _test.go entirely by design.
var serviceManagerTestExecAllowlist = map[string]serviceManagerExitEntry{
	// Opt-in e2e, off unless TSLINK_SYSTEMD_E2E=1, and it refuses to run
	// against anything that looks like a real installation. It drives the
	// caller's real `systemctl --user` on purpose; that is the test.
	"cmd/install_linux_e2e_test.go": {note: "gated real-systemd e2e (TSLINK_SYSTEMD_E2E=1)", occurrences: 3},
}

// TestServiceManagerExitInventoryIsComplete fails when a new non-test file
// starts naming a service manager binary, and when an already-reviewed file
// grows a new mention. The guard can only close exits it knows about, and a
// hole added in some future package would otherwise be invisible until it took
// production down.
//
// The assertion is set equality plus per-file counts, not "no unexpected
// files". If the scanner itself broke, every known entry would go missing and
// this test goes red, so a green result cannot come from finding nothing.
func TestServiceManagerExitInventoryIsComplete(t *testing.T) {
	root := repoRootForTest(t)

	found, err := scanServiceManagerLiterals(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(found, serviceManagerExitInventory)

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
	if len(miscounted) > 0 {
		t.Errorf("reviewed files changed how many times they name an OS service manager: %v\n"+
			"A new mention inside an already-listed file is the cheapest way to add an exit that no seam covers. "+
			"Route it through the file's existing seam, or register a new seam and update the count here.", miscounted)
	}
}

// TestServiceManagerTestExecsAreReviewed fails when a test file builds its own
// exec.Command to a service manager outside the reviewed allowlist. The guard
// owns seam variables; it cannot see a test that never touches one.
func TestServiceManagerTestExecsAreReviewed(t *testing.T) {
	root := repoRootForTest(t)

	found, err := scanServiceManagerTestExecs(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(found, serviceManagerTestExecAllowlist)

	if len(unexpected) > 0 {
		t.Errorf("these test files build a real service manager process exit and are not reviewed: %v\n"+
			"A test that calls exec.Command itself bypasses the guard entirely: the guard replaces seam variables, "+
			"not the exec package. Route the call through the package seam, or add the file here with the gate that keeps it off by default.", unexpected)
	}
	if len(missing) > 0 {
		t.Errorf("allowlisted test files no longer build a service manager process exit: %v\n"+
			"Either the call moved or this scanner stopped matching, in which case it would silently pass on a real hole.", missing)
	}
	if len(miscounted) > 0 {
		t.Errorf("reviewed test files changed how many real service manager process exits they build: %v", miscounted)
	}
}

// scanServiceManagerLiterals counts service manager binary mentions per
// non-test .go file under root.
func scanServiceManagerLiterals(root string) (map[string]int, error) {
	return scanServiceManagerSources(root, false, func(data string) int {
		total := 0
		for _, binary := range serviceManagerBinaries {
			total += strings.Count(data, binary)
		}
		return total
	})
}

// scanServiceManagerTestExecs counts real service manager process exits per
// _test.go file under root.
func scanServiceManagerTestExecs(root string) (map[string]int, error) {
	return scanServiceManagerSources(root, true, func(data string) int {
		return len(serviceManagerTestExecPattern.FindAllString(data, -1))
	})
}

func scanServiceManagerSources(root string, testFiles bool, count func(string) int) (map[string]int, error) {
	found := map[string]int{}
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") != testFiles {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if n := count(string(data)); n > 0 {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			found[filepath.ToSlash(rel)] = n
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// diffServiceManagerInventory compares a scan against a reviewed set in all
// three directions: files nobody reviewed, reviewed files that stopped
// matching (the scanner's own tripwire), and reviewed files whose count moved.
func diffServiceManagerInventory(found map[string]int, want map[string]serviceManagerExitEntry) (unexpected, missing, miscounted []string) {
	for file := range found {
		if _, ok := want[file]; !ok {
			unexpected = append(unexpected, file)
		}
	}
	for file, entry := range want {
		got, ok := found[file]
		switch {
		case !ok:
			missing = append(missing, file)
		case got != entry.occurrences:
			miscounted = append(miscounted, fmtCount(file, entry, got))
		}
	}
	sort.Strings(unexpected)
	sort.Strings(missing)
	sort.Strings(miscounted)
	return unexpected, missing, miscounted
}

func fmtCount(file string, entry serviceManagerExitEntry, got int) string {
	return file + " (" + entry.note + "): reviewed " + itoa(entry.occurrences) + ", found " + itoa(got)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
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
