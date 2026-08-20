package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/monody0007/tslink/cmd"
)

func testManifestBytes(t *testing.T, platform cmd.PlatformInfo, mutate func(*cmd.CLIManifest)) []byte {
	t.Helper()
	manifest := cmd.Manifest()
	manifest.Platform = platform
	if mutate != nil {
		mutate(&manifest)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func otherPlatform() cmd.PlatformInfo {
	goos := "linux"
	if runtime.GOOS == goos {
		goos = "darwin"
	}
	return cmd.PlatformInfo{GOOS: goos, GOARCH: runtime.GOARCH}
}

func TestCheckManifestStrictSamePlatformRejectsGenuinelyStaleManifest(t *testing.T) {
	platform := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, platform, nil)
	existing := testManifestBytes(t, platform, func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Short = "deliberately stale same-platform command text"
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || result.Equal {
		t.Fatalf("same-platform stale check = %+v, want strict mismatch", result)
	}
}

func TestCheckManifestStrictSameGOOSIgnoresArchitectureButRejectsStaleContent(t *testing.T) {
	manifestPlatform := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: "arm64"}
	runningPlatform := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: "amd64"}
	generated := testManifestBytes(t, runningPlatform, nil)

	current := testManifestBytes(t, manifestPlatform, nil)
	result, err := compareManifest(current, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || !result.Equal {
		t.Fatalf("same-GOOS different-architecture current check = %+v, want strict match", result)
	}

	stale := testManifestBytes(t, manifestPlatform, func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Short = "deliberately stale same-GOOS command text"
	})
	result, err = compareManifest(stale, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || result.Equal {
		t.Fatalf("same-GOOS different-architecture stale check = %+v, want strict mismatch", result)
	}
}

func TestCheckManifestCrossPlatformAcceptsIndependentMatchAndSkipsCommands(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Short = "platform-specific command difference"
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || !result.Equal {
		t.Fatalf("cross-platform command-only check = %+v, want independent match", result)
	}
}

func TestCheckManifestCrossPlatformRejectsPlatformIndependentChange(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.RegistrySchemaVersion++
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || result.Equal {
		t.Fatalf("cross-platform independent-field check = %+v, want mismatch", result)
	}
}

func TestCheckManifestRequiresDeclaredPlatform(t *testing.T) {
	data := testManifestBytes(t, cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, nil)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "platform")
	withoutPlatform, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}

	_, err = compareManifest(withoutPlatform, data)
	if err == nil || !strings.Contains(err.Error(), "required top-level field platform is missing") {
		t.Fatalf("missing platform error = %v", err)
	}
}

func TestCheckManifestCrossPlatformReportsComparedAndSkippedCoverage(t *testing.T) {
	result := manifestCheckResult{
		ManifestPlatform: cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"},
		RunningPlatform:  cmd.PlatformInfo{GOOS: "linux", GOARCH: "arm64"},
	}
	compared, skipped := crossPlatformCoverageMessages(result)
	if want := "gen-manifest: compared platform-independent top-level fields (all except platform and commands) for manifest darwin/arm64 and running linux/arm64"; compared != want {
		t.Fatalf("compared message = %q, want %q", compared, want)
	}
	if want := "gen-manifest: skipped platform-specific commands and flags for manifest darwin/arm64 on running linux/arm64"; skipped != want {
		t.Fatalf("skipped message = %q, want %q", skipped, want)
	}
}

func TestCheckManifestCrossPlatformFailureNamesConstraintWithoutSuccessOutput(t *testing.T) {
	result := manifestCheckResult{
		ManifestPlatform: cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"},
		RunningPlatform:  cmd.PlatformInfo{GOOS: "linux", GOARCH: "arm64"},
		SamePlatform:     false,
		Equal:            false,
	}
	output := outputForManifestCheck(result)
	if output.OK {
		t.Fatal("cross-platform stale output reports success")
	}
	if len(output.Stdout) != 0 {
		t.Fatalf("cross-platform stale stdout = %q, want no success-shaped coverage lines", output.Stdout)
	}
	want := "gen-manifest: docs/cli-manifest.json is stale; the committed manifest is authoritative for GOOS=darwin and must be regenerated on that GOOS"
	if output.Stderr != want {
		t.Fatalf("cross-platform stale stderr = %q, want %q", output.Stderr, want)
	}
}

func TestCheckManifestSamePlatformStaleMessageRemainsActionable(t *testing.T) {
	output := outputForManifestCheck(manifestCheckResult{SamePlatform: true, Equal: false})
	want := "gen-manifest: docs/cli-manifest.json is stale; run `go run ./tools/gen-manifest`"
	if output.OK || len(output.Stdout) != 0 || output.Stderr != want {
		t.Fatalf("same-platform stale output = %+v, want stderr %q only", output, want)
	}
}

var knownGOARCH = map[string]struct{}{
	"386": {}, "amd64": {}, "arm": {}, "arm64": {}, "loong64": {},
	"mips": {}, "mips64": {}, "mips64le": {}, "mipsle": {},
	"ppc64": {}, "ppc64le": {}, "riscv64": {}, "s390x": {}, "wasm": {},
}

