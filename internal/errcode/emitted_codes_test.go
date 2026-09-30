package errcode

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The scanner reads every non-test Go file of the module, for every platform,
// and collects each stable error code the product can emit:
//
//   - string constants named Code<Something> used in returns (registry.CodeX,
//     config.CodeX, ...); declarations alone are only resolution targets;
//   - the Code field of every error-code carrier literal (CodedError,
//     StableCodeError, SnapshotError, the runtime ServiceError, ...), resolved
//     through constants and through local variables assigned in the same
//     function;
//   - the return value of every StableCode method.
//
// A Code value it cannot resolve to a constant is accepted only when it
// forwards a code something else already carries (x.Code, x.StableCode(),
// registry.ErrorCode(err), a parameter); anything else is reported, so a code
// built at run time cannot slip past the table.

// codeCarriers are the struct types whose Code field is a stable error code
// an agent or script can read.
var codeCarriers = map[string]bool{
	"CodedError":                  true, // registry
	"StableCodeError":             true, // registry
	"SnapshotError":               true, // runtime
	"Freshness":                   true, // runtime: status's runtime_snapshot.code
	"ServiceError":                true, // runtime: a service's failure in status and doctor
	"InviteTargetError":           true, // tailapi: a per-target failure in invite list
	"RegistryCheckIssue":          true, // cmd: registry check issues
	"ErrorObject":                 true, // output: the envelope's error object
	"StatusRuntimeSnapshotResult": true, // cmd: forwards runtime freshness codes
}

// These Code fields describe table metadata or diagnostics, not emitted error
// codes. Keep the exceptions qualified and explicit so a new carrier cannot
// silently escape the registration check.
var nonErrorCodeFields = map[string]string{
	"internal/errcode.Code":        "the table itself, not an emission",
	"internal/inspect.WarningView": "warning namespace, not error exits",
	"cmd.DoctorFinding":            "doctor diagnostics, not error exits",
}

var codeConstName = regexp.MustCompile(`^Code[A-Z]`)

type emittedCode struct {
	Code string
	Pos  string
}

type scannedPackage struct {
	path  string
	files []*ast.File
	// consts maps a constant name to the expression and file it is declared
	// with, for resolution.
	consts  map[string]constDecl
	structs map[string]*ast.StructType
}

type constDecl struct {
	expr ast.Expr
	file *ast.File
}

