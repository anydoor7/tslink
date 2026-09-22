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

// serviceManagerQuotedBinaryPattern matches a Go string literal that names an
// OS service manager whose process exits the guard has to own, with or without
// a leading directory. A source file containing one is either a seam the guard
// installs over, or a hole in the guard.
//
// The optional directory group is the whole point of the shape. `"launchctl"`
// and `"/bin/launchctl"` reach the same binary; only the first consults PATH,
// so only the first is something the child-process shim can stand in front of.
// The absolute form is therefore the *more* dangerous of the two and was, until
// 2026-09-16, the one form every check here failed to see: the patterns
// required a double quote immediately before the name, which an absolute path
// does not have. A test that built an exec.Command whose program argument was
// the absolute path of launchctl and whose verb was bootout against the
// production label passed all three scans and the shim at once.
//
// This comment says that in prose rather than writing the call out, because
// the pattern below now matches it -- writing the example literally makes this
// file a finding of its own, which is the first thing the fix demonstrated.
//
// The alternation is derived from serviceManagerShimBinaries rather than spelled
// out again, so a manager the PATH shim plants a fake for cannot be one these
// scans ignore.
//
// The closing quote must follow the name directly: "/usr/bin/launchctl-wrapper"
// and "/etc/systemctl.conf" name something else and are not findings.
var serviceManagerQuotedBinaryPattern = `"(?:[^"\n]*/)?` + serviceManagerNameAlternation() + `"`

func serviceManagerNameAlternation() string {
	escaped := make([]string, 0, len(serviceManagerShimBinaries))
	for _, binary := range serviceManagerShimBinaries {
		escaped = append(escaped, regexp.QuoteMeta(binary))
	}
	return "(?:" + strings.Join(escaped, "|") + ")"
}

// serviceManagerLiteralPattern counts those literals anywhere in a file. It
// replaced a strings.Count over the quoted names for the reason above: counting
// `"launchctl"` as a substring cannot see `"/bin/launchctl"`.
var serviceManagerLiteralPattern = regexp.MustCompile(serviceManagerQuotedBinaryPattern)

// serviceManagerTestExecPattern matches a test file building its own process
// exit to a service manager, e.g. exec.Command with the binary named inline.
// It deliberately also matches the same text inside a comment or a string: a
// lexical scanner that tried to be clever about context would be one more
// thing that can silently stop matching.
var serviceManagerTestExecPattern = regexp.MustCompile(
	`exec\.Command(?:Context)?\([^)\n]*` + serviceManagerQuotedBinaryPattern)

// serviceManagerTestArgPassthroughPattern matches a test file spreading a
// caller-supplied slice into a spawned process: an exec constructor whose
// argument list ends in a spread.
//
// This is the shape the PATH shim cannot see. The shim decides what a child
// resolves the name `launchctl` to; it says nothing about a call site that
// hands a compiled tslink binary an argv the helper never inspects, because
// such a call site names no manager at all. `add` without --no-daemon-install
// and `install` both reach the OS service manager through exactly that door,
// and both are one argument away from any existing pass-through helper.
//
// Like the pattern above it matches inside comments and strings on purpose.
var serviceManagerTestArgPassthroughPattern = regexp.MustCompile(
	`exec\.Command(?:Context)?\([^\n]*\.\.\.\)`)

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
	// Not an exit either, and the opposite of one: the list of fake binaries
	// the child-process PATH shim plants, plus one mention inside the doc
	// comment that explains why PATH is the mechanism.
	"internal/testenv/service_manager_path_shim.go": {note: "PATH shim: names the fakes it plants, execs nothing", occurrences: 4},
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
	//
	// "On purpose" became conditional when the PATH shim arrived: the shim is
	// not selective, so until 2026-09-17 this e2e resolved
	// systemctl to a fake in both halves and verified the shim instead of
	// systemd. It now calls testenv.AllowRealServiceManagerInChildProcesses
	// after its own gate, and the opt-in is named in the teardown report.
	"cmd/install_linux_e2e_test.go": {note: "gated real-systemd e2e (TSLINK_SYSTEMD_E2E=1), explicit shim opt-out", occurrences: 3},
	// The PATH shim's own probes. They are written with the binary inline so
	// that this scan sees them: a probe that resolved the name from a variable
	// would be a real process exit hidden from the one check built to find it.
	// Every argv they build names a job label or unit that does not exist, so
	// the worst a real binary could do with them is answer "not found".
	"cmd/service_manager_path_shim_unix_test.go": {note: "PATH shim probes, nonexistent targets only", occurrences: 2},
}

