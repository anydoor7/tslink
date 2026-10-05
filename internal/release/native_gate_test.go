package release_test

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v2"
)

const (
	onUbuntu  = "matrix.os == 'ubuntu-latest'"
	onMacOS   = "matrix.os == 'macos-latest'"
	onWindows = "matrix.os == 'windows-latest'"

	windowsRaceStep = "Race (Windows-specific packages)"
)

type nativeStep struct {
	Name             string `yaml:"name"`
	If               string `yaml:"if"`
	Shell            string `yaml:"shell"`
	WorkingDirectory string `yaml:"working-directory"`
	Run              string `yaml:"run"`
}

// nativeJob returns the candidate workflow's top-level env and the steps of
// its native matrix job.
func nativeJob(t *testing.T) (map[string]string, []nativeStep) {
	t.Helper()
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	var wf struct {
		Env  map[string]string `yaml:"env"`
		Jobs map[string]struct {
			Steps []nativeStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &wf); err != nil {
		t.Fatalf("parse %s: %v", candidateWorkflow, err)
	}
	job, ok := wf.Jobs["native"]
	if !ok || len(job.Steps) == 0 {
		t.Fatalf("%s has no native job steps", candidateWorkflow)
	}
	return wf.Env, job.Steps
}

func namedStep(t *testing.T, steps []nativeStep, name string) nativeStep {
	t.Helper()
	var found []nativeStep
	for _, step := range steps {
		if step.Name == name {
			found = append(found, step)
		}
	}
	if len(found) != 1 {
		t.Fatalf("native job has %d steps named %q, want exactly one", len(found), name)
	}
	return found[0]
}

func hasLine(run, want string) bool {
	for _, line := range strings.Split(run, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// TestNativeJobRunsEachPropertyWhereItCanDiffer pins the OS placement of
// every suite execution in the native job. Each extra execution is another
// chance for a timing failure, so race and shuffle run only on the runners
// where they can find something; deleting one of these steps, widening its
// condition, or adding another `go test` step turns this test red.
func TestNativeJobRunsEachPropertyWhereItCanDiffer(t *testing.T) {
	env, steps := nativeJob(t)
	for _, want := range []struct {
		name, cond, shell, dir, line string
	}{
		{"Build", "", "", "", "go build ./..."},
		{"Vet", "", "", "", "go vet ./..."},
		{"Test", "", "", "", "go test -count=1 ./..."},
		{"Race", onMacOS, "", "", "go test -race -count=1 ./..."},
		{windowsRaceStep, onWindows, "bash", "", `go test -race -count=1 "${packages[@]}"`},
		{"Scoped fsnotify native tests", "", "bash", "third_party/fsnotify", `go test -count=1 -run "${TSLINK_FSNOTIFY_OWNED_TESTS:?}" ./...`},
		{"Scoped fsnotify race", "", "bash", "third_party/fsnotify", `go test -race -count=1 -run "${TSLINK_FSNOTIFY_OWNED_TESTS:?}" ./...`},
		{"Recorded deterministic shuffle", onUbuntu, "bash", "", `go test -count=1 -shuffle="${seed}" ./...`},
		{"Native CLI smoke", "", "bash", "", `go build -trimpath -o "${bin}" .`},
		{"Windows Task Scheduler lifecycle smoke", onWindows, "powershell", "", "./scripts/windows-supervision-smoke.ps1 -Binary ./dist/native/tslink.exe"},
		{"Coverage (ubuntu)", onUbuntu, "bash", "", "go test ./... -race -coverprofile=coverage.out"},
	} {
		step := namedStep(t, steps, want.name)
		if step.If != want.cond {
			t.Errorf("native step %q runs if %q, want %q", want.name, step.If, want.cond)
		}
		// A run block that names an env var needs bash on Windows too:
		// PowerShell would read "${NAME}" as an empty script variable.
		if step.Shell != want.shell {
			t.Errorf("native step %q shell = %q, want %q", want.name, step.Shell, want.shell)
		}
		if step.WorkingDirectory != want.dir {
			t.Errorf("native step %q working-directory = %q, want %q", want.name, step.WorkingDirectory, want.dir)
		}
		if !hasLine(step.Run, want.line) {
			t.Errorf("native step %q no longer runs %s:\n%s", want.name, want.line, step.Run)
		}
	}

	suiteSteps := map[string]bool{
		"Test": true, "Race": true, windowsRaceStep: true,
		"Scoped fsnotify native tests": true, "Scoped fsnotify race": true,
		"Recorded deterministic shuffle": true, "Coverage (ubuntu)": true,
	}
	for _, step := range steps {
		if strings.Contains(step.Run, "go test") && !suiteSteps[step.Name] {
			t.Errorf("native step %q runs go test outside the pinned shape:\n%s", step.Name, step.Run)
		}
	}

	if seeds := strings.Fields(env["TSLINK_SHUFFLE_SEEDS"]); len(seeds) != 3 {
		t.Errorf("TSLINK_SHUFFLE_SEEDS = %v, want the three recorded seeds", seeds)
	}
	shuffle := namedStep(t, steps, "Recorded deterministic shuffle").Run
	if !hasLine(shuffle, "for seed in ${TSLINK_SHUFFLE_SEEDS}; do") {
		t.Errorf("shuffle step no longer iterates every recorded seed:\n%s", shuffle)
	}
	if coverage := namedStep(t, steps, "Coverage (ubuntu)").Run; !hasLine(coverage, `threshold="85.0"`) {
		t.Errorf("coverage step lost the 85%% floor:\n%s", coverage)
	}
}

// testFuncs returns the go test entry points (func TestXxx(*testing.T))
// declared in files, mapped to the file that declares them. Files are parsed
// without build constraints, so every platform's tests are seen on any host.
func testFuncs(t *testing.T, files []string) map[string]string {
	t.Helper()
	names := map[string]string{}
	fset := token.NewFileSet()
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestName(fn.Name.Name) || !takesTestingT(fn) {
				continue
			}
			names[fn.Name.Name] = filepath.Base(path)
		}
	}
	return names
}

// isTestName follows cmd/go: "Test" alone, or followed by a non-lowercase rune.
func isTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") {
		return false
	}
	if name == "Test" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(name[len("Test"):])
	return !unicode.IsLower(r)
}