type moduleScanner struct {
	fset     *token.FileSet
	module   string
	pkgs     map[string]*scannedPackage
	resolved map[string]string
	emitted  []emittedCode
	problems []string
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

func modulePath(t *testing.T, root string) string {
	t.Helper()
	file, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatal("go.mod has no module line")
	return ""
}

// scanModule parses the module's product sources under root.
func scanModule(t *testing.T, root string) *moduleScanner {
	t.Helper()
	s := &moduleScanner{
		fset:     token.NewFileSet(),
		module:   modulePath(t, root),
		pkgs:     map[string]*scannedPackage{},
		resolved: map[string]string{},
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(s.fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		importPath := s.module
		if rel != "." {
			importPath += "/" + filepath.ToSlash(rel)
		}
		pkg := s.pkgs[importPath]
		if pkg == nil {
			pkg = &scannedPackage{path: importPath, consts: map[string]constDecl{}, structs: map[string]*ast.StructType{}}
			s.pkgs[importPath] = pkg
		}
		pkg.files = append(pkg.files, file)
		ast.Inspect(file, func(n ast.Node) bool {
			if spec, ok := n.(*ast.TypeSpec); ok {
				if st, ok := spec.Type.(*ast.StructType); ok {
					pkg.structs[spec.Name.Name] = st
				}
			}
			return true
		})
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if i < len(value.Values) {
						pkg.consts[name.Name] = constDecl{expr: value.Values[i], file: file}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// importPathFor returns the import path a file refers to by name.
func (s *moduleScanner) importPathFor(file *ast.File, name string) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := ""
		if spec.Name != nil {
			local = spec.Name.Name
		} else if pkg := s.pkgs[path]; pkg != nil && len(pkg.files) > 0 {
			local = pkg.files[0].Name.Name
		} else {
			local = path[strings.LastIndex(path, "/")+1:]
		}
		if local == name {
			return path, true
		}
	}
	return "", false
}

// constString resolves a string constant of the module.
func (s *moduleScanner) constString(pkgPath, name string, depth int) (string, bool) {
	key := pkgPath + "." + name
	if value, ok := s.resolved[key]; ok {
		return value, true
	}
	pkg := s.pkgs[pkgPath]
	if pkg == nil || depth > 16 {
		return "", false
	}
	decl, ok := pkg.consts[name]
	if !ok {
		return "", false
	}
	value, ok := s.constExpr(pkgPath, decl.file, decl.expr, depth+1)
	if ok {
		s.resolved[key] = value
	}
	return value, ok
}

func (s *moduleScanner) constExpr(pkgPath string, file *ast.File, expr ast.Expr, depth int) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.Ident:
		return s.constString(pkgPath, e.Name, depth)
	case *ast.SelectorExpr:
		pkgIdent, ok := e.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		path, ok := s.importPathFor(file, pkgIdent.Name)
		if !ok {
			return "", false
		}
		return s.constString(path, e.Sel.Name, depth)
	case *ast.ParenExpr:
		return s.constExpr(pkgPath, file, e.X, depth)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := s.constExpr(pkgPath, file, e.X, depth)
		if !ok {
			return "", false
		}
		right, ok := s.constExpr(pkgPath, file, e.Y, depth)
		return left + right, ok
	}
	return "", false
}

func (s *moduleScanner) emit(code string, pos token.Pos) {
	s.emitted = append(s.emitted, emittedCode{Code: code, Pos: s.position(pos)})
}

func (s *moduleScanner) position(pos token.Pos) string {
	p := s.fset.Position(pos)
	return fmt.Sprintf("%s:%d", filepath.Base(filepath.Dir(p.Filename))+"/"+filepath.Base(p.Filename), p.Line)
}

// codeValue records the codes a Code expression can hold.
func (s *moduleScanner) codeValue(pkgPath string, file *ast.File, fn *ast.FuncDecl, expr ast.Expr, seen map[string]bool) {
	if value, ok := s.constExpr(pkgPath, file, expr, 0); ok {
		s.emit(value, expr.Pos())
		return
	}
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		// x.Code forwards a code something already carries.
		if _, isPkg := e.X.(*ast.Ident); isPkg {
			if _, ok := s.importPathFor(file, e.X.(*ast.Ident).Name); ok {
				s.problems = append(s.problems, fmt.Sprintf("%s: code %s is not a string constant of this module", s.position(e.Pos()), render(e)))
			}
		}
		return
	case *ast.CallExpr:
		if sel, ok := e.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "StableCode" || sel.Sel.Name == "ErrorCode" || sel.Sel.Name == "StableErrorCode") {
			// Forwarded, or one of the generic per-exit codes.
			return
		}
		if ident, ok := e.Fun.(*ast.Ident); ok && (ident.Name == "StableErrorCode" || ident.Name == "string") {
			return
		}
		s.problems = append(s.problems, fmt.Sprintf("%s: code comes from %s, which the table check cannot follow", s.position(e.Pos()), render(e)))
		return
	case *ast.Ident:
		if fn == nil || seen[e.Name] {
			return
		}
		seen[e.Name] = true
		if isParam(fn, e.Name) {
			return
		}
		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch st := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range st.Lhs {
					id, ok := lhs.(*ast.Ident)
					if !ok || id.Name != e.Name {
						continue
					}
					found = true
					if len(st.Rhs) == len(st.Lhs) {
						s.codeValue(pkgPath, file, fn, st.Rhs[i], seen)
					}
					// A multi-value call (code, ok := registry.ErrorCode(err))
					// forwards a code.
				}
			case *ast.ValueSpec:
				for i, id := range st.Names {
					if id.Name == e.Name {
						found = true
						if i < len(st.Values) {
							s.codeValue(pkgPath, file, fn, st.Values[i], seen)
						}
					}
				}
			case *ast.RangeStmt:
				for _, x := range []ast.Expr{st.Key, st.Value} {
					if id, ok := x.(*ast.Ident); ok && id.Name == e.Name {
						found = true
					}
				}
			}
			return true
		})
		if !found {
			s.problems = append(s.problems, fmt.Sprintf("%s: code variable %s has no assignment the table check can follow", s.position(e.Pos()), e.Name))
		}
		return
	}
	s.problems = append(s.problems, fmt.Sprintf("%s: code expression %s is not a constant the table check can follow", s.position(expr.Pos()), render(expr)))
}

