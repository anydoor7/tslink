package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v2"
)

// fakeFetcher returns canned responses keyed by path.
type fakeFetcher struct {
	resp map[string]struct {
		code int
		body string
		err  error
	}
}

func (f fakeFetcher) Get(path string) (int, []byte, error) {
	r, ok := f.resp[path]
	if !ok {
		return 404, []byte(`{}`), nil
	}
	return r.code, []byte(r.body), r.err
}

func statusOf(rs []Result, control string) Status {
	for _, r := range rs {
		if r.Control == control {
			return r.Status
		}
	}
	return Status("MISSING")
}

func TestNoTokenIsAllUnknown(t *testing.T) {
	rs := Evaluate(fakeFetcher{}, "anydoor7/tslink", false)
	if allReady(rs) {
		t.Fatal("no token must never be ready")
	}
	for _, r := range rs {
		if r.Status != Unknown {
			t.Errorf("control %s = %s, want UNKNOWN without a token", r.Control, r.Status)
		}
	}
}

func TestForbiddenIsUnknownNotNotReady(t *testing.T) {
	// A private-plan 403 must be UNKNOWN (cannot verify), not NOT_READY and not
	// READY. This is the core fail-closed repository-readiness contract.
	mk := func(code int) fakeFetcher {
		return fakeFetcher{resp: map[string]struct {
			code int
			body string
			err  error
		}{
			"/repos/o/r":                          {code, `{}`, nil},
			"/repos/o/r/branches/main/protection": {code, `{}`, nil},
			"/repos/o/r/rulesets":                 {code, `[]`, nil},
			"/repos/o/r/environments":             {code, `{}`, nil},
			"/repos/o/r/vulnerability-alerts":     {code, ``, nil},
		}}
	}
	rs := Evaluate(mk(403), "o/r", true)
	for _, r := range rs {
		if r.Status != Unknown {
			t.Errorf("403 control %s = %s, want UNKNOWN", r.Control, r.Status)
		}
	}
	if allReady(rs) {
		t.Fatal("403 everywhere must not be ready")
	}
}

func TestFullyConfiguredIsReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r": {200, `{"visibility":"public","private":false,
			"security_and_analysis":{"secret_scanning":{"status":"enabled"}}}`, nil},
		"/repos/o/r/branches/main/protection": {200, `{
			"required_status_checks":{"contexts":["Release candidate gate / gate"],
				"checks":[{"context":"Release candidate gate / gate","app_id":15368}]},
			"required_pull_request_reviews":{}}`, nil},
		// Shapes as GitHub returns them: the list has no conditions.
		"/repos/o/r/rulesets":             {200, `[{"id":7,"name":"tags","enforcement":"active","target":"tag","source_type":"Repository"}]`, nil},
		"/repos/o/r/rulesets/7":           {200, `{"id":7,"name":"tags","enforcement":"active","target":"tag","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":[]}},"rules":[{"type":"update"},{"type":"deletion"}]}`, nil},
		"/repos/o/r/environments":         {200, `{"environments":[{"name":"release"}]}`, nil},
		"/repos/o/r/environments/release": {200, `{"protection_rules":[{"type":"required_reviewers","reviewers":[{"type":"User","reviewer":{"login":"owner"}}]}],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":true}}`, nil},
		"/repos/o/r/vulnerability-alerts": {204, ``, nil},
	}}
	rs := Evaluate(f, "o/r", true)
	if !allReady(rs) {
		for _, r := range rs {
			t.Logf("%s = %s (%s)", r.Control, r.Status, r.Detail)
		}
		t.Fatal("fully configured repo should be READY on every control")
	}
}

func TestUnrelatedBranchContextIsNotReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/branches/main/protection": {200, `{
			"required_status_checks":{"contexts":["lint"]},
			"required_pull_request_reviews":{}}`, nil},
	}}
	if got := checkBranchProtection(f, "o/r").Status; got != NotReady {
		t.Fatalf("unrelated status context = %s, want NOT_READY", got)
	}
}

// The context names below were required before the release-candidate jobs
// were consolidated and the aggregate gate was added. None is emitted any
// more, so none of them may count as the gate.
var staleReleaseCandidateContexts = []string{
	"Native build/vet/test/race (ubuntu-latest)",
	"Native build/vet/test/race (macos-latest)",
	"Native build/vet/test/race (windows-latest)",
	"Compiled-binary machine contracts (ubuntu-latest)",
	"Compiled-binary machine contracts (macos-latest)",
	"Compiled-binary machine contracts (windows-latest)",
	"Staticcheck",
	"govulncheck (main module)",
	"govulncheck (repo)",
	"gofmt + tidy-diff (read-only source proof)",
	"Cross build (darwin/amd64)",
	"Cross build (darwin/arm64)",
	"Cross build (linux/amd64)",
	"Cross build (linux/arm64)",
	"Cross build (windows/amd64)",
	"Cross build (windows/arm64)",
	"Artifact download + hash + content verify",
	"GoReleaser config + license/notice presence",
}

func protectionWithContexts(contexts string) fakeFetcher {
	return fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/branches/main/protection": {200, `{
			"required_status_checks":` + contexts + `,
			"required_pull_request_reviews":{}}`, nil},
	}}
}

func TestStaleReleaseCandidateContextsAreNotReady(t *testing.T) {
	for name, contexts := range map[string][]string{
		"caller job name alone":            {"Release candidate gate"},
		"pre-consolidation leaf set":       staleReleaseCandidateContexts,
		"one pre-consolidation leaf":       staleReleaseCandidateContexts[:1],
		"leaf set under the caller prefix": prefixed("Release candidate gate / ", staleReleaseCandidateContexts),
		"aggregate job name alone":         {"gate"},
	} {
		t.Run(name, func(t *testing.T) {
			f := protectionWithContexts(`{"contexts":` + jsonStringArray(contexts) + `}`)
			if got := checkBranchProtection(f, "o/r").Status; got != NotReady {
				t.Fatalf("required contexts %v = %s, want NOT_READY", contexts, got)
			}
		})
	}
}

func TestEmittedAggregateContextIsReady(t *testing.T) {
	for name, checks := range map[string]string{
		"contexts list":    `{"contexts":["lint","Release candidate gate / gate"]}`,
		"checks list only": `{"contexts":[],"checks":[{"context":"Release candidate gate / gate","app_id":15368}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := checkBranchProtection(protectionWithContexts(checks), "o/r")
			if got.Status != Ready {
				t.Fatalf("required status checks %s = %s, want READY", checks, got.Status)
			}
		})
	}
	t.Run("both lists name one check", func(t *testing.T) {
		got := checkBranchProtection(protectionWithContexts(`{"contexts":["Release candidate gate / gate"],"checks":[{"context":"Release candidate gate / gate","app_id":15368}]}`), "o/r")
		if got.Status != Ready || !strings.Contains(got.Detail, "among 1 required contexts") {
			t.Fatalf("one check listed twice = %s (%s), want READY among 1 required contexts", got.Status, got.Detail)
		}
	})
}

func prefixed(prefix string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, prefix+value)
	}
	return out
}

// TestAggregateContextMatchesWorkflows ties the required context to the job
// names GitHub combines into it, so renaming either job fails here instead of
// silently making every repository look unprotected.
func TestAggregateContextMatchesWorkflows(t *testing.T) {
	type namedJobs struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	read := func(name string) namedJobs {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		var wf namedJobs
		if err := yaml.Unmarshal(body, &wf); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return wf
	}
	gate, ok := read("release-candidate.yml").Jobs["gate"]
	if !ok || gate.Name == "" {
		t.Fatal("release-candidate.yml has no named aggregate `gate` job")
	}
	caller, ok := read("ci.yml").Jobs["candidate"]
	if !ok || caller.Name == "" || caller.Uses != "./.github/workflows/release-candidate.yml" {
		t.Fatalf("ci.yml candidate job = %+v, want a named call of release-candidate.yml", caller)
	}
	if want := caller.Name + " / " + gate.Name; releaseCandidateAggregateContext != want {
		t.Fatalf("releaseCandidateAggregateContext = %q, but the workflows emit %q", releaseCandidateAggregateContext, want)
	}
}

