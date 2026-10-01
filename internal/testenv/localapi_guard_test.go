package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// localAPIClientTypes maps an import path to the name under which it exports
// the Tailscale LocalAPI client type (tailscale.com's own type, its deprecated
// alias, and TSLink's alias in internal/server).
var localAPIClientTypes = map[string]string{
	"tailscale.com/client/local":                 "Client",
	"tailscale.com/client/tailscale":             "LocalClient",
	"github.com/anydoor7/tslink/internal/server": "LocalClient",
}

// TestNoTestBuildsALocalAPIClientThatCanReachTheHost (G2). A LocalAPI client
// talks to the tailscaled of the machine the tests run on unless it has both
// a stub Transport and OmitAuth: without OmitAuth the upstream client looks
// up the host's LocalAPI token before every request (lsof and a token file
// read on macOS, systemctl and a /proc scan on Linux), even when Transport is
// replaced. A zero value has neither, and a fake that "is never called" is
// one refactor away from being called.
//
// So in every _test.go file, a LocalAPI client may only be built with both
// fields set, which is what localapitest.NewClient does; the zero forms
// (T{}, new(T), var x T) and literals missing either field are refused.
func TestNoTestBuildsALocalAPIClientThatCanReachTheHost(t *testing.T) {
	root := repoRootForTest(t)
	var problems []string
	failClosed := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		found, ok := scanLocalAPIClients(t, path, filepath.ToSlash(rel))
		problems = append(problems, found...)
		failClosed += ok
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	// Control: the scan must recognise the fail-closed clients the suite
	// does build, or a scanner that matches nothing would pass as clean.
	if failClosed == 0 {
		t.Fatal("the scan recognised no fail-closed LocalAPI client literal at all; it is not matching the client type, so a clean result would mean nothing")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d LocalAPI client(s) built in tests can reach the host's tailscaled:\n  %s\n"+
			"Use localapitest.NewClient(stub) (internal/testenv/localapitest), which sets OmitAuth and a stub Transport.",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// scanLocalAPIClients returns the unsafe constructions in one file and the
// number of fail-closed literals it saw.
func scanLocalAPIClients(t *testing.T, path, rel string) ([]string, int) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	qualified := map[string]string{} // local import name -> client type name
	for _, spec := range file.Imports {
		importPath, _ := strconv.Unquote(spec.Path.Value)
		typeName, ok := localAPIClientTypes[importPath]
		if !ok {
			continue
		}
		name := importPath[strings.LastIndex(importPath, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		qualified[name] = typeName
	}
	// Inside package server the alias is unqualified.
	bare := ""
	if strings.HasPrefix(rel, "internal/server/") && file.Name.Name == "server" {
		bare = "LocalClient"
	}
	isClient := func(expr ast.Expr) bool {
		switch e := expr.(type) {
		case *ast.Ident:
			return bare != "" && e.Name == bare
		case *ast.SelectorExpr:
			pkg, ok := e.X.(*ast.Ident)
			return ok && qualified[pkg.Name] != "" && qualified[pkg.Name] == e.Sel.Name
		}
		return false
	}
	var problems []string
	failClosed := 0
	at := func(pos token.Pos, what string) {
		problems = append(problems, rel+":"+strconv.Itoa(fset.Position(pos).Line)+": "+what)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if node.Type == nil || !isClient(node.Type) {
				return true
			}
			omitAuth, transport := false, false
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				value, isIdent := kv.Value.(*ast.Ident)
				switch key.Name {
				case "OmitAuth":
					omitAuth = isIdent && value.Name == "true"
				case "Transport":
					transport = !isIdent || value.Name != "nil"
				}
			}
			if omitAuth && transport {
				failClosed++
				return true
			}
			at(node.Pos(), "LocalAPI client literal without both OmitAuth: true and a stub Transport")
		case *ast.CallExpr:
			if fn, ok := node.Fun.(*ast.Ident); ok && fn.Name == "new" && len(node.Args) == 1 && isClient(node.Args[0]) {
				at(node.Pos(), "new() of a LocalAPI client (zero value)")
			}
		case *ast.ValueSpec:
			if node.Type != nil && isClient(node.Type) && len(node.Values) == 0 {
				at(node.Pos(), "var of a LocalAPI client type (zero value)")
			}
		case *ast.TypeSpec:
			if isClient(node.Type) {
				at(node.Pos(), "type declared over a LocalAPI client, which this scan cannot follow")
			}
		}
		return true
	})
	return problems, failClosed
}