func isParam(fn *ast.FuncDecl, name string) bool {
	for _, list := range []*ast.FieldList{fn.Recv, fn.Type.Params} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			for _, id := range field.Names {
				if id.Name == name {
					return true
				}
			}
		}
	}
	return false
}

func render(expr ast.Expr) string {
	var b strings.Builder
	ast.Fprint(&b, token.NewFileSet(), expr, nil)
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if x, ok := sel.X.(*ast.Ident); ok {
			return x.Name + "." + sel.Sel.Name
		}
	}
	if call, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			return render(sel) + "(...)"
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			return id.Name + "(...)"
		}
	}
	return fmt.Sprintf("%T", expr)
}

func carrierName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// codeFieldIndex follows the declaration, including grouped and embedded
// fields: Code need not be the first field of a positional literal.
func codeFieldIndex(st *ast.StructType) (int, bool) {
	index := 0
	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			if typ, ok := field.Type.(*ast.Ident); ok && name.Name == "Code" && typ.Name == "string" {
				return index, true
			}
			index++
		}
		if len(field.Names) == 0 {
			index++
		}
	}
	return 0, false
}

func (s *moduleScanner) carrierCodeIndex(pkgPath string, file *ast.File, typ ast.Expr) (int, bool) {
	if sel, ok := typ.(*ast.SelectorExpr); ok {
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return 0, false
		}
		pkgPath, ok = s.importPathFor(file, ident.Name)
		if !ok {
			return 0, false
		}
	}
	pkg := s.pkgs[pkgPath]
	if pkg == nil {
		return 0, false
	}
	st := pkg.structs[carrierName(typ)]
	if st == nil {
		return 0, false
	}
	return codeFieldIndex(st)
}

func (s *moduleScanner) checkCarrierRegistrations(pkg *scannedPackage) {
	for name, st := range pkg.structs {
		if _, hasCode := codeFieldIndex(st); !hasCode || codeCarriers[name] {
			continue
		}
		qualified := strings.TrimPrefix(pkg.path, s.module+"/") + "." + name
		if _, exempt := nonErrorCodeFields[qualified]; exempt {
			continue
		}
		s.problems = append(s.problems, fmt.Sprintf("%s: unregistered Code string carrier %s", s.position(st.Pos()), qualified))
	}
}

// collect finds every emitted code.
func (s *moduleScanner) collect() {
	paths := make([]string, 0, len(s.pkgs))
	for path := range s.pkgs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		pkg := s.pkgs[path]
		s.checkCarrierRegistrations(pkg)
		for _, file := range pkg.files {
			for _, decl := range file.Decls {
				fn, _ := decl.(*ast.FuncDecl)
				if fn != nil && fn.Name.Name == "StableCode" && fn.Recv != nil && fn.Body != nil {
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						if ret, ok := n.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
							s.codeValue(path, file, fn, ret.Results[0], map[string]bool{})
						}
						return true
					})
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					if ret, ok := n.(*ast.ReturnStmt); ok {
						for _, result := range ret.Results {
							// A named code returned directly is an emission, unlike
							// its declaration or an entry in the metadata table.
							if codeConstName.MatchString(carrierName(result)) {
								s.codeValue(path, file, fn, result, map[string]bool{})
							}
						}
					}
					lit, ok := n.(*ast.CompositeLit)
					if !ok || !codeCarriers[carrierName(lit.Type)] {
						return true
					}
					index, resolved := s.carrierCodeIndex(path, file, lit.Type)
					for i, elt := range lit.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							if !resolved {
								s.problems = append(s.problems, fmt.Sprintf("%s: cannot locate Code field of %s", s.position(lit.Pos()), carrierName(lit.Type)))
								break
							}
							if i == index {
								s.codeValue(path, file, fn, elt, map[string]bool{})
							}
							continue
						}
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
							s.codeValue(path, file, fn, kv.Value, map[string]bool{})
						}
					}
					return true
				})
			}
		}
	}
}

