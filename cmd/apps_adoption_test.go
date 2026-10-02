package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// Port of the independent reviewer's rollback reproduction, also exercising
// the AddIfMissing loser and daemon setup failure paths.
func TestRecipeSettlesTentativeRegistration(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, setupFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("concurrent=%t/setup_fails=%t", concurrent, setupFails), func(t *testing.T) {
				path := recipeTestRegistry(t)
				req := recipeRequest{RecipeID: "jellyfin", NoDaemonInstall: true}
				_, svc, err := recipeService(req)
				if err != nil {
					t.Fatal(err)
				}
				var persisted registry.Service
				create := func() {
					created, err := registry.AddTentative(path, svc)
					if err != nil || !created {
						t.Fatalf("fixture creation: %t %v", created, err)
					}
					persisted, err = loadPersistedService(path, svc.Name)
					if err != nil {
						t.Fatal(err)
					}
				}
				if concurrent {
					old := recipeAddIfMissingFn
					t.Cleanup(func() { recipeAddIfMissingFn = old })
					recipeAddIfMissingFn = func(string, registry.Service) (bool, error) { create(); return false, nil }
				} else {
					create()
				}
				if setupFails {
					ensureDaemonFn = func(context.Context, io.Writer, bool) error {
						return registry.CodedError{Code: "daemon_setup_failed", Message: "fixture daemon setup failure"}
					}
				}
				result, err := applyRecipe(context.Background(), req, path, false, io.Discard)
				if setupFails {
					if err == nil || !strings.Contains(err.Error(), "Configuration remains") {
						t.Fatalf("setup failure: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if result.Action != templateActionSkipExisting || result.Applied {
					t.Fatalf("reuse: %+v", result)
				}
				removed, err := registry.RemoveIfUnchanged(path, persisted)
				if err != nil {
					t.Fatal(err)
				}
				if removed {
					t.Fatal("creator rollback removed registration after recipe reuse")
				}
				if _, err := loadPersistedService(path, svc.Name); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRecipeAdoptionCompareFailure(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, change := range []string{"deleted", "replaced", "lock error"} {
			t.Run(fmt.Sprintf("concurrent=%t/%s", concurrent, change), func(t *testing.T) {
				path := recipeTestRegistry(t)
				req := recipeRequest{RecipeID: "jellyfin", NoDaemonInstall: true}
				_, svc, err := recipeService(req)
				if err != nil {
					t.Fatal(err)
				}
				create := func() {
					if _, err := registry.AddTentative(path, svc); err != nil {
						t.Fatal(err)
					}
				}
				if concurrent {
					old := recipeAddIfMissingFn
					t.Cleanup(func() { recipeAddIfMissingFn = old })
					recipeAddIfMissingFn = func(string, registry.Service) (bool, error) { create(); return false, nil }
				} else {
					create()
				}
				old := addKeepIfUnchangedFn
				t.Cleanup(func() { addKeepIfUnchangedFn = old })
				addKeepIfUnchangedFn = func(p string, expected registry.Service) (bool, error) {
					original := expected
					switch change {
					case "deleted":
						if _, err := registry.RemoveIfUnchanged(p, expected); err != nil {
							t.Fatal(err)
						}
					case "replaced":
						expected.Target = "http://127.0.0.1:9999"
						if _, err := registry.Add(p, expected); err != nil {
							t.Fatal(err)
						}
					case "lock error":
						return false, fmt.Errorf("fixture adoption lock failure")
					}
					return old(p, original)
				}
				ensureDaemonFn = func(context.Context, io.Writer, bool) error {
					t.Fatal("daemon setup ran after failed adoption")
					return nil
				}
				_, err = applyRecipe(context.Background(), req, path, false, io.Discard)
				want := "deleted or replaced during recipe adoption"
				if change == "lock error" {
					want = "fixture adoption lock failure"
				}
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("adoption refusal cause: %v", err)
				}
			})
		}
	}
}

func TestRecipePreviewDoesNotSettleTentative(t *testing.T) {
	path := recipeTestRegistry(t)
	req := recipeRequest{RecipeID: "jellyfin", NoDaemonInstall: true}
	_, svc, err := recipeService(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddTentative(path, svc); err != nil {
		t.Fatal(err)
	}
	persisted, err := loadPersistedService(path, svc.Name)
	if err != nil {
		t.Fatal(err)
	}
	result, err := applyRecipe(context.Background(), req, path, true, io.Discard)
	if err != nil || !result.DryRun {
		t.Fatalf("preview: %+v %v", result, err)
	}
	removed, err := registry.RemoveIfUnchanged(path, persisted)
	if err != nil || !removed {
		t.Fatalf("read-only preview settled registration: removed=%t err=%v", removed, err)
	}
}