func takesTestingT(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// TestScopedFsnotifyPatternSelectsOnlyOwnedTests keeps the scoped fsnotify
// steps on TSLink's own regression tests. The retained upstream suite
// collects events after fixed sleeps, so it is run by hand when the copy is
// updated (NOTICE.tslink) rather than on every gate. A new owned Test
// function that the pattern misses, or a pattern that reaches an upstream
// test, turns this test red.
func TestScopedFsnotifyPatternSelectsOnlyOwnedTests(t *testing.T) {
	env, _ := nativeJob(t)
	pattern := env["TSLINK_FSNOTIFY_OWNED_TESTS"]
	dir := filepath.Join(repoRoot(t), "third_party", "fsnotify")

	ownedFiles, err := filepath.Glob(filepath.Join(dir, "ownership_*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	ownedFiles = append(ownedFiles, filepath.Join(dir, "close_windows_test.go"))
	owned := map[string]bool{}
	for _, path := range ownedFiles {
		owned[path] = true
	}
	var upstreamFiles []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, "_test.go") && !owned[path] {
			upstreamFiles = append(upstreamFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}

	ownedTests := testFuncs(t, ownedFiles)
	upstreamTests := testFuncs(t, upstreamFiles)
	// Control: both enumerations must see real tests, or every check below
	// passes vacuously.
	if len(ownedFiles) < 4 || len(ownedTests) < len(ownedFiles) {
		t.Fatalf("owned enumeration found %d tests in %d files (%v); it likely broke", len(ownedTests), len(ownedFiles), ownedFiles)
	}
	if _, ok := upstreamTests["TestClose"]; !ok || len(upstreamTests) < 20 {
		t.Fatalf("upstream enumeration found %d tests without TestClose; it likely broke", len(upstreamTests))
	}

	names := make([]string, 0, len(ownedTests))
	for name := range ownedTests {
		names = append(names, name)
	}
	sort.Strings(names)
	if want := "^(" + strings.Join(names, "|") + ")$"; pattern != want {
		t.Errorf("TSLINK_FSNOTIFY_OWNED_TESTS = %q\nwant %q", pattern, want)
	}
	selector, err := regexp.Compile(pattern)
	if err != nil {
		t.Fatalf("TSLINK_FSNOTIFY_OWNED_TESTS is not a regexp: %v", err)
	}
	for _, name := range names {
		if !selector.MatchString(name) {
			t.Errorf("scoped fsnotify steps skip owned test %s (%s)", name, ownedTests[name])
		}
	}
	for name, file := range upstreamTests {
		if selector.MatchString(name) {
			t.Errorf("scoped fsnotify steps run upstream test %s (%s)", name, file)
		}
	}
}

// goListFiles maps each main-module package to the Go, test and external
// test files that `go list` selects for goos.
func goListFiles(t *testing.T, root, goos string) map[string]map[string]bool {
	t.Helper()
	cmd := exec.Command("go", "list", "-json=ImportPath,GoFiles,TestGoFiles,XTestGoFiles", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("GOOS=%s go list: %v", goos, err)
	}
	packages := map[string]map[string]bool{}
	decoder := json.NewDecoder(bytes.NewReader(out))
	for decoder.More() {
		var pkg struct {
			ImportPath                         string
			GoFiles, TestGoFiles, XTestGoFiles []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode GOOS=%s go list: %v", goos, err)
		}
		files := map[string]bool{}
		for _, list := range [][]string{pkg.GoFiles, pkg.TestGoFiles, pkg.XTestGoFiles} {
			for _, name := range list {
				files[name] = true
			}
		}
		packages[pkg.ImportPath] = files
	}
	return packages
}

// TestWindowsRaceSelectsEveryWindowsSpecificPackage executes the workflow's
// own Windows race block with a stub `go`, fed by the real `go list` output
// for GOOS=windows. The expected set is computed here from `go list -json`,
// independently of the block's template and awk filter.
func TestWindowsRaceSelectsEveryWindowsSpecificPackage(t *testing.T) {
	_, steps := nativeJob(t)
	script := namedStep(t, steps, windowsRaceStep).Run
	root := repoRoot(t)

	windows := goListFiles(t, root, "windows")
	want := map[string]bool{}
	for pkg, files := range windows {
		for name := range files {
			if strings.HasSuffix(name, "_windows.go") || strings.HasSuffix(name, "_windows_test.go") {
				want[pkg] = true
			}
		}
	}
	if !want["github.com/anydoor7/tslink/internal/daemon"] || !want["github.com/anydoor7/tslink/internal/server"] {
		t.Fatalf("expected Windows packages are missing from %v; the listing likely broke", want)
	}
	// The step selects by file name. A file built only for Windows through a
	// build constraint alone would escape it unless its package also has a
	// suffixed file.
	linux, darwin := goListFiles(t, root, "linux"), goListFiles(t, root, "darwin")
	for pkg, files := range windows {
		for name := range files {
			if !linux[pkg][name] && !darwin[pkg][name] && !want[pkg] {
				t.Errorf("%s builds %s only on Windows, but the package has no _windows.go or _windows_test.go file, so Windows race skips it", pkg, name)
			}
		}
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "stubs")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := `#!/usr/bin/env bash
set -euo pipefail
case "$1" in
list)
  printf '%s\n' "$@" > "$MOCK_LIST_TRACE"
  printf '%s' "$(< "$MOCK_LISTING")"
  exit "$MOCK_LIST_EXIT" ;;
test)
  printf '%s\n' "$@" > "$MOCK_TEST_TRACE"
  exit "$MOCK_TEST_EXIT" ;;
esac
exit 81
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(dir, "listing")
	listTrace := filepath.Join(dir, "list.trace")
	testTrace := filepath.Join(dir, "test.trace")
	execute := func(t *testing.T, listingText string, listExit, testExit string) (string, int) {
		t.Helper()
		for _, path := range []string{listTrace, testTrace} {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(listing, []byte(listingText), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "-c", script)
		cmd.Dir = dir
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "PATH=") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"MOCK_LISTING="+listing, "MOCK_LIST_TRACE="+listTrace, "MOCK_TEST_TRACE="+testTrace,
			"MOCK_LIST_EXIT="+listExit, "MOCK_TEST_EXIT="+testExit)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), 0
		}
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s block: %v\n%s", windowsRaceStep, err, out)
		}
		return string(out), exit.ExitCode()
	}
	traced := func(t *testing.T, path string) []string {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("stub go was not invoked: %v", err)
		}
		return strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	}
	noTest := func(t *testing.T) {
		t.Helper()
		if _, err := os.Stat(testTrace); !os.IsNotExist(err) {
			t.Fatalf("go test ran without a package selection: stat error %v", err)
		}
	}

	// An empty selection must fail rather than pass a race step that tested
	// nothing. This run also records the block's exact go list arguments.
	out, code := execute(t, "", "0", "0")
	if code == 0 || !strings.Contains(out, "no package has a _windows.go or _windows_test.go source") {
		t.Fatalf("empty selection: exit %d, want the explicit failure\n%s", code, out)
	}
	noTest(t)
	// Re-run the block's own listing for GOOS=windows. The verb and package
	// pattern stay fixed here; only the recorded template is forwarded.
	listArgs := traced(t, listTrace)
	if len(listArgs) != 4 || listArgs[0] != "list" || listArgs[1] != "-f" || listArgs[3] != "./..." {
		t.Fatalf("Windows race block called go %q, want go list -f <template> ./...", listArgs)
	}
	listCmd := exec.Command("go", "list", "-f", listArgs[2], "./...")
	listCmd.Dir = root
	listCmd.Env = append(os.Environ(), "GOOS=windows")
	realListing, err := listCmd.Output()
	if err != nil {
		t.Fatalf("GOOS=windows go %v: %v", listArgs, err)
	}

	t.Run("selects exactly the Windows-specific packages", func(t *testing.T) {
		out, code := execute(t, string(realListing), "0", "0")
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		args := traced(t, testTrace)
		if len(args) < 3 || strings.Join(args[:3], " ") != "test -race -count=1" {
			t.Fatalf("go %v, want go test -race -count=1 <packages>", args)
		}
		got := map[string]bool{}
		for _, pkg := range args[3:] {
			if got[pkg] {
				t.Errorf("package %s selected twice", pkg)
			}
			got[pkg] = true
			if !want[pkg] {
				t.Errorf("selected %s, which has no Windows-specific file", pkg)
			}
		}
		for pkg := range want {
			if !got[pkg] {
				t.Errorf("Windows race skips %s", pkg)
			}
		}
	})
	t.Run("go list failure fails the step", func(t *testing.T) {
		out, code := execute(t, string(realListing), "3", "0")
		if code != 3 {
			t.Fatalf("exit %d, want go list's 3\n%s", code, out)
		}
		noTest(t)
	})
	t.Run("go test failure fails the step", func(t *testing.T) {
		out, code := execute(t, string(realListing), "0", "5")
		if code != 5 {
			t.Fatalf("exit %d, want go test's 5\n%s", code, out)
		}
	})
}
