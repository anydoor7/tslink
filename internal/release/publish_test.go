package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v2"
)

// stableOnly is the release.yml predicate for a stable tag. Hyphenated tags
// are pre-releases, matching GoReleaser's semver pre-release detection used by
// `release.prerelease: auto` and `homebrew_casks.skip_upload: auto`.
const stableOnly = "${{ !contains(github.ref_name, '-') }}"

const (
	tapClientIDExpr   = "${{ vars.HOMEBREW_TAP_APP_CLIENT_ID }}"
	tapPrivateKeyExpr = "${{ secrets.HOMEBREW_TAP_APP_PRIVATE_KEY }}"
	tapTokenOutput    = "${{ steps.tap-token.outputs.token }}"
	tapTokenTemplate  = "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
)

type publishStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
	With map[string]string `yaml:"with"`
}

func publishSteps(t *testing.T) []publishStep {
	t.Helper()
	body, ok := readWorkflows(t)["release.yml"]
	if !ok {
		t.Fatal("release.yml is missing")
	}
	var wf struct {
		Jobs map[string]struct {
			Steps []publishStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &wf); err != nil {
		t.Fatalf("parse release.yml: %v", err)
	}
	steps := wf.Jobs["publish"].Steps
	if len(steps) == 0 {
		t.Fatal("release.yml publish job has no steps")
	}
	return steps
}

type caskConfig struct {
	Repository struct {
		Owner string `yaml:"owner"`
		Name  string `yaml:"name"`
		Token string `yaml:"token"`
	} `yaml:"repository"`
	SkipUpload string `yaml:"skip_upload"`
	Caveats    string `yaml:"caveats"`
}

type releaseConfig struct {
	Release struct {
		Prerelease string `yaml:"prerelease"`
	} `yaml:"release"`
	Casks []caskConfig `yaml:"homebrew_casks"`
}

func goreleaserConfig(t *testing.T) releaseConfig {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".goreleaser.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg releaseConfig
	if err := yaml.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}
	if len(cfg.Casks) != 1 {
		t.Fatalf(".goreleaser.yml declares %d homebrew_casks, want exactly one", len(cfg.Casks))
	}
	return cfg
}

// stepIndex returns the index of the only step matching match.
func stepIndex(t *testing.T, steps []publishStep, what string, match func(publishStep) bool) int {
	t.Helper()
	found := -1
	for i, step := range steps {
		if match(step) {
			if found >= 0 {
				t.Fatalf("publish job has more than one %s step", what)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("publish job has no %s step", what)
	}
	return found
}

// The stable-tag credential gate is the stable-only step that runs a script
// before any tool is installed.
func credentialGateIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "stable credential gate", func(s publishStep) bool {
		return s.If == stableOnly && s.Run != ""
	})
}

func mintIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "tap token mint", func(s publishStep) bool {
		return strings.HasPrefix(s.Uses, "actions/create-github-app-token@")
	})
}

func goreleaserIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "GoReleaser", func(s publishStep) bool {
		return strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@")
	})
}

