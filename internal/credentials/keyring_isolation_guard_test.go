package credentials

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	goKeyringImportPath   = "github.com/zalando/go-keyring"
	credentialsImportPath = "github.com/anydoor7/tslink/internal/credentials"
	isolationFuncName     = "IsolateForTesting"
)

type goListPackage struct {
	ImportPath   string
	Dir          string
	ForTest      string
	TestGoFiles  []string
	XTestGoFiles []string
	Deps         []string
}

// keyringLinkingTestPackages lists every package in the module whose test
// binary links go-keyring. It reads only build metadata; it never touches a
// keyring.
func keyringLinkingTestPackages(t *testing.T, root string) []goListPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-e", "-test", "-json=ImportPath,Dir,ForTest,TestGoFiles,XTestGoFiles,Deps", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -test ./...: %v\n%s", err, stderr.String())
	}
	sources := map[string]goListPackage{}
	var binaries []goListPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var pkg goListPackage
		if err := dec.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		switch {
		case strings.HasSuffix(pkg.ImportPath, ".test"):
			binaries = append(binaries, pkg)
		case pkg.ForTest == "" && !strings.Contains(pkg.ImportPath, " "):
			sources[pkg.ImportPath] = pkg
		}
	}
	var linking []goListPackage
	for _, binary := range binaries {
		if !containsString(binary.Deps, goKeyringImportPath) {
			continue
		}
		underTest := strings.TrimSuffix(binary.ImportPath, ".test")
		source, ok := sources[underTest]
		if !ok {
			t.Fatalf("test binary %s links go-keyring but its package was not listed", binary.ImportPath)
		}
		linking = append(linking, source)
	}
	sort.Slice(linking, func(i, j int) bool { return linking[i].ImportPath < linking[j].ImportPath })
	return linking
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// installsKeyringIsolation reports whether one of the package's test files has
// a package-level init function that calls credentials.IsolateForTesting. An
// init function runs before TestMain and before every test in the binary.
func installsKeyringIsolation(pkg goListPackage) (bool, error) {
	files := append(append([]string(nil), pkg.TestGoFiles...), pkg.XTestGoFiles...)
	for _, name := range files {
		path := filepath.Join(pkg.Dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return false, err
		}
		qualifier := ""
		if file.Name.Name != "credentials" || pkg.ImportPath != credentialsImportPath {
			qualifier = importName(file, credentialsImportPath)
			if qualifier == "" {
				continue
			}
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != "init" || fn.Body == nil {
				continue
			}
			for _, stmt := range fn.Body.List {
				expr, ok := stmt.(*ast.ExprStmt)
				if !ok {
					continue
				}
				call, ok := expr.X.(*ast.CallExpr)
				if !ok {
					continue
				}
				if callsIsolation(call.Fun, qualifier) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func importName(file *ast.File, path string) string {
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil || value != path {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

func callsIsolation(fun ast.Expr, qualifier string) bool {
	if qualifier == "" {
		ident, ok := fun.(*ast.Ident)
		return ok && ident.Name == isolationFuncName
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != isolationFuncName {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == qualifier
}

func moduleRoot(t *testing.T) string {
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
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// TestEveryKeyringLinkingTestBinaryInstallsIsolation fails closed: it reads
// build metadata and source only, so a missing isolation file is reported
// without any call to the real OS keyring. go-keyring's MockInit is a
// process-wide switch, so a binary without it reads the operator's real
// credentials through /usr/bin/security, secret-service, or Credential Manager.
func TestEveryKeyringLinkingTestBinaryInstallsIsolation(t *testing.T) {
	linking := keyringLinkingTestPackages(t, moduleRoot(t))
	var paths []string
	for _, pkg := range linking {
		paths = append(paths, pkg.ImportPath)
	}
	// Control: this package's own test binary links go-keyring. If the scan
	// cannot see it, the scan has no discriminating power.
	if !containsString(paths, credentialsImportPath) {
		t.Fatalf("scan did not list %s among go-keyring test binaries: %v", credentialsImportPath, paths)
	}
	var missing []string
	for _, pkg := range linking {
		ok, err := installsKeyringIsolation(pkg)
		if err != nil {
			t.Fatalf("parse test files of %s: %v", pkg.ImportPath, err)
		}
		if !ok {
			missing = append(missing, pkg.ImportPath)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("test binaries link go-keyring without installing its mock before TestMain: %s\n"+
			"add a _test.go file with `func init() { credentials.%s() }` to each package",
			strings.Join(missing, ", "), isolationFuncName)
	}
	t.Logf("%d go-keyring test binaries install isolation: %s", len(paths), strings.Join(paths, ", "))
}
