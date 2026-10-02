package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Parse every platform, including files excluded from the host build. The graph
// includes function literals and package-level function aliases (the manager
// seams). Any new manager site must be classified; any caller on an MCP path
// to one of those sites must retain its incoming context.
func TestMCPManagerCallInventory(t *testing.T) {
	type node struct {
		file, name string
		decl       *ast.FuncDecl
	}
	fs := token.NewFileSet()
	nodes := map[string][]node{}
	aliases := map[string]string{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		file := entry.Name()
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(fs, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range tree.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				nodes[fn.Name.Name] = append(nodes[fn.Name.Name], node{file, fn.Name.Name, fn})
			}
			if gen, ok := decl.(*ast.GenDecl); ok {
				for _, spec := range gen.Specs {
					if v, ok := spec.(*ast.ValueSpec); ok {
						for i, value := range v.Values {
							if i < len(v.Names) {
								aliases[v.Names[i].Name] = inventoryCallName(value)
								if lit, ok := value.(*ast.FuncLit); ok {
									name := v.Names[i].Name
									nodes[name] = append(nodes[name], node{file, name, &ast.FuncDecl{Name: ast.NewIdent(name), Type: lit.Type, Body: lit.Body}})
								}
							}
						}
					}
				}
			}
		}
	}
	manager := map[string]bool{"detectSupervisionFn": true, "managerOutputFn": true, "windowsSchedulerFn": true, "launchctlCombinedOutput": true, "systemctlCombinedOutput": true, "loginctlCombinedOutputFn": true, "runBoundedManagerCommandContext": true}
	// The reviewer inventory also lists default wrappers. They now require a
	// context; they cannot be used as a context-free bridge from another caller.
	wrappers := []string{"detectSupervision", "runBoundedManagerCommand"}
	// Compensation is deliberately independent of a failed request's lifetime.
	// These exact functions restore captured prior state or disable an uncertain
	// replacement; uninstall is available only through the CLI command tree.
	compensation := map[string]bool{"rollbackNewLaunchAgent": true, "restorePreviousLaunchAgent": true, "restorePreviousSystemdUnit": true}
	roots := []string{"callMCPTool", "defaultMCPActions", "mcpEventsSnapshotFn"}
	reachable := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if reachable[name] {
			return
		}
		reachable[name] = true
		if alias := aliases[name]; alias != "" {
			visit(alias)
		}
		for _, n := range nodes[name] {
			ast.Inspect(n.decl.Body, func(a ast.Node) bool {
				if call, ok := a.(*ast.CallExpr); ok {
					visit(inventoryCallName(call.Fun))
				}
				return true
			})
		}
	}
	for _, root := range roots {
		if len(nodes[root]) == 0 {
			t.Fatalf("missing MCP root %s", root)
		}
		visit(root)
	}
	// Work backwards from every manager boundary to find the entire caller chain.
	leads := map[string]bool{}
	for name := range manager {
		leads[name] = true
	}
	changed := true
	for changed {
		changed = false
		for alias, target := range aliases {
			if leads[target] && !leads[alias] {
				leads[alias] = true
				changed = true
			}
		}
		for name, list := range nodes {
			if leads[name] {
				continue
			}
			for _, n := range list {
				ast.Inspect(n.decl.Body, func(a ast.Node) bool {
					if call, ok := a.(*ast.CallExpr); ok && leads[inventoryCallName(call.Fun)] {
						leads[name] = true
						changed = true
					}
					return true
				})
			}
		}
	}
	// Pin all file/function pairs in the supplied inventory. Declaration rows
	// (the wrappers) are checked separately above; process adapters are included.
	expected := map[string]int{
		"doctor.go/buildDoctorResult/detectSupervisionFn":                              1,
		"status.go/baseStatus/detectSupervisionFn":                                     1,
		"daemon_setup.go/ensureDaemon/detectSupervisionFn":                             2,
		"supervision_unix.go/managerOutputChecked/managerOutputFn":                     1,
		"install_windows.go/installWindowsTask/windowsSchedulerFn":                     2,
		"install_windows.go/windowsSchedulerChecked/windowsSchedulerFn":                1,
		"supervision_linux.go/linuxLingerState/loginctlCombinedOutputFn":               1,
		"install_linux.go/linuxLingerWarning/loginctlCombinedOutputFn":                 1,
		"install_linux.go/systemctlCombinedOutputChecked/systemctlCombinedOutput":      1,
		"install_darwin.go/launchctlCombinedOutputChecked/launchctlCombinedOutput":     1,
		"install_darwin.go/restorePreviousLaunchAgent/launchctlCombinedOutput":         1,
		"install_linux.go/restorePreviousSystemdUnit/systemctlCombinedOutput":          4,
		"uninstall_linux.go/runUninstallLocked/systemctlCombinedOutput":                3,
		"uninstall_linux.go/confirmSystemdStoppedAfterError/systemctlCombinedOutput":   1,
		"uninstall_linux.go/systemdUnitLeftBehind/systemctlCombinedOutput":             1,
		"uninstall_linux.go/resetFailedSystemdServiceWarning/systemctlCombinedOutput":  2,
		"uninstall_windows.go/runUninstallLocked/windowsSchedulerFn":                   5,
		"uninstall_darwin.go/bootoutLaunchAgent/launchctlCombinedOutput":               1,
		"daemon_setup.go/boundedManagerOutput/runBoundedManagerCommandContext":         1,
		"install_darwin.go/launchctlCombinedOutput/runBoundedManagerCommandContext":    1,
		"install_linux.go/systemctlCombinedOutput/runBoundedManagerCommandContext":     1,
		"install_linux.go/loginctlCombinedOutputFn/runBoundedManagerCommandContext":    1,
		"supervision_windows.go/callWindowsScheduler/runBoundedManagerCommandContext":  2,
		"supervision_unix.go/runBoundedManagerCommand/runBoundedManagerCommandContext": 1,
	}
	actual := map[string]int{}
	for name, list := range nodes {
		for _, n := range list {
			allowedBackground := map[token.Pos]bool{}
			ast.Inspect(n.decl.Body, func(a ast.Node) bool {

				// Cobra installers are real CLI boundaries. MCP bootstrap explicitly sets
				// cmd.Context; only a nil CLI context may use this default.
				if branch, ok := a.(*ast.IfStmt); ok && name == "runInstallLocked" {
					if cond, ok := branch.Cond.(*ast.BinaryExpr); ok && cond.Op == token.EQL && (inventoryCallName(cond.X) == "ctx" || inventoryNilCommandContext(cond.X)) && inventoryCallName(cond.Y) == "nil" {
						ast.Inspect(branch.Body, func(b ast.Node) bool {
							if call, ok := b.(*ast.CallExpr); ok && inventoryCallName(call.Fun) == "Background" {
								allowedBackground[call.Pos()] = true
							}
							return true
						})
					}
				}
				return true
			})
			ast.Inspect(n.decl.Body, func(a ast.Node) bool {
				call, ok := a.(*ast.CallExpr)
				if !ok {
					return true
				}
				target := inventoryCallName(call.Fun)
				if target == "managerCompensationContext" {
					windowsDisable := n.file == "install_windows.go" && name == "installWindowsTask"
					if !compensation[name] && !windowsDisable {
						t.Errorf("%s: compensation context outside captured-state cleanup", fs.Position(call.Pos()))
					}
					if len(call.Args) != 1 || inventoryCallName(call.Args[0]) != "ctx" {
						t.Errorf("%s: compensation lost its caller", fs.Position(call.Pos()))
					}
				}
				if manager[target] {
					key := n.file + "/" + name + "/" + target
					actual[key]++
					cli := strings.HasPrefix(n.file, "uninstall_")
					if reachable[name] && !cli {
						if len(call.Args) == 0 || !inventoryCallerContext(call.Args[0]) || !inventoryHasContextSource(n.decl) {
							t.Errorf("%s: manager context is not the caller parameter", fs.Position(call.Pos()))
						}
					}
				}
				// This catches context loss anywhere above the direct query adapter,
				// including a new context-free helper introduced after this inventory.
				if reachable[name] && leads[name] && (target == "Background" || target == "TODO") {
					// Defaults are restricted to the nil-context CLI boundary.
					if !allowedBackground[call.Pos()] {
						t.Errorf("%s: MCP manager caller resets context", fs.Position(call.Pos()))
					}
				}
				return true
			})
		}
	}
	for key, count := range expected {
		if actual[key] != count {
			t.Errorf("inventory %s: sites=%d want=%d", key, actual[key], count)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			t.Errorf("unclassified manager site %s", key)
		}
	}
	for _, name := range wrappers {
		for _, n := range nodes[name] {
			if n.decl.Type.Params == nil || len(n.decl.Type.Params.List) == 0 || inventoryCallName(n.decl.Type.Params.List[0].Type) != "Context" {
				t.Errorf("%s: wrapper lacks context parameter", filepath.Join(n.file, name))
			}
		}
	}
	t.Logf("inventory: %d classified file/function/adapter rows; %d MCP-reachable symbols", len(actual), len(reachable))
}

