// Package release_test holds hermetic validators over the GitHub Actions
// workflow files. They prove release-safety invariants locally, with no network
// and no GitHub execution:
//
//   - every third-party `uses:` reference is pinned to an immutable 40-hex
//     commit SHA (not a mutable tag/branch);
//   - the invalid upload-artifact pin is gone;
//   - the tag Release workflow can only publish AFTER the reusable
//     release-candidate gate succeeds, and elevated write/id-token permission
//     is confined to the publish job behind a repository-variable release
//     authorization guard;
//   - PR/main CI and the tag path consume the SAME reusable candidate gate;
//   - the candidate gate still declares every required job.
//
// Hosted runner behavior, action resolution, environment reviewer/ruleset
// readback, and branch protection remain external; this test only proves the
// checked-in graph is fail-closed.
package release_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/manifestcheck"
	yaml "go.yaml.in/yaml/v2"
)

const (
	candidateWorkflow = "release-candidate.yml"
	reusableRef       = "./.github/workflows/release-candidate.yml"
	// The pin that shipped before this remediation; it claimed v7.2.1 which
	// never existed. It must never reappear.
	invalidUploadPin = "1a80836c5c9d9e5755a25cb59ec6f45a3b5f41a8"
)

var (
	usesRE = regexp.MustCompile(`(?m)^\s*(?:-\s*)?uses:\s*([^\s#]+)`)
	shaRE  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod) from test working directory")
		}
		dir = parent
	}
}

func workflowsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), ".github", "workflows")
}

func readWorkflows(t *testing.T) map[string][]byte {
	t.Helper()
	dir := workflowsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read workflows dir %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".yml") && !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = normalizeWorkflowNewlines(b)
	}
	if len(out) == 0 {
		t.Fatalf("no workflow files found in %s", dir)
	}
	return out
}

func normalizeWorkflowNewlines(body []byte) []byte {
	return bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n"))
}

func TestNormalizeWorkflowNewlines(t *testing.T) {
	got := normalizeWorkflowNewlines([]byte("first\r\nsecond\r\n"))
	if string(got) != "first\nsecond\n" {
		t.Fatalf("normalized workflow = %q, want LF line endings", got)
	}
}

func TestRepositoryPinsLFLineEndings(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".gitattributes"))
	if err != nil {
		t.Fatalf("read .gitattributes: %v", err)
	}
	if string(body) != "* text=auto eol=lf\n" {
		t.Fatalf(".gitattributes = %q, want repository-wide LF policy", body)
	}
}

func TestDocumentationHasNoChineseDuplicates(t *testing.T) {
	cmd := exec.Command("git", "-C", repoRoot(t), "ls-files", "-z", "--", "*.md")
	tracked, err := cmd.Output()
	if err != nil {
		t.Fatalf("enumerate tracked Markdown: %v", err)
	}
	foundHomepage := false
	for _, name := range strings.Split(string(tracked), "\x00") {
		if name == "README.md" {
			foundHomepage = true
		}
		if strings.HasSuffix(name, "_zh.md") {
			t.Errorf("tracked Chinese duplicate %q: documentation is English-only; only docs/README.<lang>.md translations are allowed", name)
		}
	}
	if !foundHomepage {
		t.Fatal("tracked Markdown enumeration did not include README.md")
	}
}

// TestEveryActionPinnedToSHA proves no workflow references a mutable action tag.
func TestEveryActionPinnedToSHA(t *testing.T) {
	for name, body := range readWorkflows(t) {
		text := string(body)
		if strings.Contains(text, invalidUploadPin) {
			t.Errorf("%s still references the invalid upload-artifact pin %s", name, invalidUploadPin)
		}
		for _, m := range usesRE.FindAllStringSubmatch(text, -1) {
			ref := m[1]
			// Local reusable workflow references are pinned by repo, not by SHA.
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
				continue
			}
			at := strings.LastIndex(ref, "@")
			if at < 0 {
				t.Errorf("%s: unpinned action reference %q (no @sha)", name, ref)
				continue
			}
			pin := ref[at+1:]
			if !shaRE.MatchString(pin) {
				t.Errorf("%s: action %q is pinned to %q, not a 40-hex commit SHA", name, ref, pin)
			}
		}
	}
}

// stringOrSlice accepts `needs: a` and `needs: [a, b]`.
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var single string
	if err := unmarshal(&single); err == nil {
		*s = []string{single}
		return nil
	}
	var multi []string
	if err := unmarshal(&multi); err == nil {
		*s = multi
	}
	return nil
}