var packageSourceExtensions = map[string]struct{}{
	".c": {}, ".cc": {}, ".cpp": {}, ".cxx": {}, ".f": {}, ".F": {},
	".for": {}, ".f90": {}, ".go": {}, ".h": {}, ".m": {}, ".s": {},
	".S": {}, ".swig": {}, ".swigcxx": {}, ".syso": {},
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}

func architectureTagsInConstraint(expr constraint.Expr, found map[string]struct{}) {
	switch expr := expr.(type) {
	case *constraint.TagExpr:
		for arch := range knownGOARCH {
			if expr.Tag == arch || strings.HasPrefix(expr.Tag, arch+".") {
				found[expr.Tag] = struct{}{}
			}
		}
	case *constraint.NotExpr:
		architectureTagsInConstraint(expr.X, found)
	case *constraint.AndExpr:
		architectureTagsInConstraint(expr.X, found)
		architectureTagsInConstraint(expr.Y, found)
	case *constraint.OrExpr:
		architectureTagsInConstraint(expr.X, found)
		architectureTagsInConstraint(expr.Y, found)
	}
}

func architectureScopedPackageSources(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(entry.Name())
		if _, ok := packageSourceExtensions[ext]; !ok {
			return nil
		}
		stem := strings.TrimSuffix(entry.Name(), ext)
		stem = strings.TrimSuffix(stem, "_test")
		for arch := range knownGOARCH {
			if strings.HasSuffix(stem, "_"+arch) {
				found = append(found, filepath.ToSlash(path)+" (filename)")
				break
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "//go:build ") {
				continue
			}
			expr, err := constraint.Parse(line)
			if err != nil {
				return fmt.Errorf("parse build constraint in %s: %w", path, err)
			}
			tags := map[string]struct{}{}
			architectureTagsInConstraint(expr, tags)
			for tag := range tags {
				found = append(found, filepath.ToSlash(path)+" (build tag "+tag+")")
			}
		}
		return nil
	})
	sort.Strings(found)
	return found, err
}

func TestSameGOOSComparisonAssumesNoArchitectureScopedPackageSources(t *testing.T) {
	found, err := architectureScopedPackageSources(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("GOARCH-neutral manifest comparison is invalid; architecture-scoped package sources found: %v", found)
	}
}

func platformSpecificFlagRegistrations(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "cmd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var registrations []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") ||
			(!strings.HasSuffix(name, "_darwin.go") && !strings.HasSuffix(name, "_linux.go") && !strings.HasSuffix(name, "_windows.go")) {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if strings.HasPrefix(method.Sel.Name, "Get") {
				return true
			}
			flagSetCall, ok := method.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			flagSetMethod, ok := flagSetCall.Fun.(*ast.SelectorExpr)
			if !ok || (flagSetMethod.Sel.Name != "Flags" && flagSetMethod.Sel.Name != "PersistentFlags") {
				return true
			}
			command, ok := flagSetMethod.X.(*ast.Ident)
			if !ok || len(call.Args) < 2 {
				registrations = append(registrations, name+":unrecognized-registration")
				return true
			}
			flagNameLiteral, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				registrations = append(registrations, name+":"+command.Name+":unrecognized-flag-name")
				return true
			}
			flagName, err := strconv.Unquote(flagNameLiteral.Value)
			if err != nil {
				registrations = append(registrations, name+":unrecognized-flag-name")
				return true
			}
			usageLiteral, ok := call.Args[len(call.Args)-1].(*ast.BasicLit)
			if !ok {
				registrations = append(registrations, name+":"+command.Name+":"+flagName+":unrecognized-usage")
				return true
			}
			usage, err := strconv.Unquote(usageLiteral.Value)
			if err != nil {
				t.Fatalf("decode %s flag usage in %s: %v", flagName, name, err)
			}
			registrations = append(registrations, strings.Join([]string{name, command.Name, flagName, usage}, ":"))
			return true
		})
	}
	sort.Strings(registrations)
	return registrations
}

func TestPlatformSpecificFlagRegistrationsAreExactlyTheTwoDarwinForceFlags(t *testing.T) {
	got := platformSpecificFlagRegistrations(t)
	want := []string{
		"install_darwin.go:installCmd:force:Proceed with an upgrade despite an unavailable launchd domain (may start a second daemon)",
		"uninstall_darwin.go:uninstallCmd:force:Remove the plist despite an unavailable launchd domain (may leave a daemon running)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("platform-specific flag registrations = %q, want exactly %q", got, want)
	}
}

func TestMinimumGoVersionFromGoMod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n\ngo 1.26.5\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := minimumGoVersionFromGoMod(path)
	if err != nil {
		t.Fatalf("minimumGoVersionFromGoMod() error = %v", err)
	}
	if got != "1.26.5" {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want 1.26.5", got)
	}
}

func TestMinimumGoVersionFromGoModFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got, err := minimumGoVersionFromGoMod(path); err == nil {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want error for missing go directive", got)
	}
}
