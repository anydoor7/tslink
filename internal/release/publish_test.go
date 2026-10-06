package release_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"
	"unicode"

	yaml "go.yaml.in/yaml/v2"
)

// stableOnly is the release.yml predicate for a stable tag. The publish job's
// first step rejects tags with +build metadata, so in every tag that gets
// further a hyphen starts SemVer's pre-release field, which GoReleaser's
// `release.prerelease: auto` and `homebrew_casks.skip_upload: auto` read.
// TestReleaseTagStepExecutes runs that step.
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
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	SkipUpload  string `yaml:"skip_upload"`
	Caveats     string `yaml:"caveats"`
}

type releaseConfig struct {
	Release struct {
		Prerelease string `yaml:"prerelease"`
	} `yaml:"release"`
	Casks []caskConfig `yaml:"homebrew_casks"`
	Nfpms []struct {
		Description string `yaml:"description"`
	} `yaml:"nfpms"`
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

// TestPrereleaseTagsStayPrereleases pins the three pre-release decisions:
// GoReleaser marks tags with a SemVer pre-release field as pre-releases (never
// latest), the cask skips the tap for them, and release.yml applies its
// stable-only credential requirements to tags without a hyphen. The last
// agrees with the first two only for tags without +build metadata, which
// TestReleaseTagStepExecutes shows the publish job's first step enforces.
func TestPrereleaseTagsStayPrereleases(t *testing.T) {
	cfg := goreleaserConfig(t)
	if cfg.Release.Prerelease != "auto" {
		t.Errorf(".goreleaser.yml release.prerelease = %q, want auto so a -rc tag is not published as the latest release", cfg.Release.Prerelease)
	}
	if cfg.Casks[0].SkipUpload != "auto" {
		t.Errorf("homebrew_casks skip_upload = %q, want auto", cfg.Casks[0].SkipUpload)
	}
	for _, step := range publishSteps(t) {
		if strings.Contains(step.If, "github.ref_name") && step.If != stableOnly {
			t.Errorf("publish step %q uses tag predicate %q, want %s", step.Name, step.If, stableOnly)
		}
	}
}

// The tag check is the script step that reads the tag and writes no notes.
func tagCheckIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "release tag check", func(s publishStep) bool {
		return s.Env["TAG"] == refNameExpr && s.Env["RELEASE_NOTES"] == "" && s.Run != ""
	})
}

// semverPrerelease returns the pre-release field of a SemVer tag: the text
// after the first hyphen of the version before any +build metadata.
func semverPrerelease(tag string) string {
	version, _, _ := strings.Cut(tag, "+")
	_, prerelease, _ := strings.Cut(version, "-")
	return prerelease
}

// TestReleaseTagStepExecutes runs the checked-in tag check, which must come
// before every other publish step. It accepts only SemVer versions without
// build metadata, and for each tag it accepts, the hyphen test in stableOnly
// must classify the tag as SemVer does. v0.1.0+build-1 shows why "+" is
// refused: GoReleaser publishes it as a stable release, but stableOnly would
// skip the signing gate and the tap token for it.
func TestReleaseTagStepExecutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the ubuntu publish step with bash; covered on Linux and macOS")
	}
	steps := publishSteps(t)
	check := tagCheckIndex(t, steps)
	if check != 0 {
		t.Fatalf("release tag check is publish step %d, want the first step", check)
	}
	step := steps[check]
	if step.If != "" {
		t.Errorf("release tag check runs only if %q; every tag must pass it", step.If)
	}
	if strings.Contains(step.Run, "${{") {
		t.Errorf("release tag check interpolates an expression; pass values through env:\n%s", step.Run)
	}
	for _, tc := range []struct {
		tag        string
		ok         bool
		prerelease bool
	}{
		{tag: "v0.1.0", ok: true},
		{tag: "v10.20.30", ok: true},
		{tag: "v0.2.0-rc.1", ok: true, prerelease: true},
		{tag: "v1.0.0-alpha-beta.0a.0", ok: true, prerelease: true},
		{tag: "v0.1.0+build-1"},
		{tag: "v0.1.0+build"},
		{tag: "v0.1.0-rc.1+build-1"},
		{tag: "v01.0.0"},
		{tag: "v0.01.0"},
		{tag: "v1.0.0-rc.01"},
		{tag: "v1.0.0-"},
		{tag: "v1.0.0-rc..1"},
		{tag: "v1.0.0-é"},
		{tag: "v1.0.0-rc.ä"},
		{tag: "0.1.0"},
		{tag: "v0.1"},
		{tag: "v0.1.0.1"},
		{tag: "release-v0.1.0"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", step.Run)
			cmd.Dir = t.TempDir()
			// In this locale glibc's [A-Za-z] also matches é and ä, so the
			// step must choose its own.
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TAG=" + tc.tag, "LC_ALL=en_US.UTF-8"}
			out, err := cmd.CombinedOutput()
			if !tc.ok {
				if err == nil || !strings.Contains(string(out), "::error::") || !strings.Contains(string(out), tc.tag) {
					t.Fatalf("tag %s: want a failure naming it, got err=%v\n%s", tc.tag, err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("tag %s rejected: %v\n%s", tc.tag, err, out)
			}
			// stableOnly is !contains(github.ref_name, '-').
			hyphenated := strings.Contains(tc.tag, "-")
			if semver := semverPrerelease(tc.tag) != ""; hyphenated != semver || semver != tc.prerelease {
				t.Fatalf("tag %s: stableOnly treats it as a pre-release: %v; SemVer: %v; want %v", tc.tag, hyphenated, semver, tc.prerelease)
			}
		})
	}
}