func TestRulesetConditionsComeFromDetail(t *testing.T) {
	list := `[{"id":7,"name":"tags","enforcement":"active","target":"tag"},{"id":8,"name":"main","enforcement":"active","target":"branch"}]`
	for _, tc := range []struct {
		name       string
		detailCode int
		detailBody string
		want       Status
	}{
		{"detail includes v tags", 200, `{"enforcement":"active","target":"tag","conditions":{"ref_name":{"include":["refs/tags/v*"]}}}`, Ready},
		{"detail covers other tags", 200, `{"enforcement":"active","target":"tag","conditions":{"ref_name":{"include":["refs/tags/release-*"]}}}`, NotReady},
		{"detail disabled", 200, `{"enforcement":"disabled","target":"tag","conditions":{"ref_name":{"include":["refs/tags/v*"]}}}`, NotReady},
		{"detail forbidden", 403, `{}`, Unknown},
		{"detail unreadable", 500, `{}`, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No entry for /rulesets/8: the branch ruleset must not be fetched.
			f := fakeFetcher{resp: map[string]struct {
				code int
				body string
				err  error
			}{
				"/repos/o/r/rulesets":   {200, list, nil},
				"/repos/o/r/rulesets/7": {tc.detailCode, tc.detailBody, nil},
			}}
			if got := checkRulesets(f, "o/r"); got.Status != tc.want {
				t.Fatalf("checkRulesets = %s (%s), want %s", got.Status, got.Detail, tc.want)
			}
		})
	}
}

// tagRulesets serves one active refs/tags/v* ruleset per exclude list, as
// GitHub returns them: summaries in the list, conditions in each detail.
func tagRulesets(excludes ...string) fakeFetcher {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{}}
	var list []string
	for i, exclude := range excludes {
		id := strconv.Itoa(7 + i)
		list = append(list, `{"id":`+id+`,"enforcement":"active","target":"tag"}`)
		f.resp["/repos/o/r/rulesets/"+id] = struct {
			code int
			body string
			err  error
		}{200, `{"id":` + id + `,"enforcement":"active","target":"tag","conditions":{"ref_name":{"include":["refs/tags/v*"],"exclude":` + exclude + `}},"rules":[{"type":"update"},{"type":"deletion"}]}`, nil}
	}
	f.resp["/repos/o/r/rulesets"] = struct {
		code int
		body string
		err  error
	}{200, "[" + strings.Join(list, ",") + "]", nil}
	return f
}