func (s stringOrSlice) contains(v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// envField accepts `environment: name` and `environment: {name: ...}`.
type envField struct{ Name string }

func (e *envField) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var single string
	if err := unmarshal(&single); err == nil {
		e.Name = single
		return nil
	}
	var obj struct {
		Name string `yaml:"name"`
	}
	if err := unmarshal(&obj); err == nil {
		e.Name = obj.Name
	}
	return nil
}

type jobDef struct {
	Uses        string            `yaml:"uses"`
	Needs       stringOrSlice     `yaml:"needs"`
	Environment envField          `yaml:"environment"`
	Env         map[string]string `yaml:"env"`
	Permissions map[string]string `yaml:"permissions"`
	With        map[string]string `yaml:"with"`
	Secrets     any               `yaml:"secrets"`
	Strategy    struct {
		Matrix struct {
			GOOS   []string `yaml:"goos"`
			GOARCH []string `yaml:"goarch"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []struct {
		Name string            `yaml:"name"`
		Run  string            `yaml:"run"`
		Env  map[string]string `yaml:"env"`
		With map[string]string `yaml:"with"`
	} `yaml:"steps"`
}

type workflowFile struct {
	Env  map[string]string `yaml:"env"`
	Jobs map[string]jobDef `yaml:"jobs"`
}

func parse(t *testing.T, name string, body []byte) workflowFile {
	t.Helper()
	var wf workflowFile
	if err := yaml.Unmarshal(body, &wf); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return wf
}

// TestTagPublishCannotBypassCandidate is the core tag-publish graph proof.
func TestTagPublishCannotBypassCandidate(t *testing.T) {
	all := readWorkflows(t)
	body, ok := all["release.yml"]
	if !ok {
		t.Fatal("release.yml is missing")
	}
	wf := parse(t, "release.yml", body)

	candidate, ok := wf.Jobs["candidate"]
	if !ok {
		t.Fatal("release.yml has no `candidate` job")
	}
	if candidate.Uses != reusableRef {
		t.Errorf("release.yml candidate job must call %q, got %q", reusableRef, candidate.Uses)
	}

	publish, ok := wf.Jobs["publish"]
	if !ok {
		t.Fatal("release.yml has no `publish` job")
	}
	if !publish.Needs.contains("candidate") {
		t.Errorf("release.yml publish job must `needs: candidate`, got %v", publish.Needs)
	}
	if publish.Environment.Name != "release" {
		t.Errorf("release.yml publish job must bind environment %q, got %q", "release", publish.Environment.Name)
	}
	text := string(body)
	for _, want := range []string{
		"TSLINK_RELEASE_PUBLISH_ENABLED",
		"vars.TSLINK_RELEASE_PUBLISH_ENABLED",
		"Release publishing is disabled",
		"external readback proves the release environment",
		"refs/tags/v* ruleset",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml missing release authorization guard text %q", want)
		}
	}

	// Elevated write/id-token permission must exist ONLY on the publish job.
	for jobName, job := range wf.Jobs {
		if jobName == "publish" {
			continue
		}
		if v := job.Permissions["contents"]; v == "write" {
			t.Errorf("release.yml job %q must not hold contents:write; only publish may", jobName)
		}
		if _, ok := job.Permissions["id-token"]; ok {
			t.Errorf("release.yml job %q must not request id-token; only publish may", jobName)
		}
	}
	if publish.Permissions["contents"] != "write" {
		t.Errorf("publish job must hold contents:write, got %q", publish.Permissions["contents"])
	}
	if publish.Permissions["id-token"] != "write" {
		t.Errorf("publish job must hold id-token:write, got %q", publish.Permissions["id-token"])
	}
}

// TestCIConsumesReusableCandidate proves PR/main run the same gate as the tag path.
//
// ci.yml is present again, so this test runs rather than skipping; the skip
// branch is kept only so a future deletion of ci.yml does not turn this test
// into a spurious failure. Deleting ci.yml is still refused by
// TestCIShapePinsTriggersAndCostControls, which fails with "ci.yml is missing".
// The invariant stays worth guarding because it is what makes PR/main and the
// tag path consume one gate instead of two drifting ones.
func TestCIConsumesReusableCandidate(t *testing.T) {
	all := readWorkflows(t)
	body, ok := all["ci.yml"]
	if !ok {
		t.Skip("ci.yml absent; no PR/main workflow to check")
	}
	wf := parse(t, "ci.yml", body)
	job, ok := wf.Jobs["candidate"]
	if !ok {
		t.Fatal("ci.yml has no `candidate` job")
	}
	if job.Uses != reusableRef {
		t.Errorf("ci.yml candidate job must call %q, got %q", reusableRef, job.Uses)
	}
}

// onTriggers returns the keys declared under the top-level `on:` mapping.
// yaml.v2 resolves an unquoted `on` key to the boolean true (YAML 1.1), so both
// spellings are accepted. Presence is judged on the decoded mapping's keys
// rather than on a typed struct field, because `pull_request:` with a null
// value is a valid trigger and still counts as present.
func onTriggers(t *testing.T, name string, body []byte) map[interface{}]interface{} {
	t.Helper()
	var raw map[interface{}]interface{}
	if err := yaml.Unmarshal(body, &raw); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for key, value := range raw {
		if key != true && key != "on" {
			continue
		}
		triggers, ok := value.(map[interface{}]interface{})
		if !ok {
			t.Fatalf("%s `on:` is not a mapping: %T", name, value)
		}
		return triggers
	}
	t.Fatalf("%s has no `on:` mapping", name)
	return nil
}

// TestCIShapePinsTriggersAndCostControls pins the parts of ci.yml that the
// `uses:` assertion above cannot see: pull_request plus push to main,
// read-only permissions, a per-ref concurrency group that cancels superseded
// runs, and a candidate job that declares no secrets of its own. Dropping the
// pull_request trigger, deleting the concurrency block, granting a write
// permission, or passing `secrets: inherit` turns an assertion red. The
// pull_request branch filter is deliberately not pinned, so widening CI to
// every PR is not a test failure.
func TestCIShapePinsTriggersAndCostControls(t *testing.T) {
	body, ok := readWorkflows(t)["ci.yml"]
	if !ok {
		t.Fatal("ci.yml is missing")
	}
	var wf struct {
		On struct {
			Push struct {
				Branches []string `yaml:"branches"`
			} `yaml:"push"`
		} `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Concurrency struct {
			Group            string `yaml:"group"`
			CancelInProgress bool   `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
		Jobs map[string]jobDef `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}
	if !containsString(wf.On.Push.Branches, "main") {
		t.Errorf("ci.yml push branches = %v, want main", wf.On.Push.Branches)
	}
	if _, ok := onTriggers(t, "ci.yml", body)["pull_request"]; !ok {
		t.Error("ci.yml must run on pull_request")
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("ci.yml workflow permissions = %v, want only contents: read", wf.Permissions)
	}
	if !strings.Contains(wf.Concurrency.Group, "github.ref") || !wf.Concurrency.CancelInProgress {
		t.Errorf("ci.yml concurrency = %+v, want a per-ref group with cancel-in-progress: true", wf.Concurrency)
	}
	candidate, ok := wf.Jobs["candidate"]
	if !ok {
		t.Fatal("ci.yml has no `candidate` job")
	}
	if candidate.Permissions["contents"] != "read" {
		t.Errorf("ci.yml candidate permissions = %v, want contents: read", candidate.Permissions)
	}
	if _, ok := candidate.With["ref"]; !ok {
		t.Errorf("ci.yml candidate must pass a ref input to the reusable gate, got %v", candidate.With)
	}
	if candidate.Secrets != nil {
		t.Errorf("ci.yml candidate must not declare secrets: release-candidate.yml declares none and release.yml passes none, got %v", candidate.Secrets)
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// TestCandidateDeclaresRequiredGates guards against a gate being silently dropped.
func TestCandidateDeclaresRequiredGates(t *testing.T) {
	all := readWorkflows(t)
	body, ok := all[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	wf := parse(t, candidateWorkflow, body)
	required := []string{
		"tier",                   // conservative PR classification; main/tags always full
		"policy-tests",           // proposed policy tested separately from trusted classification
		"gate",                   // aggregate fails on unexpected skips/failures/cancellation
		"native",                 // 3-OS build/vet/test/race/shuffle/smoke
		"manifest-platform-diff", // downloaded native manifests prove mark completeness
		"machine-contract",       // compiled-binary contracts
		"staticcheck",            // static analysis
		"govulncheck-main",       // independent main vuln scan
		"govulncheck-repo",       // independent repo vuln scan
		"reproducible-source",    // gofmt + tidy-diff
		"cross-build",            // six cross-builds
		"artifact-verify",        // download/hash/content verification
		"release-config",         // goreleaser check + license/notice + this validator
	}
	for _, job := range required {
		if _, ok := wf.Jobs[job]; !ok {
			t.Errorf("%s is missing required gate job %q", candidateWorkflow, job)
		}
	}
}

// A Linux-only scan misses imports selected exclusively on Darwin or Windows.
func TestGovulncheckRepoCoversReleaseTargets(t *testing.T) {
	wf := parse(t, candidateWorkflow, readWorkflows(t)[candidateWorkflow])
	job, ok := wf.Jobs["govulncheck-repo"]
	if !ok {
		t.Fatal("govulncheck-repo job is missing")
	}
	targets := strings.Fields(wf.Env["TSLINK_RELEASE_TARGETS"])
	wantTargets := []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
	if len(targets) != len(wantTargets) {
		t.Fatalf("govulncheck targets = %v, want exactly six", targets)
	}
	for _, target := range wantTargets {
		if !containsString(targets, target) {
			t.Errorf("govulncheck-repo omits %s", target)
		}
		wantArtifact := "govulncheck-repo-" + strings.ReplaceAll(target, "/", "-")
		artifact := false
		for _, step := range job.Steps {
			if step.With["name"] == wantArtifact && step.With["path"] == wantArtifact+".txt" {
				artifact = true
			}
		}
		if !artifact {
			t.Errorf("govulncheck-repo has no unique report artifact for %s", target)
		}
	}
}

// Run the checked-in step, with only the compiler and scanner replaced. This
// catches a target environment accidentally applied to the host tool install.
func TestGovulncheckRepoExecutesHostToolForForeignTarget(t *testing.T) {
	wf := parse(t, candidateWorkflow, readWorkflows(t)[candidateWorkflow])
	job := wf.Jobs["govulncheck-repo"]
	var stepRun string
	var stepEnv map[string]string
	for _, step := range job.Steps {
		if step.Name == "govulncheck ./..." {
			stepRun, stepEnv = step.Run, step.Env
		}
	}
	if stepRun == "" {
		t.Fatal("govulncheck ./... run block is missing")
	}
	targetOS, targetArch := "windows", "amd64"
	if runtime.GOOS == targetOS {
		targetOS = "darwin"
	}
	if runtime.GOARCH == targetArch {
		targetArch = "arm64"
	}
	if !containsString(strings.Fields(wf.Env["TSLINK_RELEASE_TARGETS"]), targetOS+"/"+targetArch) {
		t.Fatalf("foreign target %s/%s is absent from loop", targetOS, targetArch)
	}
	stepRun = strings.ReplaceAll(stepRun, "${{ matrix.goos }}", targetOS)
	stepRun = strings.ReplaceAll(stepRun, "${{ matrix.goarch }}", targetArch)

	for _, tc := range []struct {
		name          string
		scanExit      string
		installExit   string
		ambientTarget bool
		wantExit      int
	}{
		{"clean", "0", "0", false, 0},
		{"affected", "3", "0", true, 3},
		{"install-failure", "0", "79", false, 79},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binDir := filepath.Join(dir, "stubs")
			gopath := filepath.Join(dir, "gopath")
			if err := os.MkdirAll(binDir, 0700); err != nil {
				t.Fatal(err)
			}
			compiler := `#!/usr/bin/env bash
set -euo pipefail
if [[ "$1" == env && "$2" == GOPATH ]]; then printf '%s\n' "$MOCK_GOPATH"; exit 0; fi
[[ "$1" == install && "$2" == 'golang.org/x/vuln/cmd/govulncheck@v1.6.0' ]] || exit 81
if [[ -n "${GOOS:-}" || -n "${GOARCH:-}" || -n "${CGO_ENABLED:-}" ]]; then
  printf 'foreign tool install: GOOS=%s GOARCH=%s CGO_ENABLED=%s\n' "${GOOS:-}" "${GOARCH:-}" "${CGO_ENABLED:-}" >&2
  exit 82
fi
printf 'host install\n' > "$MOCK_INSTALL_TRACE"
if [[ "${MOCK_INSTALL_EXIT:-0}" != 0 ]]; then exit "$MOCK_INSTALL_EXIT"; fi
mkdir -p "$MOCK_GOPATH/bin"
cp "$MOCK_SCANNER" "$MOCK_GOPATH/bin/govulncheck"
`
			scanner := `#!/usr/bin/env bash
set -euo pipefail
printf '%s/%s/%s:%s\n' "${GOOS:-}" "${GOARCH:-}" "${CGO_ENABLED:-}" "$*" >> "$MOCK_SCAN_TRACE"
printf 'raw advisory warning for %s/%s\n' "$GOOS" "$GOARCH"
exit "$MOCK_SCAN_EXIT"
`
			for name, body := range map[string]string{"go": compiler, "scanner": scanner} {
				if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			installTrace := filepath.Join(dir, "install.trace")
			scanTrace := filepath.Join(dir, "scan.trace")
			cmd := exec.Command("bash", "-c", stepRun)
			cmd.Dir = dir
			cmd.Env = []string{}
			for _, value := range os.Environ() {
				key := strings.SplitN(value, "=", 2)[0]
				if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" && key != "GOBIN" && key != "GOPATH" && key != "PATH" && key != "GOVULNCHECK_VERSION" {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env, "PATH="+binDir+":"+os.Getenv("PATH"), "MOCK_GOPATH="+gopath, "MOCK_SCANNER="+filepath.Join(binDir, "scanner"), "MOCK_INSTALL_TRACE="+installTrace, "MOCK_SCAN_TRACE="+scanTrace, "MOCK_SCAN_EXIT="+tc.scanExit, "MOCK_INSTALL_EXIT="+tc.installExit)
			if tc.ambientTarget {
				cmd.Env = append(cmd.Env, "GOOS="+targetOS, "GOARCH="+targetArch, "CGO_ENABLED=0")
			}
			for _, scope := range []map[string]string{wf.Env, job.Env, stepEnv} {
				for key, value := range scope {
					value = strings.ReplaceAll(strings.ReplaceAll(value, "${{ matrix.goos }}", targetOS), "${{ matrix.goarch }}", targetArch)
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			output, err := cmd.CombinedOutput()
			if tc.wantExit == 0 {
				if err != nil {
					t.Fatalf("workflow run block failed before clean scan: %v\n%s", err, output)
				}
			} else {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != tc.wantExit {
					t.Fatalf("failure exit %d must propagate, got %v\n%s", tc.wantExit, err, output)
				}
			}
			if got, err := os.ReadFile(installTrace); err != nil || string(got) != "host install\n" {
				t.Fatalf("host scanner installation was not reached: %q, %v\n%s", got, err, output)
			}
			if tc.installExit != "0" {
				if _, err := os.Stat(scanTrace); !os.IsNotExist(err) {
					t.Fatalf("scanner ran after failed install: stat error %v", err)
				}
				return
			}
			var wantScan strings.Builder
			for _, target := range strings.Fields(wf.Env["TSLINK_RELEASE_TARGETS"]) {
				wantScan.WriteString(target + "/0:-show verbose ./...\n")
				wantWarning := "raw advisory warning for " + target + "\n"
				path := filepath.Join(dir, "govulncheck-repo-"+strings.ReplaceAll(target, "/", "-")+".txt")
				if got, err := os.ReadFile(path); err != nil || string(got) != wantWarning || !strings.Contains(string(output), wantWarning) {
					t.Fatalf("raw warning lost for %s: artifact %q, err %v, output %q", target, got, err, output)
				}
			}
			if got, err := os.ReadFile(scanTrace); err != nil || string(got) != wantScan.String() {
				t.Fatalf("target scanner invocations = %q, %v; want %q", got, err, wantScan.String())
			}
		})
	}
}

// Execute the workflow's own Bash block, so a missing array declaration or a
// renamed payload can no longer pass a source-text-only assertion.
func TestArtifactVerifyChecksEveryBundledDocument(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The step runs on ubuntu-latest and the Linux release-config job
		// already executes it here. On Windows, "bash" may resolve to Git Bash
		// or to the System32 WSL launcher, and find/sha256sum/sort need not be
		// on its PATH, so a result there would test the runner, not the step.
		t.Skip("executes the ubuntu artifact-verify step with bash; covered on Linux")
	}
	body := readWorkflows(t)[candidateWorkflow]
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["artifact-verify"].Steps {
		if step.Name == "Verify content, license payload, and hashes" {
			script = step.Run
		}
	}
	if script == "" {
		t.Fatal("artifact-verify Bash step is missing")
	}

	var expectedFiles = []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "COMMERCIAL.md"}
	var targets = []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	makeFixture := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, target := range targets {
			payloadDir := filepath.Join(dir, "downloaded", "tslink-"+target)
			if err := os.MkdirAll(payloadDir, 0o700); err != nil {
				t.Fatal(err)
			}
			binary := "tslink"
			if strings.HasPrefix(target, "windows-") {
				binary = "tslink.exe"
			}
			for _, name := range append([]string{binary}, expectedFiles...) {
				if err := os.WriteFile(filepath.Join(payloadDir, name), []byte("fixture\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
		return dir
	}
	run := func(dir string) ([]byte, error) {
		cmd := exec.Command("bash", "-c", script)
		cmd.Dir = dir
		return cmd.CombinedOutput()
	}

	t.Run("complete payload reaches checksum", func(t *testing.T) {
		dir := makeFixture(t)
		out, err := run(dir)
		if err != nil {
			t.Fatalf("complete artifact step failed: %v\n%s", err, out)
		}
		hashes, err := os.ReadFile(filepath.Join(dir, "candidate-artifacts.sha256"))
		if err != nil || len(strings.Split(strings.TrimSpace(string(hashes)), "\n")) != len(targets)*(len(expectedFiles)+1) {
			t.Fatalf("checksum file did not include every payload: %v, %s", err, hashes)
		}
	})
	for _, missing := range expectedFiles {
		t.Run("missing "+missing, func(t *testing.T) {
			dir := makeFixture(t)
			if err := os.Remove(filepath.Join(dir, "downloaded", "tslink-linux-arm64", missing)); err != nil {
				t.Fatal(err)
			}
			out, err := run(dir)
			want := "missing " + missing + " in linux-arm64"
			if err == nil || !strings.Contains(string(out), want) {
				t.Fatalf("missing payload did not fail at the file check: err=%v output=%s", err, out)
			}
		})
	}
}

func TestManifestPlatformDiffNeedsEveryNativeArtifact(t *testing.T) {
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	wf := parse(t, candidateWorkflow, body)
	job, ok := wf.Jobs["manifest-platform-diff"]
	if !ok {
		t.Fatalf("%s has no manifest-platform-diff job", candidateWorkflow)
	}
	if !job.Needs.contains("native") {
		t.Fatalf("manifest-platform-diff must need the full native matrix, got %v", job.Needs)
	}
	text := string(body)
	for _, want := range []string{
		"go run ./tools/gen-manifest -output \"${RUNNER_TEMP}/cli-manifest.json\"",
		"name: cli-manifest-${{ matrix.os }}",
		"pattern: cli-manifest-*",
		"go run ./tools/check-manifest-platforms",
		"-darwin downloaded-manifests/cli-manifest-macos-latest/cli-manifest.json",
		"-linux downloaded-manifests/cli-manifest-ubuntu-latest/cli-manifest.json",
		"-windows downloaded-manifests/cli-manifest-windows-latest/cli-manifest.json",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s missing manifest diff wiring %q", candidateWorkflow, want)
		}
	}
}

func TestDarwinManifestStrictCheckRunsInExistingNativeJob(t *testing.T) {
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	text := string(body)
	want := strings.Join([]string{
		"- name: CLI manifest fixture is current (Darwin)",
		"        if: matrix.os == 'macos-latest'",
		"        shell: bash",
		"        run: |",
		"          output=\"$(go run ./tools/gen-manifest -check)\"",
		"          printf '%s\\n' \"${output}\"",
		"          test \"${output}\" = \"" + manifestcheck.StrictUpToDateMessage + "\"",
	}, "\n")
	if !strings.Contains(text, want) {
		t.Fatalf("%s must run the strict manifest check inside its existing macOS native matrix job", candidateWorkflow)
	}
}

func TestManifestCheckStepsNameFullFixtureCoverage(t *testing.T) {
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	for _, platform := range []string{"Linux", "Darwin"} {
		name := "- name: CLI manifest fixture is current (" + platform + ")"
		if !strings.Contains(string(body), name) {
			t.Errorf("%s must name the strict fixture check: %s", candidateWorkflow, name)
		}
	}
	linuxStep := "- name: CLI manifest fixture is current (Linux)\n        run: go run ./tools/gen-manifest -check"
	if !strings.Contains(string(body), linuxStep) {
		t.Errorf("%s Linux manifest step must run exactly go run ./tools/gen-manifest -check", candidateWorkflow)
	}
}

// goreleaserFeatureFloors maps a GoReleaser config key to the minimum GoReleaser
// version that can PARSE it. When one of these keys appears in .goreleaser.yml,
// every workflow pin that runs `goreleaser check`/`release` must be at least the
// floor version or the pinned tool hard-fails on an "unknown field" unmarshal.
var goreleaserFeatureFloors = map[string][3]int{
	// `brews` was deprecated and replaced by `homebrew_casks` in GoReleaser 2.11.
	"homebrew_casks": {2, 11, 0},
}

// goreleaserVersionPins returns the `version:` pinned under every
// `goreleaser/goreleaser-action` step in a workflow file, in document order.
func goreleaserVersionPins(text string) []string {
	var pins []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "goreleaser/goreleaser-action") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if strings.HasPrefix(trimmed, "version:") {
			v := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
			v = strings.Trim(v, "\"'")
			pins = append(pins, v)
			inBlock = false
			continue
		}
		// A new list item starts a new step before we saw a version pin.
		if strings.HasPrefix(trimmed, "- ") {
			inBlock = false
		}
	}
	return pins
}

func parseSemver(t *testing.T, v string) [3]int {
	t.Helper()
	raw := strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("cannot parse GoReleaser version %q as major.minor.patch", v)
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			t.Fatalf("cannot parse GoReleaser version %q component %q: %v", v, p, err)
		}
		out[i] = n
	}
	return out
}

func semverLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// TestGoReleaserVersionPinsSatisfyConfigFeatures guards GoReleaser parser compatibility.
//
// It fails on the exact skew that made the release-candidate gate red: both the
// `release-config` check job and the tag `publish` job pinned goreleaser v2.9.0,
// while .goreleaser.yml uses `homebrew_casks` (needs >= 2.11). v2.9.0 hard-fails
// on that key with `field homebrew_casks not found`, so `goreleaser check`
// exited 1 deterministically. The test also fails on any skew between the pins
// (a check job and a publish job validating/publishing with different versions).
func TestGoReleaserVersionPinsSatisfyConfigFeatures(t *testing.T) {
	config, err := os.ReadFile(filepath.Join(repoRoot(t), ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}
	floor := [3]int{0, 0, 0}
	var floorReasons []string
	for key, min := range goreleaserFeatureFloors {
		if regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:`).Match(config) {
			floorReasons = append(floorReasons, key)
			if semverLess(floor, min) {
				floor = min
			}
		}
	}

	type pin struct {
		file    string
		version string
	}
	var pins []pin
	for name, body := range readWorkflows(t) {
		for _, v := range goreleaserVersionPins(string(body)) {
			pins = append(pins, pin{file: name, version: v})
		}
	}
	if len(pins) == 0 {
		t.Fatal("no goreleaser/goreleaser-action version pins found in any workflow")
	}

	// Every pin must satisfy the feature floor implied by the config.
	for _, p := range pins {
		got := parseSemver(t, p.version)
		if semverLess(got, floor) {
			t.Errorf("%s pins GoReleaser %s but the config uses %v which needs >= %d.%d.%d",
				p.file, p.version, floorReasons, floor[0], floor[1], floor[2])
		}
	}

	// No skew: every workflow that runs goreleaser must pin the same version,
	// so the version validated by `check` is the version that `release` uses.
	canonical := pins[0].version
	for _, p := range pins[1:] {
		if p.version != canonical {
			t.Errorf("GoReleaser version skew: %s pins %s but %s pins %s; all pins must match",
				p.file, p.version, pins[0].file, canonical)
		}
	}
}

func TestReleaseVerifyScriptIsReadOnly(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "release-verify.sh"))
	if err != nil {
		t.Fatalf("read scripts/release-verify.sh: %v", err)
	}
	text := string(body)
	for _, forbidden := range []string{
		"--execute",
		"SCRATCH_REPO",
		"SCRATCH_TAP",
		"execute-mode external steps are disabled",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("release-verify.sh still advertises or contains fake execute mode text %q", forbidden)
		}
	}
	for _, want := range []string{
		"READ-ONLY mode",
		"no external mutation",
		"external publish/sign/attest/install gates",
		"Unknown external gates make the script exit nonzero",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("release-verify.sh missing read-only/fail-closed text %q", want)
		}
	}
}

// compiledSelectorRE captures the `-run '<selector>'` used by the dedicated
// machine-contract binary job in release-candidate.yml.
var compiledSelectorRE = regexp.MustCompile(`go test [^\n]*-run '([^']+)' \./cmd/\.\.\.`)

// compiledTestNameRE finds every `func TestCompiled...` declaration.
var compiledTestNameRE = regexp.MustCompile(`func (TestCompiled\w+)\s*\(`)

// TestCompiledSelectorCoversAllCompiledTests is the compiled-contract-gate guard.
//
// The dedicated `machine-contract` job runs a single `go test -run '<selector>'`.
// A narrow substring selector (`BinaryContract|MachineContract`) silently ran
// only 2 of 7 TestCompiled* tests. This test enumerates every current
// TestCompiled* name and proves the pinned selector matches all of them, so a
// newly added compiled contract cannot escape the dedicated gate unnoticed.
func TestCompiledSelectorCoversAllCompiledTests(t *testing.T) {
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	m := compiledSelectorRE.FindStringSubmatch(string(body))
	if m == nil {
		t.Fatalf("could not find the machine-contract `go test -run '<selector>' ./cmd/...` in %s", candidateWorkflow)
	}
	selector := m[1]
	selectorRE, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("selector %q is not a valid regexp: %v", selector, err)
	}

	cmdDir := filepath.Join(repoRoot(t), "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		t.Fatalf("read %s: %v", cmdDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(cmdDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, sm := range compiledTestNameRE.FindAllStringSubmatch(string(src), -1) {
			names = append(names, sm[1])
		}
	}
	if len(names) < 5 {
		t.Fatalf("expected several TestCompiled* tests in cmd/, found %d (%v); enumeration likely broke", len(names), names)
	}

	for _, name := range names {
		if !selectorRE.MatchString(name) {
			t.Errorf("compiled-contract selector %q does not run %q; a compiled contract would be silently skipped", selector, name)
		}
	}

	// Negative control: the historical narrow selector must NOT cover every
	// current TestCompiled* name. If it did, this guard would be vacuous.
	narrow := regexp.MustCompile("BinaryContract|MachineContract")
	covered := 0
	for _, name := range names {
		if narrow.MatchString(name) {
			covered++
		}
	}
	if covered == len(names) {
		t.Errorf("the historical narrow selector already covers all %d TestCompiled* tests; guard is vacuous", len(names))
	}
}

var (
	nativeSmokeCommandsBlockRE = regexp.MustCompile(`(?s)commands=\(\s*(.*?)\s*\)`)
	doubleQuotedCommandRE      = regexp.MustCompile(`"([^"]+)"`)
)

func nativeSmokeCommands(t *testing.T) []string {
	t.Helper()
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	match := nativeSmokeCommandsBlockRE.FindSubmatch(body)
	if match == nil {
		t.Fatalf("%s does not define the native smoke commands array", candidateWorkflow)
	}
	var commands []string
	for _, m := range doubleQuotedCommandRE.FindAllStringSubmatch(string(match[1]), -1) {
		commands = append(commands, m[1])
	}
	if len(commands) == 0 {
		t.Fatalf("%s native smoke commands array is empty", candidateWorkflow)
	}
	return commands
}

func manifestCommandPaths(t *testing.T) map[string]struct{} {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "cli-manifest.json"))
	if err != nil {
		t.Fatalf("read docs/cli-manifest.json: %v", err)
	}
	var manifest struct {
		Commands []struct {
			Path string `json:"path"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("parse docs/cli-manifest.json: %v", err)
	}
	paths := make(map[string]struct{}, len(manifest.Commands))
	for _, cmd := range manifest.Commands {
		paths[cmd.Path] = struct{}{}
	}
	if len(paths) == 0 {
		t.Fatal("manifest contains no command paths")
	}
	return paths
}

func smokeCommandPath(command string) string {
	var path []string
	for _, field := range strings.Fields(command) {
		if strings.HasPrefix(field, "-") {
			break
		}
		path = append(path, field)
	}
	if len(path) == 0 {
		return "tslink"
	}
	return "tslink " + strings.Join(path, " ")
}

func TestNativeSmokeCommandsAreManifestBacked(t *testing.T) {
	paths := manifestCommandPaths(t)
	rootOnly := map[string]struct{}{
		"--help":           {},
		"--version":        {},
		"--version --json": {},
	}
	commands := nativeSmokeCommands(t)
	var hasHumanVersion, hasJSONVersion bool
	for _, command := range commands {
		if command == "version" {
			t.Fatalf("native smoke uses nonexistent `tslink version`; root --version is the supported contract")
		}
		if _, ok := rootOnly[command]; ok {
			if command == "--version" {
				hasHumanVersion = true
			}
			if command == "--version --json" {
				hasJSONVersion = true
			}
			continue
		}
		path := smokeCommandPath(command)
		if _, ok := paths[path]; !ok {
			t.Fatalf("native smoke command %q maps to unknown manifest path %q", command, path)
		}
	}
	if !hasHumanVersion {
		t.Fatal("native smoke must exercise root --version")
	}
	if !hasJSONVersion {
		t.Fatal("native smoke must preserve the root --version --json machine contract")
	}
}
