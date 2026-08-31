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
	"github.com/monody0007/tslink/internal/manifestcheck"
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

func TestCheckManifestCrossPlatformAcceptsOnlyMarkedCommandDifferences(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Flags = append(manifest.Commands[0].Flags, cmd.FlagInfo{
			Name: "platform-only", Type: "bool", Usage: "synthetic marked flag", Platforms: []string{otherPlatform().GOOS},
		})
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || !result.Equal {
		t.Fatalf("cross-platform marked-entry check = %+v, want match after exact exclusion", result)
	}
}

func TestCheckManifestCrossPlatformRejectsUnmarkedPlatformSpecificFlag(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Flags = append(manifest.Commands[0].Flags, cmd.FlagInfo{
			Name: "unmarked-platform-only", Type: "bool", Usage: "must remain visible to the comparison",
		})
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || result.Equal {
		t.Fatalf("cross-platform unmarked flag check = %+v, want mismatch", result)
	}
}

func TestCheckManifestCrossPlatformRejectsRenamedServeCommand(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		for i := range manifest.Commands {
			if manifest.Commands[i].Path == "tslink serve" {
				manifest.Commands[i].Path = "tslink nonexistent-serve"
				return
			}
		}
		t.Fatal("test manifest has no tslink serve command")
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || result.Equal {
		t.Fatalf("cross-platform renamed-command check = %+v, want mismatch", result)
	}
}

func TestCheckManifestStrictStillRejectsMutatedMarkedEntries(t *testing.T) {
	platform := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	addMarkedFlag := func(manifest *cmd.CLIManifest, usage string) {
		manifest.Commands[0].Flags = append(manifest.Commands[0].Flags, cmd.FlagInfo{
			Name: "marked-strict-probe", Type: "bool", Usage: usage, Platforms: []string{"darwin"},
		})
	}
	generatedFlag := testManifestBytes(t, platform, func(manifest *cmd.CLIManifest) {
		addMarkedFlag(manifest, "current marked usage")
	})
	existingFlag := testManifestBytes(t, platform, func(manifest *cmd.CLIManifest) {
		addMarkedFlag(manifest, "mutated marked usage")
	})
	result, err := compareManifest(existingFlag, generatedFlag)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || result.Equal {
		t.Fatalf("strict marked-flag mutation = %+v, want mismatch", result)
	}

	generatedField := testManifestBytes(t, platform, nil)
	existingField := testManifestBytes(t, platform, func(manifest *cmd.CLIManifest) {
		field := manifestCommand(t, manifest, "tslink install").JSONResultFields["plist_path"]
		field.Description = "mutated marked result field"
		for i := range manifest.Commands {
			if manifest.Commands[i].Path == "tslink install" {
				manifest.Commands[i].JSONResultFields["plist_path"] = field
				return
			}
		}
	})
	result, err = compareManifest(existingField, generatedField)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || result.Equal {
		t.Fatalf("strict marked-result-field mutation = %+v, want mismatch", result)
	}
}

func manifestCommand(t *testing.T, manifest *cmd.CLIManifest, path string) cmd.CommandInfo {
	t.Helper()
	for _, command := range manifest.Commands {
		if command.Path == path {
			return command
		}
	}
	t.Fatalf("test manifest has no %s command", path)
	return cmd.CommandInfo{}
}

func TestCheckManifestPlatformMarksAreKeyedByCommandPathAndFlagName(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	withInstallMark := func(manifest *cmd.CLIManifest, mutateNeutral bool) {
		for i := range manifest.Commands {
			switch manifest.Commands[i].Path {
			case "tslink install":
				manifest.Commands[i].Flags = append(manifest.Commands[i].Flags, cmd.FlagInfo{
					Name: "force", Type: "bool", Usage: "synthetic install force", Platforms: []string{otherPlatform().GOOS},
				})
			case "tslink tags delete-remote":
				if mutateNeutral {
					for j := range manifest.Commands[i].Flags {
						if manifest.Commands[i].Flags[j].Name == "force" {
							manifest.Commands[i].Flags[j].Usage = "mutated neutral force usage"
						}
					}
				}
			}
		}
	}
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		withInstallMark(manifest, false)
	})
	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Equal {
		t.Fatalf("install --force mark should exclude only its own key: %+v", result)
	}

	existing = testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		withInstallMark(manifest, true)
	})
	result, err = compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.Equal {
		t.Fatal("install --force mark incorrectly excluded tags delete-remote --force")
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

func TestCheckManifestRejectsFabricatedGOARCH(t *testing.T) {
	generated := testManifestBytes(t, cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, nil)
	existing := testManifestBytes(t, cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: "TOTALLY-FAKE-ARCH"}, nil)

	_, err := compareManifest(existing, generated)
	if err == nil || !strings.Contains(err.Error(), `decode committed manifest: platform goarch "TOTALLY-FAKE-ARCH" is not a recognized Go architecture`) {
		t.Fatalf("fabricated GOARCH error = %v", err)
	}
}

