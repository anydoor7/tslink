package testenv

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const testenvImportPath = "github.com/monody0007/tslink/internal/testenv"

// isolationGuardGOOS are the platforms whose test binaries must all run Main.
// A package can have tests on some platforms only, so the check is per
// platform, with each platform's own file set.
var isolationGuardGOOS = []string{"darwin", "linux", "windows"}

// TestEveryTestPackageRunsTheSharedIsolation (G1) keeps Main's coverage
// complete. Main only protects the binaries whose TestMain calls it, and the
// failure it prevents (a test reading the contributor's real config, home or
// credentials) is silent. So for every directory of this module that has
// _test.go files, and for each platform, the files built there must declare
// one TestMain that
//
//   - calls testenv.Main with its *testing.M, and
//   - uses that *testing.M nowhere else except inside the arguments of
//     testenv.Main or testenv.RealHostMain, so no test can run before or
//     outside the isolation;
//
// and no other function in those files may take a *testing.M.
func TestEveryTestPackageRunsTheSharedIsolation(t *testing.T) {
	root := repoRootForTest(t)

	var problems []string
	checked := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
			return filepath.SkipDir
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		var testFiles []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
				testFiles = append(testFiles, entry.Name())
			}
		}
		if len(testFiles) == 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		for _, goos := range isolationGuardGOOS {
			ctx := build.Default
			ctx.GOOS = goos
			ctx.GOARCH = "arm64"
			ctx.CgoEnabled = true
			var built []string
			for _, file := range testFiles {
				ok, err := ctx.MatchFile(path, file)
				if err != nil {
					return err
				}
				if ok {
					built = append(built, file)
				}
			}
			if len(built) == 0 {
				continue
			}
			checked[rel] = true
			problems = append(problems, checkIsolatedTestMain(t, path, rel, goos, built)...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	// Control: the walk has to have found the packages it is meant to cover,
	// or an empty scan would pass for a clean one.
	for _, want := range []string{".", "cmd", "internal/server", "internal/config", "internal/testenv", "tools/gen-notices"} {
		if !checked[want] {
			t.Fatalf("isolation guard never examined %s; the walk is broken, so a clean result would mean nothing (examined: %v)", want, sortedSet(checked))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d finding(s): these test binaries can run tests outside testenv.Main, so they may read the contributor's real home, config or credentials:\n  %s\n"+
			"Give the package a TestMain that returns testenv.Main(m, run) (see internal/testenv/isolation.go).",
			len(problems), strings.Join(problems, "\n  "))
	}
}

func sortedSet(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// checkIsolatedTestMain inspects one package's test files as built for goos.
func checkIsolatedTestMain(t *testing.T, dir, rel, goos string, files []string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var problems []string
	testMains := 0
	for _, name := range files {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s/%s: %v", rel, name, err)
		}
		testenvName := testenvLocalName(file)
		where := rel + "/" + name
		ast.Inspect(file, func(n ast.Node) bool {
			var ftype *ast.FuncType
			var body *ast.BlockStmt
			isTestMain := false
			switch fn := n.(type) {
			case *ast.FuncDecl:
				ftype, body = fn.Type, fn.Body
				isTestMain = fn.Recv == nil && fn.Name.Name == "TestMain"
			case *ast.FuncLit:
				ftype, body = fn.Type, fn.Body
			default:
				return true
			}
			param := testingMParam(ftype)
			if param == "" {
				return true
			}
			if !isTestMain {
				problems = append(problems, where+" ["+goos+"]: a function other than TestMain takes a *testing.M")
				return true
			}
			testMains++
			problems = append(problems, checkTestMainBody(fset, where, goos, body, param, testenvName)...)
			return true
		})
	}
	if testMains == 0 {
		problems = append(problems, rel+" ["+goos+"]: no TestMain in "+strings.Join(files, ", "))
	}
	return problems
}

// testenvLocalName returns how file refers to package testenv: "" when the
// file is in package testenv itself, the import's local name otherwise, and
// "-" when the file does not import it.
func testenvLocalName(file *ast.File) string {
	if file.Name.Name == "testenv" {
		return ""
	}
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) != testenvImportPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return "testenv"
	}
	return "-"
}

// testingMParam returns the name of ftype's *testing.M parameter, if any.
func testingMParam(ftype *ast.FuncType) string {
	if ftype.Params == nil {
		return ""
	}
	for _, field := range ftype.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "M" {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "testing" {
			continue
		}
		if len(field.Names) == 0 {
			return "_"
		}
		return field.Names[0].Name
	}
	return ""
}

func checkTestMainBody(fset *token.FileSet, where, goos string, body *ast.BlockStmt, param, testenvName string) []string {
	if body == nil {
		return []string{where + " [" + goos + "]: TestMain has no body"}
	}
	isEntry := func(call *ast.CallExpr, names ...string) bool {
		var fn string
		switch f := call.Fun.(type) {
		case *ast.Ident:
			if testenvName != "" {
				return false
			}
			fn = f.Name
		case *ast.SelectorExpr:
			pkg, ok := f.X.(*ast.Ident)
			if !ok || testenvName == "" || pkg.Name != testenvName {
				return false
			}
			fn = f.Sel.Name
		default:
			return false
		}
		for _, name := range names {
			if fn == name {
				return true
			}
		}
		return false
	}

	var entries []*ast.CallExpr
	mainCalls := 0
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isEntry(call, "Main", "RealHostMain") {
			return true
		}
		entries = append(entries, call)
		if len(call.Args) > 0 {
			if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == param && isEntry(call, "Main") {
				mainCalls++
			}
		}
		return true
	})

	var problems []string
	if param == "_" {
		return []string{where + " [" + goos + "]: TestMain discards its *testing.M"}
	}
	if mainCalls == 0 {
		problems = append(problems, where+" ["+goos+"]: TestMain never calls testenv.Main("+param+", ...)")
	}
	ast.Inspect(body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != param {
			return true
		}
		for _, call := range entries {
			if ident.Pos() > call.Lparen && ident.Pos() < call.Rparen {
				return true
			}
		}
		problems = append(problems, where+" ["+goos+"]: TestMain uses "+param+" outside testenv.Main at line "+
			strconv.Itoa(fset.Position(ident.Pos()).Line))
		return true
	})
	return problems
}
