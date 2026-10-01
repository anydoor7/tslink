package inspect

import (
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// TestSingleFileShareBackendIsTheFile pins R5-8. A share of one file used to be
// reported as backend.kind "directory" with the parent directory as display,
// in list, status, access explain and their MCP twins, although only that one
// file is served. backend.kind is a JSON contract that freezes at the first
// public release. A directory share is the control and is unchanged.
func TestSingleFileShareBackendIsTheFile(t *testing.T) {
	file := registry.Service{Name: "report", Type: registry.TypeFile, Path: "/srv/docs", File: "report.pdf"}
	dir := registry.Service{Name: "docs", Type: registry.TypeFile, Path: "/srv/docs"}
	want := map[string]BackendView{
		"report": {Kind: "file", Display: filepath.Join("/srv/docs", "report.pdf")},
		"docs":   {Kind: "directory", Display: "/srv/docs"},
	}

	for _, svc := range []registry.Service{file, dir} {
		if got := ServiceViewFor(svc).Backend; got != want[svc.Name] {
			t.Errorf("ServiceViewFor(%s).Backend = %+v, want %+v", svc.Name, got, want[svc.Name])
		}
	}
	// The list view is built from the same services and must agree.
	for _, view := range ServiceViews([]registry.Service{file, dir}) {
		if view.Backend != want[view.Name] {
			t.Errorf("list view %s backend = %+v, want %+v", view.Name, view.Backend, want[view.Name])
		}
	}
}