func TestCheckManifestCrossPlatformReportsComparedAndSkippedCoverage(t *testing.T) {
	result := manifestCheckResult{
		ManifestPlatform: cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"},
		RunningPlatform:  cmd.PlatformInfo{GOOS: "linux", GOARCH: "arm64"},
		ManifestCoverage: manifestCoverage{Commands: 35, Flags: 80, MarkedFlags: 2, MarkedJSONResultFields: 23},
	}
	compared, skipped := crossPlatformCoverageMessages(result)
	if want := "gen-manifest: compared all top-level fields except platform for manifest darwin/arm64 and running linux/arm64"; compared != want {
		t.Fatalf("compared message = %q, want %q", compared, want)
	}
	if want := "gen-manifest: compared 35/35 command identities and 78/80 committed flag entries; excluded 2 platform-marked flags and 23 platform-marked JSON result fields"; skipped != want {
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
	want := "gen-manifest: docs/cli-manifest.json is stale; the committed manifest is authoritative for GOOS=darwin and must be regenerated on that GOOS, or mark a proven platform-scoped command entry with platforms"
	if output.Stderr != want {
		t.Fatalf("cross-platform stale stderr = %q, want %q", output.Stderr, want)
	}
}

func TestCheckManifestSameGOOSArchitectureDivergenceIsDiagnostic(t *testing.T) {
	output := outputForManifestCheck(manifestCheckResult{
		ManifestPlatform: cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"},
		RunningPlatform:  cmd.PlatformInfo{GOOS: "darwin", GOARCH: "amd64"},
		SamePlatform:     true,
		Equal:            false,
	})
	want := "gen-manifest: docs/cli-manifest.json differs across architectures on GOOS=darwin (manifest GOARCH=arm64, running GOARCH=amd64); manifest generation must remain architecture-independent"
	if output.OK || len(output.Stdout) != 0 || output.Stderr != want {
		t.Fatalf("same-GOOS architecture-divergent output = %+v, want stderr %q only", output, want)
	}
}

func TestStrictSuccessOutputIsTheAssertedConstant(t *testing.T) {
	output := outputForManifestCheck(manifestCheckResult{SamePlatform: true, Equal: true})
	if !output.OK || output.Stderr != "" || len(output.Stdout) != 1 || output.Stdout[0] != manifestcheck.StrictUpToDateMessage {
		t.Fatalf("strict success output = %+v, want stdout exactly %q", output, manifestcheck.StrictUpToDateMessage)
	}
}

func TestCheckManifestSamePlatformStaleMessageRemainsActionable(t *testing.T) {
	output := outputForManifestCheck(manifestCheckResult{SamePlatform: true, Equal: false})
	want := "gen-manifest: docs/cli-manifest.json is stale; run `go run ./tools/gen-manifest`"
	if output.OK || len(output.Stdout) != 0 || output.Stderr != want {
		t.Fatalf("same-platform stale output = %+v, want stderr %q only", output, want)
	}
}

var packageSourceExtensions = map[string]struct{}{
	".c": {}, ".cc": {}, ".cpp": {}, ".cxx": {}, ".f": {}, ".F": {},
	".for": {}, ".f90": {}, ".go": {}, ".h": {}, ".m": {}, ".s": {},
	".S": {}, ".swig": {}, ".swigcxx": {}, ".syso": {},
}

func isSafeManifestGOARCHProvenance(parents []ast.Node) bool {
	if len(parents) < 2 {
		return false
	}
	keyValue, ok := parents[len(parents)-1].(*ast.KeyValueExpr)
	if !ok {
		return false
	}
	key, ok := keyValue.Key.(*ast.Ident)
	if !ok || key.Name != "GOARCH" {
		return false
	}
	composite, ok := parents[len(parents)-2].(*ast.CompositeLit)
	if !ok {
		return false
	}
	typeName, ok := composite.Type.(*ast.Ident)
	return ok && typeName.Name == "PlatformInfo"
}

func architectureSensitiveCommandExpressions(path string, data []byte) ([]string, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, data, 0)
	if err != nil {
		return nil, err
	}
	found := map[string]struct{}{}
	var parents []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			parents = parents[:len(parents)-1]
			return true
		}
		position := files.Position(node.Pos())
		marker := fmt.Sprintf("%s:%d", filepath.ToSlash(path), position.Line)
		switch expression := node.(type) {
		case *ast.SelectorExpr:
			if expression.Sel.Name == "GOARCH" && !isSafeManifestGOARCHProvenance(parents) {
				found[marker+" (GOARCH expression)"] = struct{}{}
			}
			if qualifier, ok := expression.X.(*ast.Ident); ok {
				if qualifier.Name == "strconv" && expression.Sel.Name == "IntSize" {
					found[marker+" (strconv.IntSize)"] = struct{}{}
				}
				if qualifier.Name == "math" && (expression.Sel.Name == "MaxInt" || expression.Sel.Name == "MinInt") {
					found[marker+" (word-size integer bound)"] = struct{}{}
				}
				if qualifier.Name == "unsafe" && expression.Sel.Name == "Sizeof" {
					found[marker+" (unsafe.Sizeof)"] = struct{}{}
				}
			}
		case *ast.BasicLit:
			if expression.Kind == token.STRING {
				value, err := strconv.Unquote(expression.Value)
				if err == nil && value == "GOARCH" {
					found[marker+" (GOARCH string)"] = struct{}{}
				}
			}
		}
		parents = append(parents, node)
		return true
	})
	result := make([]string, 0, len(found))
	for marker := range found {
		result = append(result, marker)
	}
	sort.Strings(result)
	return result, nil
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
		if ext == ".go" && !strings.HasSuffix(entry.Name(), "_test.go") {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			parts := strings.Split(filepath.ToSlash(relative), "/")
			if len(parts) > 1 && parts[0] == "cmd" {
				expressions, err := architectureSensitiveCommandExpressions(path, data)
				if err != nil {
					return fmt.Errorf("parse architecture expressions in %s: %w", path, err)
				}
				found = append(found, expressions...)
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

var knownGOOS = map[string]struct{}{
	"aix": {}, "android": {}, "darwin": {}, "dragonfly": {}, "freebsd": {},
	"illumos": {}, "ios": {}, "js": {}, "linux": {}, "netbsd": {},
	"openbsd": {}, "plan9": {}, "solaris": {}, "wasip1": {}, "windows": {},
}

func constraintTags(expr constraint.Expr, found map[string]struct{}) {
	switch expr := expr.(type) {
	case *constraint.TagExpr:
		found[expr.Tag] = struct{}{}
	case *constraint.NotExpr:
		constraintTags(expr.X, found)
	case *constraint.AndExpr:
		constraintTags(expr.X, found)
		constraintTags(expr.Y, found)
	case *constraint.OrExpr:
		constraintTags(expr.X, found)
		constraintTags(expr.Y, found)
	}
}

func isPlatformScopedCommandSource(name string, data []byte) (bool, error) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for goos := range knownGOOS {
		if strings.HasSuffix(stem, "_"+goos) || strings.Contains(stem, "_"+goos+"_") {
			return true, nil
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//go:build ") {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil {
			return false, err
		}
		tags := map[string]struct{}{}
		constraintTags(expr, tags)
		for tag := range tags {
			if _, ok := knownGOOS[tag]; ok {
				return true, nil
			}
		}
	}
	return false, nil
}

func TestPlatformScopedCommandSourceRecognizesBuildConstraintWithoutFilenameSuffix(t *testing.T) {
	got, err := isPlatformScopedCommandSource("macextra.go", []byte("//go:build darwin\n\npackage cmd\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("darwin build constraint in non-platform filename was not classified as platform-scoped")
	}
}

type platformFlagKey struct {
	CommandPath string
	FlagName    string
}

type platformFlagRegistration struct {
	Source string
	Key    platformFlagKey
	Usage  string
}

var platformCommandPaths = map[string]string{
	"installCmd":   "tslink install",
	"uninstallCmd": "tslink uninstall",
}

func flagSetCommand(expression ast.Expr) (string, bool, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return "", false, false
	}
	method, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (method.Sel.Name != "Flags" && method.Sel.Name != "PersistentFlags") {
		return "", false, false
	}
	command, ok := method.X.(*ast.Ident)
	if !ok {
		return "", true, false
	}
	return command.Name, true, true
}

func platformSpecificFlagRegistrations(t *testing.T) []platformFlagRegistration {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "cmd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var registrations []platformFlagRegistration
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		platformScoped, err := isPlatformScopedCommandSource(name, data)
		if err != nil {
			t.Fatalf("parse platform constraint in %s: %v", path, err)
		}
		if !platformScoped {
			continue
		}
		files := token.NewFileSet()
		file, err := parser.ParseFile(files, path, data, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		flagSets := map[string]string{}
		ast.Inspect(file, func(node ast.Node) bool {
			bind := func(left ast.Expr, right ast.Expr) {
				variable, ok := left.(*ast.Ident)
				if !ok {
					return
				}
				command, isFlagSet, valid := flagSetCommand(right)
				if !isFlagSet {
					return
				}
				if !valid {
					t.Errorf("%s:%d: flag-set receiver is not a command identifier", name, files.Position(right.Pos()).Line)
					return
				}
				flagSets[variable.Name] = command
			}
			switch declaration := node.(type) {
			case *ast.AssignStmt:
				if len(declaration.Lhs) == len(declaration.Rhs) {
					for i := range declaration.Lhs {
						bind(declaration.Lhs[i], declaration.Rhs[i])
					}
				}
			case *ast.ValueSpec:
				if len(declaration.Names) == len(declaration.Values) {
					for i := range declaration.Names {
						bind(declaration.Names[i], declaration.Values[i])
					}
				}
			}
			return true
		})
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
			var commandName string
			switch receiver := method.X.(type) {
			case *ast.CallExpr:
				var isFlagSet, valid bool
				commandName, isFlagSet, valid = flagSetCommand(receiver)
				if !isFlagSet {
					return true
				}
				if !valid {
					t.Errorf("%s:%d: flag-set receiver is not a command identifier", name, files.Position(receiver.Pos()).Line)
					return true
				}
			case *ast.Ident:
				var ok bool
				commandName, ok = flagSets[receiver.Name]
				if !ok {
					return true
				}
			default:
				return true
			}
			if method.Sel.Name != "Bool" || len(call.Args) < 2 {
				t.Errorf("%s:%d: unrecognized flag registration method %s", name, files.Position(call.Pos()).Line, method.Sel.Name)
				return true
			}
			flagNameLiteral, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s:%d: unrecognized flag name for %s", name, files.Position(call.Pos()).Line, commandName)
				return true
			}
			flagName, err := strconv.Unquote(flagNameLiteral.Value)
			if err != nil {
				t.Errorf("%s:%d: decode flag name: %v", name, files.Position(call.Pos()).Line, err)
				return true
			}
			usageLiteral, ok := call.Args[len(call.Args)-1].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s:%d: unrecognized usage for %s --%s", name, files.Position(call.Pos()).Line, commandName, flagName)
				return true
			}
			usage, err := strconv.Unquote(usageLiteral.Value)
			if err != nil {
				t.Fatalf("decode %s flag usage in %s: %v", flagName, name, err)
			}
			commandPath, ok := platformCommandPaths[commandName]
			if !ok {
				t.Errorf("%s:%d: no command path registered for %s --%s", name, files.Position(call.Pos()).Line, commandName, flagName)
				commandPath = "<unrecognized:" + commandName + ">"
			}
			registrations = append(registrations, platformFlagRegistration{
				Source: name,
				Key:    platformFlagKey{CommandPath: commandPath, FlagName: flagName},
				Usage:  usage,
			})
			return true
		})
	}
	sort.Slice(registrations, func(i, j int) bool {
		left := registrations[i].Source + "\x00" + registrations[i].Key.CommandPath + "\x00" + registrations[i].Key.FlagName
		right := registrations[j].Source + "\x00" + registrations[j].Key.CommandPath + "\x00" + registrations[j].Key.FlagName
		return left < right
	})
	return registrations
}

