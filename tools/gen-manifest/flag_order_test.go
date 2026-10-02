package main

import (
	"reflect"
	"testing"

	"github.com/anydoor7/tslink/cmd"
)

func TestPlatformFlagInsertionUsesNativeCanonicalOrder(t *testing.T) {
	// Native manifests sort by name across scopes. Inserting a local flag
	// before inherited flags would split that order and break native parity.
	flags := []cmd.FlagInfo{{Name: "force", Scope: "local"}, {Name: "json", Scope: "inherited"}, {Name: "no-auto-provision", Scope: "local"}}
	got := insertFlag(flags, cmd.FlagInfo{Name: "startup", Scope: "local", Platforms: []string{"windows"}})
	var names []string
	for _, flag := range got {
		names = append(names, flag.Name)
	}
	if want := []string{"force", "json", "no-auto-provision", "startup"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("flag order=%v, want native canonical order %v", names, want)
	}
	if len(flags) != 3 || flags[1].Name != "json" {
		t.Fatal("insertion changed its native input")
	}
}
