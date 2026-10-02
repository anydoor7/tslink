package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestRecipePreserveHostDefaultsAndOverride(t *testing.T) {
	for _, r := range recipes.List().Recipes {
		t.Run(r.ID, func(t *testing.T) {
			want := r.ID != "ollama" && r.ID != "syncthing" && r.ID != "generic-web"
			for _, option := range []*bool{nil, new(true), new(false)} {
				_, svc, err := recipeService(recipeRequest{RecipeID: r.ID, PreserveHost: option})
				if option != nil {
					want = *option
				}
				if err != nil || svc.PreserveHost != want {
					t.Fatalf("default/override=%v service=%+v err=%v", option, svc, err)
				}
			}
		})
	}
}

func TestPreserveHostCLI(t *testing.T) {
	for _, prefix := range [][]string{{"apps", "share", "jupyter"}, {"add", "--recipe", "jupyter"}, {"add", "web", "--proxy", "127.0.0.1:8080", "--dry-run"}} {
		for _, option := range []string{"", "--preserve-host", "--preserve-host=false"} {
			t.Run(strings.Join(prefix, " ")+option, func(t *testing.T) {
				recipeTestRegistry(t)
				args := append([]string{}, prefix...)
				args = append(args, "--json")
				if option != "" {
					args = append(args, option)
				}
				out, err := runRecipeRoot(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					Data struct {
						Service struct {
							PreserveHost bool `json:"preserve_host"`
						} `json:"service"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &envelope); err != nil {
					t.Fatal(err)
				}
				want := prefix[1] != "web"
				if option != "" {
					want = option == "--preserve-host"
				}
				if envelope.Data.Service.PreserveHost != want {
					t.Fatalf("CLI policy want %t: %s", want, out)
				}
			})
		}
	}
	for _, flag := range []string{"--dir", "--tcp"} {
		t.Run(flag, func(t *testing.T) {
			recipeTestRegistry(t)
			value := "127.0.0.1:8080"
			if flag == "--dir" {
				value = t.TempDir()
			}
			_, err := runRecipeRoot(t, "add", "invalid", flag, value, "--dry-run", "--preserve-host")
			if err == nil || !strings.Contains(err.Error(), "--preserve-host requires --proxy") {
				t.Fatalf("admission=%v", err)
			}
		})
	}
}

func TestSharePreserveHostReuse(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			path := recipeTestRegistry(t)
			spec, err := inferShareTarget("8080", true)
			if err != nil {
				t.Fatal(err)
			}
			spec, err = applyShareExposure(spec, shareRequest{PreserveHost: preserve})
			if err != nil {
				t.Fatal(err)
			}
			svc, created, err := registerShare(path, spec, "")
			if err != nil || !created || svc.PreserveHost != preserve {
				t.Fatalf("create=%+v %t %v", svc, created, err)
			}
			reused, created, err := registerShare(path, spec, "")
			if err != nil || created || reused.Name != svc.Name {
				t.Fatalf("same-policy reuse=%+v %t %v", reused, created, err)
			}
			spec.Service.PreserveHost = !preserve
			_, _, err = registerShare(path, spec, "")
			if err == nil || !strings.Contains(err.Error(), "preserve_host=true") || !strings.Contains(err.Error(), "preserve_host=false") {
				t.Fatalf("conflicting reuse: %v", err)
			}
			reg, err := registry.Load(path)
			if err != nil || len(reg.Services) != 1 || reg.Services[0].PreserveHost != preserve {
				t.Fatalf("conflict changed registry: %+v %v", reg, err)
			}
		})
	}
	spec, err := inferShareTarget(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyShareExposure(spec, shareRequest{PreserveHost: true}); err == nil || !strings.Contains(err.Error(), "requires an HTTP port target") {
		t.Fatalf("file share admission: %v", err)
	}
}

func TestPreserveHostProjections(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			path := recipeTestRegistry(t)
			dir := filepath.Dir(path)
			svc := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://127.0.0.1:8080", PreserveHost: preserve}
			if _, err := registry.Add(path, svc); err != nil {
				t.Fatal(err)
			}
			if inspect.ServiceViewFor(svc).PreserveHost != preserve {
				t.Fatal("service view lost policy")
			}
			status, err := readOnlyStatus.getStatus(filepath.Join(dir, "pid"), path)
			if err != nil || len(status.Services) != 1 || status.Services[0].PreserveHost == nil || *status.Services[0].PreserveHost != preserve {
				t.Fatalf("status=%+v err=%v", status, err)
			}
			urls, err := readOnlyStatus.getStatusURLs(filepath.Join(dir, "pid"), path, filepath.Join(dir, "runtime.json"))
			if err != nil || len(urls.Services) != 1 || urls.Services[0].PreserveHost != preserve || listSummary(urls.Services[0]).PreserveHost != preserve {
				t.Fatalf("status/list projection=%+v %v", urls, err)
			}
			if err := validateListOptions(listOptions{Fields: []string{"preserve_host"}}); err != nil {
				t.Fatal(err)
			}
			if got := selectListFields(listSummary(urls.Services[0]), []string{"preserve_host"}); got["preserve_host"] != preserve {
				t.Fatalf("selected field=%v", got)
			}
			explain, err := accessExplainResultForPath(path, "web")
			if err != nil || explain.TSLinkKnown.PreserveHost != preserve {
				t.Fatalf("explain=%+v %v", explain, err)
			}
			var out bytes.Buffer
			formatAccessExplain(explain, &out)
			if !strings.Contains(out.String(), fmt.Sprintf("Use canonical external Host: %t", preserve)) {
				t.Fatal(out.String())
			}
			old := mcpStatusFn
			t.Cleanup(func() { mcpStatusFn = old })
			mcpStatusFn = func(string, string, string, string) (StatusResult, error) { return status, nil }
			result, err := callMCPTool(context.Background(), defaultMCPActions(sharePaths{Registry: path}, io.Discard), "status", json.RawMessage(`{}`))
			if err != nil || result.IsError {
				t.Fatalf("MCP status=%+v %v", result, err)
			}
			validateAgainstToolOutputSchema(t, "status", result.StructuredContent)
			b, _ := json.Marshal(result.StructuredContent)
			if !strings.Contains(string(b), fmt.Sprintf(`"preserve_host":%t`, preserve)) {
				t.Fatalf("MCP status lost policy: %s", b)
			}
			plan, err := applyRecipe(context.Background(), recipeRequest{RecipeID: "jupyter", Name: "web", NoDaemonInstall: true}, path, false, io.Discard)
			if err != nil || plan.Service.PreserveHost != preserve || !plan.Requested.PreserveHost || plan.Applied {
				t.Fatalf("existing recipe configuration changed: %+v %v", plan, err)
			}
		})
	}
}

func TestSharePreserveHostCLI(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(fmt.Sprint(preserve), func(t *testing.T) {
			path := recipeTestRegistry(t)
			cmd, _, err := rootCmd.Find([]string{"share"})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = cmd.Flags().Set("preserve-host", "false")
				cmd.Flags().Lookup("preserve-host").Changed = false
			}()
			oldRunning, oldResolve := shareIsRunningFn, shareResolveEndpointOnceFn
			t.Cleanup(func() { shareIsRunningFn = oldRunning; shareResolveEndpointOnceFn = oldResolve })
			shareIsRunningFn = func(string) bool { return true }
			shareResolveEndpointOnceFn = func(string, string, string, string) (serviceURLResolution, error) {
				return serviceURLResolution{Result: URLResult{URL: "https://app.review.example/"}}, nil
			}
			out, err := runRecipeRoot(t, "share", "8080", fmt.Sprintf("--preserve-host=%t", preserve), "--json")
			if err != nil {
				t.Fatal(err)
			}
			reg, err := registry.Load(path)
			if err != nil || len(reg.Services) != 1 || reg.Services[0].PreserveHost != preserve {
				t.Fatalf("share CLI registry=%+v err=%v", reg, err)
			}
			var envelope struct {
				Data struct {
					PreserveHost *bool `json:"preserve_host"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Data.PreserveHost == nil || *envelope.Data.PreserveHost != preserve {
				t.Fatalf("share result missing policy: %s", out)
			}
		})
	}
}

func TestPreserveHostMCPArguments(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		for _, tool := range []string{"add", "share", "recipe_plan", "recipe_apply"} {
			t.Run(fmt.Sprintf("%s/%t", tool, preserve), func(t *testing.T) {
				recipeTestRegistry(t)
				actions := mcpActions{
					add: func(_ context.Context, p AddParams, _ bool) (any, error) {
						if p.PreserveHost != preserve {
							t.Fatalf("MCP add lost policy: %+v", p)
						}
						return map[string]any{}, nil
					},
					share: func(_ context.Context, r shareRequest) (ShareResult, error) {
						if r.PreserveHost != preserve {
							t.Fatalf("MCP share lost policy: %+v", r)
						}
						return ShareResult{}, nil
					},
					recipeApply: func(_ context.Context, r recipeRequest, _ bool) (any, error) {
						if r.PreserveHost == nil || *r.PreserveHost != preserve {
							t.Fatalf("MCP recipe lost explicit bool: %+v", r)
						}
						_, svc, err := recipeService(r)
						if svc.PreserveHost != preserve {
							t.Fatal("MCP override not applied")
						}
						return svc, err
					},
				}
				arg := fmt.Sprintf(`{"preserve_host":%t,"target":"8080"}`, preserve)
				if tool == "add" {
					arg = fmt.Sprintf(`{"name":"web","type":"proxy","target":"8080","preserve_host":%t}`, preserve)
				}
				if strings.HasPrefix(tool, "recipe_") {
					arg = fmt.Sprintf(`{"recipe_id":"jupyter","preserve_host":%t}`, preserve)
				}
				result, err := callMCPTool(context.Background(), actions, tool, json.RawMessage(arg))
				if err != nil || result.IsError {
					t.Fatalf("MCP=%+v err=%v", result, err)
				}
			})
		}
	}
}
