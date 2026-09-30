package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestNoE2ETestReadsTheArgvOrEnvironmentOfAnotherProcess. The e2e tests in
// cmd find the daemons they started by listing this user's processes and
// reading the executable path of the few that are named like the binary under
// test and started after the test binary. Such a candidate can still be a
// tslink that the contributor's shell prompt, editor, monitoring or agent runs
// while the tests do, so on macOS the path must come from proc_info's
// PROC_PIDPATHINFO (what proc_pidpath uses), which returns the path and
// nothing else. The kern.procargs2 sysctl, like the older kern.procargs,
// copies the process's whole argv and environment into the test process along
// with the path.
//
// So no string literal or identifier in cmd's _test.go files may name
// procargs; a comment may. Control: the scan must see the path-only call.
func TestNoE2ETestReadsTheArgvOrEnvironmentOfAnotherProcess(t *testing.T) {
	cmdDir := filepath.Join(repoRootForTest(t), "cmd")
	files, err := filepath.Glob(filepath.Join(cmdDir, "*_test.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("glob cmd/*_test.go = %d files, %v; want the e2e test files", len(files), err)
	}
	var problems []string
	pathOnlyReads := 0
	for _, path := range files {
		rel := "cmd/" + filepath.Base(path)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		at := func(pos token.Pos, what string) {
			problems = append(problems, rel+":"+strconv.Itoa(fset.Position(pos).Line)+": "+what)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BasicLit:
				if node.Kind == token.STRING && strings.Contains(strings.ToLower(node.Value), "procargs") {
					at(node.Pos(), node.Value)
				}
			case *ast.Ident:
				if strings.Contains(strings.ToLower(node.Name), "procargs") {
					at(node.Pos(), node.Name)
				}
				if node.Name == "SYS_PROC_INFO" {
					pathOnlyReads++
				}
			}
			return true
		})
	}
	if pathOnlyReads == 0 {
		t.Fatal("the scan saw no SYS_PROC_INFO call in cmd's _test.go files; the macOS e2e process table reads executable paths some other way, or the scan is not reading those files, so a clean result would mean nothing")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d place(s) in cmd's tests name procargs, which reads another process's argv and environment:\n  %s\n"+
			"Read only the executable path: proc_info PROC_PIDPATHINFO on macOS (cmd/e2e_process_table_darwin_test.go).",
			len(problems), strings.Join(problems, "\n  "))
	}
}
