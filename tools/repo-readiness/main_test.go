package main

import "testing"

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
	// READY. This is the core fail-closed contract for readiness.
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
			"required_status_checks":{"contexts":["candidate"]},
			"required_pull_request_reviews":{}}`, nil},
		"/repos/o/r/rulesets":             {200, `[{"name":"tags","enforcement":"active"}]`, nil},
		"/repos/o/r/environments":         {200, `{"environments":[{"name":"release"}]}`, nil},
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
