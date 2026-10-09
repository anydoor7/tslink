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
	if _, exists := config["winget"]; exists {
		t.Fatal("winget must be an owner-run post-release submission, outside GoReleaser")
	}
	for _, publisher := range []string{"scoops"} {
		items, ok := config[publisher].([]interface{})
		if !ok || len(items) != 1 {
			t.Fatalf("%s must declare exactly one publisher", publisher)
		}
		item := items[0].(map[interface{}]interface{})
		want := map[string]interface{}{
			"name": "tslink", "ids": []interface{}{"archive"}, "skip_upload": "auto",
			"homepage": "https://github.com/anydoor7/tslink", "license": "Apache-2.0",
			"repository.owner": "anydoor7", "directory": "bucket", "description": description,
			"repository.name": "scoop-bucket", "repository.branch": "main",
			"repository.token":    "{{ .Env.SCOOP_BUCKET_GITHUB_TOKEN }}",
			"commit_msg_template": "Scoop update for {{ .ProjectName }} version {{ .Tag }}",
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
	if steps[release].ID != "goreleaser" {
		t.Fatal("Release step must expose its outcome as goreleaser")
	}
	attest := stepIndex(t, steps, "attestation", func(s publishStep) bool {
		return s.Name == "Attest release artifacts"
	})
	if attest <= release || steps[attest].If != "${{ !cancelled() && steps.goreleaser.outcome != 'skipped' && hashFiles('dist/checksums.txt') != '' }}" {
		t.Fatal("attestation must run after an attempted release with checksums, including publisher failure")
	}
	for _, step := range steps {
		for key := range step.Env {
			if strings.Contains(strings.ToUpper(key), "WINGET") {
				t.Errorf("step %s must not receive winget credentials", step.Name)
			}
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
	for _, fault := range []string{"", "missing-token", "bucket-not-covered", "bucket-private", "tap-archived", "bucket-uninitialized", "readback-denied"} {
		t.Run(fault, func(t *testing.T) {
			seen := map[string]bool{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen[r.URL.Path] = true
				token := "app-secret-fixture"
				if r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("readback used the wrong credential")
				}
				if r.Method != "GET" {
					t.Error("preflight attempted a mutation")
				}
				// GitHub App installation-token responses omit permissions.
				data := map[string]interface{}{}
				switch r.URL.Path {
				case "/installation/repositories":
					if r.URL.RawQuery != "per_page=100" {
						t.Error("installation readback must request its full scoped list")
					}
					if fault == "readback-denied" {
						http.Error(w, token, http.StatusForbidden)
						return
					}
					tap := map[string]interface{}{"full_name": "anydoor7/homebrew-tap", "private": false, "archived": fault == "tap-archived"}
					bucket := map[string]interface{}{"full_name": "anydoor7/scoop-bucket", "private": fault == "bucket-private", "archived": false}
					repos := []interface{}{tap}
					if fault != "bucket-not-covered" {
						repos = append(repos, bucket)
					}
					data["repositories"] = repos
				case "/repos/anydoor7/scoop-bucket/branches/main":
					if fault == "bucket-uninitialized" {
						http.Error(w, token, http.StatusNotFound)
						return
					}
				case "/repos/anydoor7/homebrew-tap/branches/main":
				default:
					t.Errorf("unexpected readback path %s", r.URL.Path)
				}
				json.NewEncoder(w).Encode(data)
			}))
			defer server.Close()
			cmd := exec.Command(python, filepath.Join(repoRoot(t), "scripts/windows-publish-preflight.py"))
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GITHUB_API_URL=" + server.URL}
			if fault != "missing-token" {
				cmd.Env = append(cmd.Env, "HOMEBREW_TAP_GITHUB_TOKEN=app-secret-fixture")
			}
			out, err := cmd.CombinedOutput()
			if (err != nil) != (fault != "") {
				t.Fatalf("fault %q: err=%v, output=%s", fault, err, out)
			}
			if fault == "" {
				for _, path := range []string{"/installation/repositories", "/repos/anydoor7/homebrew-tap/branches/main", "/repos/anydoor7/scoop-bucket/branches/main"} {
					if !seen[path] {
						t.Errorf("successful preflight did not read %s", path)
					}
				}
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
