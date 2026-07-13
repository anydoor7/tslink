package main

import (
	"strconv"
	"testing"
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
	rs := Evaluate(fakeFetcher{}, "monody0007/tslink", false)
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
			"required_status_checks":{"contexts":["Release candidate gate"]},
			"required_pull_request_reviews":{}}`, nil},
		"/repos/o/r/rulesets":             {200, `[{"name":"tags","enforcement":"active","target":"tag","conditions":{"ref_name":{"include":["refs/tags/v*"]}}}]`, nil},
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

func TestLoneReleaseCandidateLeafContextIsNotReady(t *testing.T) {
	for _, leaf := range mandatoryReleaseCandidateLeafContexts {
		f := fakeFetcher{resp: map[string]struct {
			code int
			body string
			err  error
		}{
			"/repos/o/r/branches/main/protection": {200, `{
				"required_status_checks":{"contexts":[` + quoteJSON(leaf) + `]},
				"required_pull_request_reviews":{}}`, nil},
		}}
		if got := checkBranchProtection(f, "o/r").Status; got != NotReady {
			t.Fatalf("lone release-candidate leaf %q = %s, want NOT_READY", leaf, got)
		}
	}
}

func TestPartialReleaseCandidateLeafSetIsNotReady(t *testing.T) {
	partial := append([]string(nil), mandatoryReleaseCandidateLeafContexts...)
	partial = partial[:len(partial)-1]
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/branches/main/protection": {200, `{
			"required_status_checks":{"contexts":` + jsonStringArray(partial) + `},
			"required_pull_request_reviews":{}}`, nil},
	}}
	if got := checkBranchProtection(f, "o/r").Status; got != NotReady {
		t.Fatalf("partial release-candidate leaf set = %s, want NOT_READY", got)
	}
}

func TestCompleteReleaseCandidateLeafSetIsReady(t *testing.T) {
	f := fakeFetcher{resp: map[string]struct {
		code int
		body string
		err  error
	}{
		"/repos/o/r/branches/main/protection": {200, `{
			"required_status_checks":{"contexts":` + jsonStringArray(mandatoryReleaseCandidateLeafContexts) + `},
			"required_pull_request_reviews":{}}`, nil},
	}}
	if got := checkBranchProtection(f, "o/r").Status; got != Ready {
		t.Fatalf("complete release-candidate leaf set = %s, want READY", got)
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