func TestPlatformSpecificFlagRegistrationsMatchManagedDaemonSurface(t *testing.T) {
	got := platformSpecificFlagRegistrations(t)
	want := []platformFlagRegistration{
		{
			Source: "install_darwin.go",
			Key:    platformFlagKey{CommandPath: "tslink install", FlagName: "force"},
			Usage:  "Proceed with an upgrade despite an unavailable launchd domain (may start a second daemon)",
		},
		{
			Source: "install_darwin.go",
			Key:    platformFlagKey{CommandPath: "tslink install", FlagName: "no-auto-provision"},
			Usage:  "Install the managed daemon with Funnel policy auto-provisioning disabled",
		},
		{
			Source: "install_linux.go",
			Key:    platformFlagKey{CommandPath: "tslink install", FlagName: "no-auto-provision"},
			Usage:  "Install the managed daemon with Funnel policy auto-provisioning disabled",
		},
		{
			Source: "install_windows.go",
			Key:    platformFlagKey{CommandPath: "tslink install", FlagName: "no-auto-provision"},
			Usage:  "Install the managed daemon with Funnel policy auto-provisioning disabled",
		},
		{
			Source: "uninstall_darwin.go",
			Key:    platformFlagKey{CommandPath: "tslink uninstall", FlagName: "force"},
			Usage:  "Remove the plist despite an unavailable launchd domain (may leave a daemon running)",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("platform-specific flag registrations = %q, want exactly %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("platform-specific flag registrations = %q, want exactly %q", got, want)
		}
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