// TestCaskCaveatsFitEveryPlatform renders the caveats both ways GoReleaser
// can: the cask also installs on Linux, so signing text must name macOS, and
// both variants tell an upgrading user to repoint the background service.
func TestCaskCaveatsFitEveryPlatform(t *testing.T) {
	caveats := goreleaserConfig(t).Casks[0].Caveats
	for _, signed := range []bool{true, false} {
		tmpl, err := template.New("caveats").Funcs(template.FuncMap{
			"isEnvSet": func(name string) bool { return signed && name == "MACOS_SIGN_P12" },
		}).Parse(caveats)
		if err != nil {
			t.Fatalf("parse caveats: %v", err)
		}
		var out bytes.Buffer
		if err := tmpl.Execute(&out, nil); err != nil {
			t.Fatalf("render caveats: %v", err)
		}
		text := out.String()
		want := []string{"run tslink install again", "upgraded binary"}
		if signed {
			want = append(want, "On macOS, tslink is signed")
		} else {
			want = append(want, "not signed or notarized", "xattr -d com.apple.quarantine")
		}
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("caveats (signed=%v) missing %q:\n%s", signed, w, text)
			}
		}
		if signed && strings.HasPrefix(strings.TrimSpace(text), "tslink is signed") {
			t.Errorf("signed caveats claim Apple signing without naming macOS:\n%s", text)
		}
	}
}

// TestCaskDescFollowsHomebrewRules applies the Cask Cookbook `desc` rules that
// brew audit enforces (rubocops/shared/desc_helper.rb): under 80 characters,
// a capital first letter, no leading article, no cask name, no platform and
// no full stop. The Linux packages carry the same description.
func TestCaskDescFollowsHomebrewRules(t *testing.T) {
	cfg := goreleaserConfig(t)
	cask := cfg.Casks[0]
	desc := cask.Description
	words := strings.Fields(desc)
	if len(words) == 0 {
		t.Fatal("homebrew_casks description is empty")
	}
	first := words[0]
	lower := strings.ToLower(desc)
	for _, rule := range []struct {
		broken bool
		what   string
	}{
		{len([]rune(desc)) > 80, "is longer than 80 characters"},
		{strings.TrimSpace(desc) != desc || strings.HasSuffix(desc, "."), "has surrounding spaces or a full stop"},
		{!unicode.IsUpper([]rune(desc)[0]), "does not start with a capital letter"},
		{strings.EqualFold(first, "a") || strings.EqualFold(first, "an") || strings.EqualFold(first, "the"), "starts with an article"},
		{strings.Contains(lower, strings.ToLower(cask.Name)), "contains the cask name"},
		{strings.Contains(lower, "macos") || strings.Contains(lower, "os x"), "contains the platform"},
	} {
		if rule.broken {
			t.Errorf("cask desc %q %s", desc, rule.what)
		}
	}
	for _, pkg := range cfg.Nfpms {
		if pkg.Description != desc {
			t.Errorf("nfpms description %q differs from the cask desc %q", pkg.Description, desc)
		}
	}
}