func inventoryCallName(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

func inventoryNilCommandContext(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Context" && inventoryCallName(sel.X) == "cmd"
}

func inventoryCallerContext(expr ast.Expr) bool {
	if inventoryCallName(expr) == "ctx" {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	return ok && inventoryCallName(call.Fun) == "managerCompensationContext" && len(call.Args) == 1 && inventoryCallName(call.Args[0]) == "ctx"
}

func inventoryHasContextSource(fn *ast.FuncDecl) bool {
	for _, p := range fn.Type.Params.List {
		if sel, ok := p.Type.(*ast.SelectorExpr); ok && inventoryCallName(sel.X) == "context" && sel.Sel.Name == "Context" {
			for _, name := range p.Names {
				if name.Name == "ctx" {
					return true
				}
			}
		}
	}
	// The concrete command adapters obtain their caller from Cobra.
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			for _, p := range lit.Type.Params.List {
				if sel, ok := p.Type.(*ast.SelectorExpr); ok && inventoryCallName(sel.X) == "context" && sel.Sel.Name == "Context" {
					for _, name := range p.Names {
						if name.Name == "ctx" {
							found = true
						}
					}
				}
			}
		}
		if assign, ok := n.(*ast.AssignStmt); ok {
			for i, lhs := range assign.Lhs {
				if inventoryCallName(lhs) == "ctx" && i < len(assign.Rhs) && inventoryNilCommandContext(assign.Rhs[i]) {
					found = true
				}
			}
		}
		return true
	})
	return found
}