func scanEmittedCodes(t *testing.T, root string) *moduleScanner {
	t.Helper()
	s := scanModule(t, root)
	s.collect()
	return s
}

func (s *moduleScanner) sites() map[string][]string {
	sites := map[string][]string{}
	for _, e := range s.emitted {
		if e.Code == "" {
			// A nil receiver's StableCode, not a code.
			continue
		}
		sites[e.Code] = append(sites[e.Code], e.Pos)
	}
	return sites
}

// TestEveryEmittedCodeIsInTheTable is the guard A3-4 and A4-3 asked for: a
// code the product can emit and the table does not list would exit 1 by
// default and never reach the manifest.
func TestEveryEmittedCodeIsInTheTable(t *testing.T) {
	s := scanEmittedCodes(t, moduleRoot(t))
	for _, problem := range s.problems {
		t.Errorf("%s", problem)
	}
	sites := s.sites()
	// Control: the scan sees a literal code (cmd/daemon_setup.go), a
	// constant (registry.CodePathNotFound), a local variable
	// (tailapi.inviteTargetError) and a StableCode method (config).
	for _, known := range []string{"daemon_not_running", "path_not_found", InternalError, "legacy_config_dir_present"} {
		if len(sites[known]) == 0 {
			t.Fatalf("scan did not find %s; it is blind", known)
		}
	}
	var missing []string
	for code, where := range sites {
		if _, ok := Lookup(code); !ok {
			missing = append(missing, fmt.Sprintf("%s (emitted at %s)", code, strings.Join(where, ", ")))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("codes emitted by the product but missing from the errcode table:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestEveryTableCodeIsEmitted keeps the table from declaring a code nothing
// raises, which the manifest would then promise to agents.
func TestEveryTableCodeIsEmitted(t *testing.T) {
	sites := scanEmittedCodes(t, moduleRoot(t)).sites()
	for _, row := range All() {
		switch row.Code {
		case UsageError, AuthError, NotFound:
			// Generic classes: every CodeError of that exit carries one.
			continue
		}
		if len(sites[row.Code]) == 0 {
			t.Errorf("table row %s is emitted nowhere in the product", row.Code)
		}
	}
}

// TestScannerFindsEveryKindOfEmission runs the scanner over a small module
// with one code per emission form, so its reach does not rest on what the
// product happens to contain today.
func TestScannerFindsEveryKindOfEmission(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"codes/codes.go": `package codes

const CodeDeclared = "declared_constant"
const aliasTarget = "via_alias"
const CodeAlias = aliasTarget
`,
		"p/p.go": `package p

import (
	"errors"

	c "example.com/m/codes"
)

type CodedError struct{ Message string; Code string }

func (CodedError) Error() string { return "" }

type own struct{}

func (own) StableCode() string { return "from_stable_code" }

func Literal() error { return CodedError{Code: "literal_code"} }

func Imported() error { return &CodedError{Code: c.CodeAlias} }

func Returned() string { return c.CodeDeclared }

func Positional() error { return CodedError{"message_not_code", "positional_code"} }

func Local(err error) error {
	code := "local_default"
	if errors.Is(err, errors.ErrUnsupported) {
		code = "local_branch"
	}
	return CodedError{Code: code}
}

func Forwarded(e CodedError) error { return CodedError{Code: e.Code} }

func Built(name string) error { return CodedError{Code: name + "_x"} }
`,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := scanEmittedCodes(t, root)
	sites := s.sites()
	for _, want := range []string{"declared_constant", "via_alias", "from_stable_code", "literal_code", "local_default", "local_branch", "positional_code"} {
		if len(sites[want]) == 0 {
			t.Errorf("scanner missed %s; found %v", want, sites)
		}
	}
	if len(sites["message_not_code"]) != 0 {
		t.Error("scanner assumed Code was the first field")
	}
	if len(s.problems) != 1 || !strings.Contains(s.problems[0], "p.go") {
		t.Fatalf("problems = %q, want exactly the run-time code built in Built", s.problems)
	}
}