// TestStablePublishMintsScopedTapToken pins the tap credential path: a GitHub
// App installation token minted for the tap repository named in
// .goreleaser.yml, with Contents write only, before GoReleaser runs, and
// handed to GoReleaser under the variable its cask config reads.
func TestStablePublishMintsScopedTapToken(t *testing.T) {
	steps := publishSteps(t)
	cask := goreleaserConfig(t).Casks[0]
	gate, mint, release := credentialGateIndex(t, steps), mintIndex(t, steps), goreleaserIndex(t, steps)
	if !(gate < mint && mint < release) {
		t.Fatalf("publish step order: credential gate %d, token mint %d, GoReleaser %d; want gate < mint < GoReleaser", gate, mint, release)
	}

	m := steps[mint]
	if m.ID != "tap-token" || m.If != stableOnly {
		t.Errorf("token mint id=%q if=%q, want id tap-token and if %s", m.ID, m.If, stableOnly)
	}
	want := map[string]string{
		"client-id":           tapClientIDExpr,
		"private-key":         tapPrivateKeyExpr,
		"owner":               cask.Repository.Owner,
		"repositories":        cask.Repository.Name,
		"permission-contents": "write",
	}
	for key, value := range want {
		if m.With[key] != value {
			t.Errorf("token mint with.%s = %q, want %q", key, m.With[key], value)
		}
	}
	for key, value := range m.With {
		if _, ok := want[key]; !ok {
			t.Errorf("token mint sets unexpected input %s=%q; the tap token needs only Contents write on one repository", key, value)
		}
	}
	if cask.Repository.Owner != "anydoor7" || cask.Repository.Name != "homebrew-tap" {
		t.Errorf("cask repository = %s/%s, want anydoor7/homebrew-tap", cask.Repository.Owner, cask.Repository.Name)
	}

	if got := steps[release].Env["HOMEBREW_TAP_GITHUB_TOKEN"]; got != tapTokenOutput {
		t.Errorf("GoReleaser env HOMEBREW_TAP_GITHUB_TOKEN = %q, want the minted token %q", got, tapTokenOutput)
	}
	if cask.Repository.Token != tapTokenTemplate {
		t.Errorf("cask repository token = %q, want %q", cask.Repository.Token, tapTokenTemplate)
	}

	gateEnv := steps[gate].Env
	if gateEnv["HOMEBREW_TAP_APP_CLIENT_ID"] != tapClientIDExpr || gateEnv["HOMEBREW_TAP_APP_PRIVATE_KEY"] != tapPrivateKeyExpr {
		t.Errorf("credential gate checks %q and %q, want the inputs the mint step uses", gateEnv["HOMEBREW_TAP_APP_CLIENT_ID"], gateEnv["HOMEBREW_TAP_APP_PRIVATE_KEY"])
	}

	for name, body := range readWorkflows(t) {
		if strings.Contains(string(body), "secrets.HOMEBREW_TAP_GITHUB_TOKEN") {
			t.Errorf("%s still reads the retired HOMEBREW_TAP_GITHUB_TOKEN secret; the tap token is minted from the GitHub App", name)
		}
	}
}

// TestStableCredentialGateExecutes runs the checked-in gate script. Every
// missing stable credential must stop the job before the token mint and
// GoReleaser, and a complete set must pass.
func TestStableCredentialGateExecutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the ubuntu publish step with bash; covered on Linux and macOS")
	}
	steps := publishSteps(t)
	gate := steps[credentialGateIndex(t, steps)]
	complete := map[string]string{
		"MACOS_SIGN_P12":               "cDEyLWZpeHR1cmU=",
		"MACOS_SIGN_P12_PASSWORD":      "",
		"MACOS_NOTARY_ISSUER_ID":       "issuer-fixture",
		"MACOS_NOTARY_KEY_ID":          "key-id-fixture",
		"MACOS_NOTARY_KEY":             "cDgtZml4dHVyZQ==",
		"HOMEBREW_TAP_APP_CLIENT_ID":   "Iv23-fixture",
		"HOMEBREW_TAP_APP_PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\nfixture\n-----END RSA PRIVATE KEY-----\n",
	}
	for key := range complete {
		if _, ok := gate.Env[key]; !ok {
			t.Fatalf("credential gate does not receive %s", key)
		}
	}
	for _, tc := range []struct {
		name      string
		change    map[string]string
		wantErr   string
		linuxOnly bool
	}{
		{name: "complete"},
		{name: "no App client ID", change: map[string]string{"HOMEBREW_TAP_APP_CLIENT_ID": ""}, wantErr: "HOMEBREW_TAP_APP_CLIENT_ID"},
		{name: "no App private key", change: map[string]string{"HOMEBREW_TAP_APP_PRIVATE_KEY": ""}, wantErr: "HOMEBREW_TAP_APP_PRIVATE_KEY"},
		{name: "no signing identity", change: map[string]string{"MACOS_SIGN_P12": ""}, wantErr: "macOS signing secrets"},
		// GNU and uutils base64 -d reject PEM text, as on the ubuntu runner;
		// the BSD base64 on macOS skips characters it cannot decode.
		{name: "PEM notary key", change: map[string]string{"MACOS_NOTARY_KEY": "-----BEGIN PRIVATE KEY-----"}, wantErr: "must be base64-encoded", linuxOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.linuxOnly && runtime.GOOS != "linux" {
				t.Skip("the step runs on ubuntu-latest; only a Linux base64 shows its decoding check")
			}
			cmd := exec.Command("bash", "-c", gate.Run)
			cmd.Dir = t.TempDir()
			cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			for key, value := range complete {
				if changed, ok := tc.change[key]; ok {
					value = changed
				}
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			out, err := cmd.CombinedOutput()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("complete stable credentials rejected: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), "::error::") || !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("gate accepted %s or failed without naming it: err=%v\n%s", tc.name, err, out)
			}
			for _, value := range complete {
				if len(value) > 8 && strings.Contains(string(out), value) {
					t.Fatalf("gate output echoes a credential value:\n%s", out)
				}
			}
		})
	}
}
