package release_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v2"
)

// These are publication contracts: a wrong repository, token, identifier or
// pre-release policy can publish a release without its Windows install path.
func TestWindowsPublisherConfiguration(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[interface{}]interface{}
	if err := yaml.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	description := goreleaserConfig(t).Casks[0].Description
	for _, publisher := range []string{"scoops", "winget"} {
		items, ok := config[publisher].([]interface{})
		if !ok || len(items) != 1 {
			t.Fatalf("%s must declare exactly one publisher", publisher)
		}
		item := items[0].(map[interface{}]interface{})
		want := map[string]interface{}{
			"name": "tslink", "ids": []interface{}{"archive"}, "skip_upload": "auto",
			"homepage": "https://github.com/anydoor7/tslink", "license": "Apache-2.0",
			"repository.owner": "anydoor7",
		}
		if publisher == "scoops" {
			want["directory"] = "bucket"
			want["description"] = description
			want["repository.name"] = "scoop-bucket"
			want["repository.branch"] = "main"
			want["repository.token"] = "{{ .Env.SCOOP_BUCKET_GITHUB_TOKEN }}"
			want["commit_msg_template"] = "Scoop update for {{ .ProjectName }} version {{ .Tag }}"
		} else {
			want["publisher"] = "anydoor7"
			want["package_name"] = "TSLink"
			want["package_identifier"] = "anydoor7.TSLink"
			want["short_description"] = description
			want["license_url"] = "https://github.com/anydoor7/tslink/blob/{{ .Tag }}/LICENSE"
			want["release_notes_url"] = "https://github.com/anydoor7/tslink/releases/tag/{{ .Tag }}"
			want["installation_notes"] = "After upgrading, run tslink install again if TSLink runs as a background service."
			want["repository.name"] = "winget-pkgs"
			want["repository.branch"] = "tslink-{{ .Version }}"
			want["repository.token"] = "{{ .Env.WINGET_GITHUB_TOKEN }}"
			want["repository.pull_request.enabled"] = true
			want["repository.pull_request.base.owner"] = "microsoft"
			want["repository.pull_request.base.name"] = "winget-pkgs"
			want["repository.pull_request.base.branch"] = "master"
		}
		for key, expected := range want {
			t.Run(publisher+"/"+key, func(t *testing.T) {
				var got interface{} = item
				for _, part := range strings.Split(key, ".") {
					mapping, _ := got.(map[interface{}]interface{})
					got = mapping[part]
				}
				actual, _ := json.Marshal(got)
				target, _ := json.Marshal(expected)
				if string(actual) != string(target) {
					t.Errorf("%s.%s = %s, want %s", publisher, key, actual, target)
				}
			})
		}
	}
}

func TestWindowsPublisherWorkflow(t *testing.T) {
	steps := publishSteps(t)
	gate, mint, release := credentialGateIndex(t, steps), mintIndex(t, steps), goreleaserIndex(t, steps)
	preflight := stepIndex(t, steps, "publisher readback", func(s publishStep) bool {
		return s.Run == "python3 scripts/windows-publish-preflight.py"
	})
	if !(gate < mint && mint < preflight && preflight < release) {
		t.Fatal("publisher readback must follow mint and precede GoReleaser")
	}
	if steps[preflight].If != stableOnly {
		t.Fatal("publisher readback must run only for stable tags")
	}
	for _, index := range []int{gate, preflight, release} {
		if steps[index].Env["WINGET_GITHUB_TOKEN"] != "${{ secrets.WINGET_GITHUB_TOKEN }}" {
			t.Errorf("step %s must receive WINGET_GITHUB_TOKEN from the release environment", steps[index].Name)
		}
	}
	if steps[preflight].Env["HOMEBREW_TAP_GITHUB_TOKEN"] != tapTokenOutput {
		t.Error("publisher readback must receive the minted App token")
	}
	if steps[release].Env["SCOOP_BUCKET_GITHUB_TOKEN"] != tapTokenOutput {
		t.Error("Scoop must receive the minted App token")
	}
}

func TestWindowsPublisherPreflightExecutes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required on the Linux publish runner")
	}
	for _, fault := range []string{"", "missing-token", "bucket-denied", "bucket-uninitialized", "tap-readonly", "fine-grained", "broad-scope", "fork-readonly", "wrong-parent", "fork-archived", "upstream-branch"} {
		t.Run(fault, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				token := "app-secret-fixture"
				if r.URL.Path == "/user" || strings.Contains(r.URL.Path, "winget-pkgs") {
					token = "winget-secret-fixture"
				}
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("readback used the wrong credential")
				}
				if r.Method != "GET" {
					t.Error("preflight attempted a mutation")
				}
				data := map[string]interface{}{"permissions": map[string]bool{"push": true}}
				switch r.URL.Path {
				case "/repos/anydoor7/scoop-bucket":
					if fault == "bucket-denied" {
						http.Error(w, token, http.StatusForbidden)
						return
					}
				case "/repos/anydoor7/scoop-bucket/branches/main":
					if fault == "bucket-uninitialized" {
						http.Error(w, token, http.StatusNotFound)
						return
					}
				case "/repos/anydoor7/homebrew-tap":
					if fault == "tap-readonly" {
						data["permissions"] = map[string]bool{"push": false}
					}
				case "/repos/anydoor7/homebrew-tap/branches/main":
				case "/user":
					scope := "public_repo"
					if fault == "fine-grained" {
						scope = ""
					} else if fault == "broad-scope" {
						scope = "repo"
					}
					w.Header().Set("X-OAuth-Scopes", scope)
				case "/repos/anydoor7/winget-pkgs":
					data["fork"] = true
					data["parent"] = map[string]string{"full_name": "microsoft/winget-pkgs"}
					if fault == "fork-readonly" {
						data["permissions"] = map[string]bool{"push": false}
					} else if fault == "wrong-parent" {
						data["parent"] = map[string]string{"full_name": "other/winget-pkgs"}
					} else if fault == "fork-archived" {
						data["archived"] = true
					}
				case "/repos/microsoft/winget-pkgs":
					data["default_branch"] = "master"
					if fault == "upstream-branch" {
						data["default_branch"] = "main"
					}
				default:
					t.Errorf("unexpected readback path %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(data)
			}))
			defer server.Close()
			cmd := exec.Command(python, filepath.Join(repoRoot(t), "scripts/windows-publish-preflight.py"))
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_API_URL=" + server.URL, "HOMEBREW_TAP_GITHUB_TOKEN=app-secret-fixture"}
			if fault != "missing-token" {
				cmd.Env = append(cmd.Env, "WINGET_GITHUB_TOKEN=winget-secret-fixture")
			}
			out, err := cmd.CombinedOutput()
			if (err != nil) != (fault != "") {
				t.Fatalf("fault %q: err=%v, output=%s", fault, err, out)
			}
			if fault != "" && !strings.Contains(string(out), "::error::") {
				t.Fatalf("failure lacks workflow annotation: %s", out)
			}
			if strings.Contains(string(out), "secret-fixture") {
				t.Fatal("readback exposed a credential")
			}
		})
	}
}