// serviceManagerTestArgPassthroughAllowlist is the reviewed set of _test.go
// files that spread caller-supplied arguments into a spawned process.
//
// Why this list exists at all: cmd/e2e_scaffold_test.go's
// offlineRegistrationArgs appends --no-daemon-install to `add` and
// `template apply`, which is what keeps those two out of the installer. That is
// a convention, not a boundary -- it is applied by two of the pass-through
// helpers and not by the others, and none of them inspects what a caller
// passes. A helper that forwards args verbatim can be handed `install`.
//
// Counting per file is the point, same as the two lists above: a second
// pass-through added to a file that is already listed is the cheapest way to
// open a door nobody reviewed.
var serviceManagerTestArgPassthroughAllowlist = map[string]serviceManagerExitEntry{
	// runCompiledTSLink (no arg filter) and runTSLinkBinaryWithConfigDir
	// (offlineRegistrationArgs). Both run the shipped binary.
	"cmd/compiled_binary_contract_test.go": {note: "compiled-binary helpers; PATH shim covers the child", occurrences: 2},
	// e2eRunBinary, via offlineRegistrationArgs.
	"cmd/e2e_scaffold_test.go": {note: "e2e helper; PATH shim covers the child", occurrences: 1},
	// runRegistryFilesystemProbe: no arg filter at all.
	"cmd/registry_check_filesystem_unix_test.go": {note: "filesystem probe helper; PATH shim covers the child", occurrences: 1},
	// The gated real-systemd e2e forwards to systemctl directly; it is on the
	// exec allowlist above for the same reason.
	"cmd/install_linux_e2e_test.go": {note: "gated real-systemd e2e (TSLINK_SYSTEMD_E2E=1), explicit shim opt-out", occurrences: 2},
	// The PATH shim's own probes, whose targets do not exist.
	"cmd/service_manager_path_shim_unix_test.go": {note: "PATH shim probes, nonexistent targets only", occurrences: 2},
	// Helpers that re-run this repo's own test executable as a daemon, and the
	// execCommand seam stubs that forward to the real constructor. None of them
	// spawns a service manager, and all of them inherit the parent environment.
	"internal/daemon/daemon_test.go": {note: "self-exec daemon helpers and execCommand seam stubs", occurrences: 4},
}

// TestServiceManagerTestArgPassthroughsAreReviewed fails when a test file
// starts forwarding caller-supplied arguments into a spawned process without
// being reviewed for it.
//
// This is the half the PATH shim structurally cannot cover. The shim decides
// what the name `launchctl` resolves to inside a child that inherited this
// process's environment; it has nothing to say about a child given an
// environment built from scratch, a child handed an absolute path, or a helper
// that will happily forward `install`. Those are all visible in source and
// invisible at runtime, which is the exact inverse of the shim.
func TestServiceManagerTestArgPassthroughsAreReviewed(t *testing.T) {
	root := repoRootForTest(t)

	found, err := scanServiceManagerTestArgPassthroughs(root)
	if err != nil {
		t.Fatalf("scan %s: %v", root, err)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(found, serviceManagerTestArgPassthroughAllowlist)

	if len(unexpected) > 0 {
		t.Errorf("these test files forward caller-supplied arguments into a spawned process and are not reviewed: %v\n"+
			"A helper that passes args through unfiltered can be handed `install` or an `add` without --no-daemon-install, "+
			"and the OS service manager is then reached from a process where no seam was ever replaced. "+
			"Confirm the callers cannot reach an installing verb, then add the file here.", unexpected)
	}
	if len(missing) > 0 {
		t.Errorf("reviewed pass-through files no longer forward arguments into a spawned process: %v\n"+
			"Either the helper moved or this scanner stopped matching, in which case it would silently pass on a real hole.", missing)
	}
	if len(miscounted) > 0 {
		t.Errorf("reviewed files changed how many argument pass-throughs they build: %v\n"+
			"A second pass-through inside an already-listed file is the cheapest way to open an unreviewed door.", miscounted)
	}
}

// TestServiceManagerExitInventoryIsComplete fails when a new non-test file
// starts naming a service manager binary, and when an already-reviewed file
// grows a new mention. The guard can only close exits it knows about, and a
// hole added in some future package would otherwise be invisible until it took
// a developer's own installed daemon down.
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
		return len(serviceManagerLiteralPattern.FindAllString(data, -1))
	})
}

// scanServiceManagerTestExecs counts real service manager process exits per
// _test.go file under root.
func scanServiceManagerTestExecs(root string) (map[string]int, error) {
	return scanServiceManagerSources(root, true, func(data string) int {
		return len(serviceManagerTestExecPattern.FindAllString(data, -1))
	})
}

// scanServiceManagerTestArgPassthroughs counts argument pass-throughs into a
// spawned process per _test.go file under root.
func scanServiceManagerTestArgPassthroughs(root string) (map[string]int, error) {
	return scanServiceManagerSources(root, true, func(data string) int {
		return len(serviceManagerTestArgPassthroughPattern.FindAllString(data, -1))
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