// TestRulesetExclusionsDefeatReleaseTagCoverage: a ruleset whose exclusions
// can match a v* release tag does not protect release tags, one with an
// exclusion this tool cannot evaluate is UNKNOWN, and exclusions outside
// refs/tags/v leave it READY.
func TestRulesetExclusionsDefeatReleaseTagCoverage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		excludes []string
		want     Status
	}{
		{"no exclusions", []string{`[]`}, Ready},
		{"exclusions omitted", []string{`null`}, Ready},
		{"other tags excluded", []string{`["refs/tags/nightly-*"]`}, Ready},
		{"another literal tag excluded", []string{`["refs/tags/nightly"]`}, Ready},
		{"branches excluded", []string{`["refs/heads/v*","~DEFAULT_BRANCH"]`}, Ready},
		{"every release tag excluded", []string{`["refs/tags/v*"]`}, NotReady},
		{"one release tag excluded", []string{`["refs/tags/nightly-*","refs/tags/v0.1.0"]`}, NotReady},
		{"literal release tag beyond the witnesses", []string{`["refs/tags/v9.9.9"]`}, NotReady},
		{"pre-release tags excluded", []string{`["refs/tags/v*-rc*"]`}, NotReady},
		{"every tag excluded", []string{`["refs/tags/*"]`}, NotReady},
		{"bare tag pattern excluded", []string{`["v*"]`}, NotReady},
		{"all refs excluded", []string{`["~ALL"]`}, NotReady},
		{"wildcard before the tag name", []string{`["refs/*/v*"]`}, NotReady},
		{"class where the tag name starts", []string{`["refs/tags/[uvw]*"]`}, NotReady},
		{"negated class where the tag name starts", []string{`["refs/tags/[!n]*"]`}, NotReady},
		{"leading ** spans directories", []string{`["**/v*"]`}, NotReady},
		{"match beats an unclear exclusion", []string{`["refs/tags/v9*","refs/tags/v*"]`}, NotReady},
		// File.fnmatch matches each of the next three against refs/tags/v9.0.0
		// or refs/tags/v2.0.0, a release tag none of the witnesses matches.
		{"release tags beyond the witnesses", []string{`["refs/tags/v9*"]`}, Unknown},
		{"class in refs, beyond the witnesses", []string{`["ref[s]/tags/v9*"]`}, Unknown},
		{"wildcards in refs, beyond the witnesses", []string{`["r?fs/*/v2.*"]`}, Unknown},
		// Read as a tag name, v9* matches v9.0.0.
		{"bare pattern beyond the witnesses", []string{`["v9*"]`}, Unknown},
		{"slash pattern without a literal refs/", []string{`["tags/v*"]`}, Unknown},
		// File.fnmatch matches neither against a release tag, but neither is
		// provably disjoint from refs/tags/v.
		{"negated class that skips release tags", []string{`["refs/tags/[!v]*"]`}, Unknown},
		{"class that would have to match a slash", []string{`["refs[^x]tags/v*"]`}, Unknown},
		{"unknown token", []string{`["~NEW_TOKEN"]`}, Unknown},
		{"another ruleset covers", []string{`["refs/tags/v*"]`, `[]`}, Ready},
		{"another ruleset is unclear", []string{`["refs/tags/v*"]`, `["refs/tags/v9*"]`}, Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkRulesets(tagRulesets(tc.excludes...), "o/r")
			if got.Status != tc.want {
				t.Fatalf("exclusions %v: checkRulesets = %s (%s), want %s", tc.excludes, got.Status, got.Detail, tc.want)
			}
		})
	}
}

// TestFullRefWildcardExclusionsAreNotReady: File.fnmatch with FNM_PATHNAME
// matches each pattern against refs/tags/v0.1.0 through a wildcard or class in
// the refs part, so the ruleset protects no release tag.
func TestFullRefWildcardExclusionsAreNotReady(t *testing.T) {
	for _, pattern := range []string{"ref[s]/tags/v*", "ref*/tags/v*", "refs*/tags/v*", "ref?/tags/v*", "r?fs/tags/v*"} {
		got := checkRulesets(tagRulesets(jsonStringArray([]string{pattern})), "o/r")
		if got.Status != NotReady {
			t.Errorf("exclusion %q: checkRulesets = %s (%s), want NOT_READY", pattern, got.Status, got.Detail)
		}
	}
}

type fnmatchCase struct {
	Pattern string `json:"pattern"`
	Witness string `json:"witness"`
}

// fnmatchCorpus reads the Ruby File.fnmatch results in
// testdata/fnmatch-corpus.json; testdata/README.md says how they were made.
func fnmatchCorpus(t *testing.T) []fnmatchCase {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "fnmatch-corpus.json"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus []fnmatchCase
	if err := json.Unmarshal(body, &corpus); err != nil {
		t.Fatal(err)
	}
	// A truncated corpus would let the sweeps pass without checking anything.
	matching := 0
	for _, c := range corpus {
		if c.Witness != "" {
			matching++
		}
	}
	if len(corpus) != 127 || matching != 76 {
		t.Fatalf("corpus has %d patterns, %d matching a release ref; want 127 and 76", len(corpus), matching)
	}
	return corpus
}

