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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/manifestcheck"
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
	Permissions map[string]string `yaml:"permissions"`
}

type workflowFile struct {
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
// ci.yml was deliberately removed in bb943b5 ("remove automatic
// private-development checks"), so on a private repo there is no PR/main
// workflow for this invariant to hold over and the test skips. The assertion is
// kept rather than deleted because the invariant it guards -- PR/main must call
// the same reusable candidate gate as the tag path -- becomes live again the
// moment ci.yml comes back, which is what publishing this repo would do.
// Deleting the test would drop the guard silently at exactly that point.
func TestCIConsumesReusableCandidate(t *testing.T) {
	all := readWorkflows(t)
	body, ok := all["ci.yml"]
	if !ok {
		t.Skip("ci.yml absent (removed in bb943b5); no PR/main workflow to check")
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

// TestCandidateDeclaresRequiredGates guards against a gate being silently dropped.
func TestCandidateDeclaresRequiredGates(t *testing.T) {
	all := readWorkflows(t)
	body, ok := all[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	wf := parse(t, candidateWorkflow, body)
	required := []string{
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
		"- name: CLI manifest fixture is current (Darwin strict)",
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

func TestLinuxManifestCheckStepNamesReducedCoverage(t *testing.T) {
	body, ok := readWorkflows(t)[candidateWorkflow]
	if !ok {
		t.Fatalf("%s is missing", candidateWorkflow)
	}
	if !strings.Contains(string(body), "- name: CLI manifest platform-independent fields are current (Linux)") {
		t.Fatalf("%s must disclose the Linux manifest check's reduced coverage in the step name", candidateWorkflow)
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