// TestExclusionsTheFnmatchOracleMatchesAreNeverReady: no exclusion that
// File.fnmatch matches against a release ref lets its ruleset count, and the
// witnesses prove every such match in the corpus.
func TestExclusionsTheFnmatchOracleMatchesAreNeverReady(t *testing.T) {
	falseReady, unknown := 0, 0
	for _, c := range fnmatchCorpus(t) {
		if c.Witness == "" {
			continue
		}
		got := checkRulesets(tagRulesets(jsonStringArray([]string{c.Pattern})), "o/r")
		switch got.Status {
		case Ready:
			falseReady++
			t.Errorf("false READY: exclusion %q matches %s (%s)", c.Pattern, c.Witness, got.Detail)
		case Unknown:
			unknown++
			t.Errorf("exclusion %q matches %s: checkRulesets = UNKNOWN (%s), want NOT_READY", c.Pattern, c.Witness, got.Detail)
		}
	}
	if falseReady+unknown > 0 {
		t.Logf("matching patterns: %d READY, %d UNKNOWN", falseReady, unknown)
	}
}

// TestFnmatchOracleRejectionsAreNotMatches: an exclusion that File.fnmatch
// matches against no release ref is never reported as excluding release
// tags. Patterns without a slash are skipped, since they are also read as tag
// names on purpose.
func TestFnmatchOracleRejectionsAreNotMatches(t *testing.T) {
	for _, c := range fnmatchCorpus(t) {
		if c.Witness != "" || !strings.Contains(c.Pattern, "/") {
			continue
		}
		if match, _ := excludeMayMatchReleaseTags(c.Pattern); match {
			t.Errorf("exclusion %q matches no release ref, but counts as matching", c.Pattern)
		}
	}
}

func TestUnrelatedRulesetTargetIsNotReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/rulesets": {200, `[{"name":"main branch","enforcement":"active","target":"branch","conditions":{"ref_name":{"include":["refs/heads/main"]}}}]`, nil},
	}}
	if got := checkRulesets(f, "o/r").Status; got != NotReady {
		t.Fatalf("unrelated ruleset = %s, want NOT_READY", got)
	}
}

func quoteJSON(s string) string {
	return strconv.Quote(s)
}

func jsonStringArray(values []string) string {
	out := "["
	for i, value := range values {
		if i > 0 {
			out += ","
		}
		out += quoteJSON(value)
	}
	return out + "]"
}

func TestReleaseEnvironmentWithoutProtectionIsNotReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/environments":         {200, `{"environments":[{"name":"release"}]}`, nil},
		"/repos/o/r/environments/release": {200, `{"protection_rules":[],"deployment_branch_policy":{"protected_branches":false,"custom_branch_policies":false}}`, nil},
	}}
	if got := checkReleaseEnvironment(f, "o/r").Status; got != NotReady {
		t.Fatalf("unprotected release environment = %s, want NOT_READY", got)
	}
}

func TestMissingControlsAreNotReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r":                          {200, `{"visibility":"private","private":true}`, nil},
		"/repos/o/r/branches/main/protection": {404, `{}`, nil},
		"/repos/o/r/rulesets":                 {200, `[]`, nil},
		"/repos/o/r/environments":             {200, `{"environments":[]}`, nil},
		"/repos/o/r/vulnerability-alerts":     {404, ``, nil},
	}}
	rs := Evaluate(f, "o/r", true)
	wantNotReady := []string{"visibility", "branch-protection:main", "rulesets", "release-environment", "vulnerability-alerts"}
	for _, c := range wantNotReady {
		if got := statusOf(rs, c); got != NotReady {
			t.Errorf("control %s = %s, want NOT_READY", c, got)
		}
	}
}
