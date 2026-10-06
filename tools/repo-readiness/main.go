// Command repo-readiness performs a READ-ONLY audit of a GitHub repository's
// public-transition controls: visibility, branch protection, rulesets,
// the protected release environment, vulnerability alerts, and secret scanning.
//
// It issues only HTTP GET requests and never mutates repository settings. Any
// control that cannot be read (missing token, private-plan 401/403, or a 404
// that means "unreadable") is reported UNKNOWN and treated as a failure, so a
// zero exit means "verified ready", never "assumed ready". Live readback and
// negative protection tests remain an external gate; this tool only reports the
// current control-plane state it can read.
//
//	GITHUB_TOKEN=<read-only> go run ./tools/repo-readiness -repo anydoor7/tslink
//	GITHUB_TOKEN=<read-only> go run ./tools/repo-readiness -repo anydoor7/tslink -json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// releaseCandidateAggregateContext is the status-check context that the
// always-running aggregate job emits on pull requests and main. GitHub names a
// job of a called reusable workflow "<caller job name> / <called job name>":
// the ci.yml `candidate` job is named "Release candidate gate" and the
// release-candidate.yml aggregate job is named "gate". The caller name alone is
// never reported as a check, and the per-target leaf jobs were consolidated, so
// this aggregate is the only context that proves the whole gate ran.
// TestAggregateContextMatchesWorkflows derives it from the workflow files.
const releaseCandidateAggregateContext = "Release candidate gate / gate"

// Status is the classification of one control.
type Status string

const (
	Ready    Status = "READY"
	NotReady Status = "NOT_READY"
	Unknown  Status = "UNKNOWN"
)

// Result is one control's audited state.
type Result struct {
	Control string `json:"control"`
	Status  Status `json:"status"`
	Detail  string `json:"detail"`
}

// Fetcher issues read-only GET requests. Implementations MUST NOT perform any
// mutating HTTP method; the interface deliberately exposes only Get.
type Fetcher interface {
	Get(path string) (statusCode int, body []byte, err error)
}

func main() {
	repo := flag.String("repo", "", "owner/name of the repository to audit")
	asJSON := flag.Bool("json", false, "emit the report as JSON")
	flag.Parse()

	if *repo == "" || !strings.Contains(*repo, "/") {
		fmt.Fprintln(os.Stderr, "repo-readiness: -repo owner/name is required")
		os.Exit(2)
	}

	token := os.Getenv("GITHUB_TOKEN")
	f := &httpFetcher{token: token, base: "https://api.github.com"}

	results := Evaluate(f, *repo, token != "")

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			Repo    string   `json:"repo"`
			Results []Result `json:"results"`
			Ready   bool     `json:"ready"`
		}{*repo, results, allReady(results)})
	} else {
		fmt.Printf("# Repository readiness: %s\n\n", *repo)
		fmt.Printf("| Control | Status | Detail |\n|---|---|---|\n")
		for _, r := range results {
			fmt.Printf("| %s | %s | %s |\n", r.Control, r.Status, r.Detail)
		}
		fmt.Printf("\nReady: %v\n", allReady(results))
	}

	if !allReady(results) {
		// Fail-closed: NOT_READY or UNKNOWN both block.
		os.Exit(1)
	}
}

func allReady(rs []Result) bool {
	for _, r := range rs {
		if r.Status != Ready {
			return false
		}
	}
	return true
}

// Evaluate runs every control check. It is pure with respect to the injected
// Fetcher so tests can drive it with canned API responses.
func Evaluate(f Fetcher, repo string, haveToken bool) []Result {
	if !haveToken {
		// No token means nothing about the private control plane is readable.
		controls := []string{
			"visibility", "branch-protection:main", "rulesets",
			"release-environment", "vulnerability-alerts", "secret-scanning",
		}
		out := make([]Result, 0, len(controls))
		for _, c := range controls {
			out = append(out, Result{c, Unknown, "no GITHUB_TOKEN; control unreadable"})
		}
		return out
	}
	return []Result{
		checkVisibility(f, repo),
		checkBranchProtection(f, repo),
		checkRulesets(f, repo),
		checkReleaseEnvironment(f, repo),
		checkVulnerabilityAlerts(f, repo),
		checkSecretScanning(f, repo),
	}
}

// unreadable reports the shared UNKNOWN mapping for auth/permission failures.
// It returns ok=false when the caller should keep interpreting the body.
func unreadable(code int, err error) (Status, string, bool) {
	if err != nil {
		return Unknown, "request error: " + err.Error(), false
	}
	switch code {
	case 401, 403:
		return Unknown, fmt.Sprintf("unreadable (HTTP %d); needs an admin token on a plan that exposes this control", code), false
	}
	return "", "", true
}

func checkVisibility(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo)
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"visibility", s, d}
	}
	if code == 404 {
		return Result{"visibility", Unknown, "repository not found (HTTP 404)"}
	}
	if code/100 != 2 {
		return Result{"visibility", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
	}
	var v struct {
		Visibility string `json:"visibility"`
		Private    bool   `json:"private"`
	}
	_ = json.Unmarshal(body, &v)
	if v.Visibility == "public" || (!v.Private && v.Visibility == "") {
		return Result{"visibility", Ready, "public"}
	}
	return Result{"visibility", NotReady, "still private; not yet transitioned"}
}

func checkBranchProtection(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo + "/branches/main/protection")
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"branch-protection:main", s, d}
	}
	if code == 404 {
		return Result{"branch-protection:main", NotReady, "main has no branch protection configured"}
	}
	if code/100 != 2 {
		return Result{"branch-protection:main", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
	}
	var p struct {
		RequiredStatusChecks struct {
			Contexts []string `json:"contexts"`
			Checks   []struct {
				Context string `json:"context"`
			} `json:"checks"`
		} `json:"required_status_checks"`
		RequiredPullRequestReviews *struct{} `json:"required_pull_request_reviews"`
	}
	_ = json.Unmarshal(body, &p)
	if p.RequiredPullRequestReviews == nil {
		return Result{"branch-protection:main", NotReady, "no required pull-request reviews"}
	}
	// GitHub reports each required check in both lists.
	var contexts []string
	seen := map[string]bool{}
	for _, context := range p.RequiredStatusChecks.Contexts {
		if context != "" && !seen[context] {
			seen[context] = true
			contexts = append(contexts, context)
		}
	}
	for _, check := range p.RequiredStatusChecks.Checks {
		if check.Context != "" && !seen[check.Context] {
			seen[check.Context] = true
			contexts = append(contexts, check.Context)
		}
	}
	if len(contexts) == 0 {
		return Result{"branch-protection:main", NotReady, "no required status-check contexts (release-candidate gate not required)"}
	}
	if !hasReleaseCandidateContext(contexts) {
		return Result{"branch-protection:main", NotReady, "required contexts do not include the release-candidate gate; unrelated contexts are insufficient"}
	}
	return Result{"branch-protection:main", Ready, fmt.Sprintf("reviews required; complete release-candidate gate present among %d required contexts", len(contexts))}
}

func hasReleaseCandidateContext(contexts []string) bool {
	for _, context := range contexts {
		if context == releaseCandidateAggregateContext {
			return true
		}
	}
	return false
}

func checkRulesets(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo + "/rulesets")
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"rulesets", s, d}
	}
	if code/100 != 2 {
		return Result{"rulesets", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
	}
	// The list endpoint returns summaries without `conditions`; the ref
	// patterns are only in each ruleset's detail.
	var summaries []struct {
		ID          int64  `json:"id"`
		Enforcement string `json:"enforcement"`
		Target      string `json:"target"`
	}
	_ = json.Unmarshal(body, &summaries)
	activeTagV := 0
	// A ruleset whose exclusions match release tags does not protect them.
	var excluded, undecided []string
	for _, summary := range summaries {
		if summary.Enforcement != "active" || !rulesetTargetMayCoverTags(summary.Target) {
			continue
		}
		code, body, err := f.Get(fmt.Sprintf("/repos/%s/rulesets/%d", repo, summary.ID))
		if s, d, cont := unreadable(code, err); !cont {
			return Result{"rulesets", s, d}
		}
		if code/100 != 2 {
			return Result{"rulesets", Unknown, fmt.Sprintf("unexpected HTTP %d reading ruleset %d", code, summary.ID)}
		}
		var detail struct {
			Enforcement string `json:"enforcement"`
			Target      string `json:"target"`
			Conditions  struct {
				RefName struct {
					Include []string `json:"include"`
					Exclude []string `json:"exclude"`
				} `json:"ref_name"`
			} `json:"conditions"`
		}
		if err := json.Unmarshal(body, &detail); err != nil {
			return Result{"rulesets", Unknown, fmt.Sprintf("could not parse ruleset %d: %v", summary.ID, err)}
		}
		if detail.Enforcement != "active" || !rulesetTargetsReleaseTags(detail.Target, detail.Conditions.RefName.Include) {
			continue
		}
		var matched, unclear []string
		for _, exclude := range detail.Conditions.RefName.Exclude {
			switch match, decided := excludeMayMatchReleaseTags(exclude); {
			case match:
				matched = append(matched, fmt.Sprintf("ruleset %d excludes %q", summary.ID, exclude))
			case !decided:
				unclear = append(unclear, fmt.Sprintf("ruleset %d excludes %q", summary.ID, exclude))
			}
		}
		switch {
		case len(matched) > 0:
			excluded = append(excluded, matched...)
		case len(unclear) > 0:
			undecided = append(undecided, unclear...)
		default:
			activeTagV++
		}
	}
	if activeTagV > 0 {
		return Result{"rulesets", Ready, fmt.Sprintf("%d active release-tag ruleset(s)", activeTagV)}
	}
	// A ruleset with an exclusion this tool cannot evaluate may still cover
	// every release tag.
	if len(undecided) > 0 {
		return Result{"rulesets", Unknown, "cannot tell whether these exclusions match release tags: " + strings.Join(undecided, "; ")}
	}
	if len(excluded) > 0 {
		return Result{"rulesets", NotReady, "release tags are excluded: " + strings.Join(excluded, "; ")}
	}
	return Result{"rulesets", NotReady, "no active ruleset targets refs/tags/v*; unrelated rulesets are insufficient"}
}

func rulesetTargetMayCoverTags(target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	return target == "" || target == "tag"
}

func rulesetTargetsReleaseTags(target string, includes []string) bool {
	if !rulesetTargetMayCoverTags(target) {
		return false
	}
	for _, include := range includes {
		include = strings.TrimSpace(include)
		if include == "refs/tags/v*" || include == "refs/tags/v[0-9]*" || include == "v*" {
			return true
		}
	}
	return false
}

// releaseTagPrefix starts the full ref of every release tag.
const releaseTagPrefix = "refs/tags/v"

// releaseTagWitnesses are release refs; an exclusion that matches one provably
// excludes a release tag.
var releaseTagWitnesses = []string{"refs/tags/v0.1.0", "refs/tags/v1.2.3-rc.1", "refs/tags/v10.20.30"}

// excludeMayMatchReleaseTags reports whether a ruleset exclude pattern can
// match a refs/tags/v* release tag. GitHub matches fnmatch patterns against the
// full ref; a pattern without a slash is read as a tag name instead, as the
// include check reads v*, since as a full ref it could match no ref. A pattern
// matches when it is a literal release ref, or when witnessTrusted accepts it
// and it matches a witness. It does not match only when it provably cannot
// reach refs/tags/v: it is a literal other ref, or its literal start rules out
// refs/tags/v. decided is false for everything else: an unknown ~ token, a
// pattern that may match release tags no witness stands for, such as
// refs/tags/v9*, a pattern outside the witnessTrusted grammar, such as
// refs/[!t-a]ags/v*, whose witness matches prove nothing, or a slash pattern
// without a literal refs/ start, whose refs part may be a wildcard or class.
func excludeMayMatchReleaseTags(pattern string) (match, decided bool) {
	pattern = strings.TrimSpace(pattern)
	switch {
	case pattern == "":
		return false, true
	case pattern == "~ALL":
		return true, true
	case pattern == "~DEFAULT_BRANCH":
		return false, true
	case strings.HasPrefix(pattern, "~"):
		return false, false
	}
	if !strings.Contains(pattern, "/") {
		pattern = "refs/tags/" + pattern
	}
	if witnessTrusted(pattern) {
		for _, ref := range releaseTagWitnesses {
			if fnmatchPathname(pattern, ref) {
				return true, true
			}
		}
	}
	if !strings.HasPrefix(pattern, "refs/") {
		return false, false
	}
	special := strings.IndexAny(pattern, `*?[{\`)
	if special < 0 {
		// A literal names one ref.
		return strings.HasPrefix(pattern, releaseTagPrefix), true
	}
	// Every character before the first special one must match itself, so
	// unless that start and refs/tags/v agree, the pattern cannot reach a
	// release tag.
	start := pattern[:special]
	if strings.HasPrefix(releaseTagPrefix, start) || strings.HasPrefix(start, releaseTagPrefix) {
		return false, false
	}
	return false, true
}

// fnmatchPathname matches ref against pattern segment by segment with
// path.Match, so nothing matches a slash, and a "**" segment before another
// spans any number of segments. For a pattern witnessTrusted accepts it agrees
// with Ruby's File.fnmatch(pattern, ref, File::FNM_PATHNAME), which GitHub
// uses for ruleset patterns, on the release witnesses. Outside that grammar
// the two can differ either way, as on the descending range t-a, which
// path.Match reads as empty and File.fnmatch as t and a, so a result there
// proves nothing.
func fnmatchPathname(pattern, ref string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(ref, "/"))
}

func matchSegments(pattern, ref []string) bool {
	if len(pattern) == 0 {
		return len(ref) == 0
	}
	if pattern[0] == "**" && len(pattern) > 1 {
		for skip := 0; skip <= len(ref); skip++ {
			if matchSegments(pattern[1:], ref[skip:]) {
				return true
			}
		}
		return false
	}
	if len(ref) == 0 {
		return false
	}
	matched, err := path.Match(negateWithCaret(pattern[0]), ref[0])
	return err == nil && matched && matchSegments(pattern[1:], ref[1:])
}

// negateWithCaret rewrites the "[!" that opens a class, which fnmatch negates,
// as "[^", the negation path.Match reads. It skips escaped bytes and keeps a
// class open until its closing "]", so a "[!" inside an open class is left
// alone. A "]" right after the opener is kept as a member, as fnmatch reads
// it; path.Match rejects such a class, so it matches nothing either way, and
// witnessTrusted rejects that shape.
func negateWithCaret(segment string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(segment); i++ {
		b.WriteByte(segment[i])
		switch {
		case segment[i] == '\\' && i+1 < len(segment):
			i++
			b.WriteByte(segment[i])
		case inClass:
			inClass = segment[i] != ']'
		case segment[i] == '[':
			inClass = true
			if i+1 < len(segment) && (segment[i+1] == '!' || segment[i+1] == '^') {
				i++
				b.WriteByte('^')
			}
			// fnmatch reads a "]" first in a class as a member; path.Match
			// rejects it.
			if i+1 < len(segment) && segment[i+1] == ']' {
				i++
				b.WriteByte(']')
			}
		}
	}
	return b.String()
}

// witnessTrusted reports whether pattern lies inside the narrow grammar in
// which fnmatchPathname and File.fnmatch with FNM_PATHNAME agree on the
// release witnesses, so that a witness match proves a release tag excluded.
// The pattern is printable ASCII with no backslash, brace or "]" outside a
// class, where "*", "?" and "**" may appear. Every "[" opens a class of an
// optional "!" or "^", one or more members and a closing "]"; a member is a
// letter, digit, "." or "_", or a range whose endpoints are both digits, both
// lowercase or both uppercase letters, the start not above the end. Anything
// else, such as an unterminated or empty class or a "[", "]", "-" or "/" in a
// class, is outside it, and excludeMayMatchReleaseTags leaves a pattern
// outside it undecided unless its literal start rules out refs/tags/v.
func witnessTrusted(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '[' {
			n := trustedClassLen(pattern[i:])
			if n == 0 {
				return false
			}
			i += n - 1
			continue
		}
		if c < ' ' || c > '~' || strings.IndexByte(`\]{}`, c) >= 0 {
			return false
		}
	}
	return true
}

// trustedClassLen returns the length of the class that s opens if the class
// is in the witnessTrusted grammar, and 0 otherwise.
func trustedClassLen(s string) int {
	i := 1
	if i < len(s) && (s[i] == '!' || s[i] == '^') {
		i++
	}
	members := 0
	for i < len(s) && s[i] != ']' {
		lo := s[i]
		switch {
		case i+2 < len(s) && s[i+1] == '-':
			hi := s[i+2]
			if rangeKind(lo) == 0 || rangeKind(lo) != rangeKind(hi) || lo > hi {
				return 0
			}
			i += 3
		case rangeKind(lo) != 0 || lo == '.' || lo == '_':
			i++
		default:
			return 0
		}
		members++
	}
	if members == 0 || i == len(s) {
		return 0
	}
	return i + 1
}

// rangeKind tells digits, lowercase letters and uppercase letters apart, the
// kinds whose ranges witnessTrusted accepts, and is 0 for any other byte.
func rangeKind(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return 1
	case 'a' <= c && c <= 'z':
		return 2
	case 'A' <= c && c <= 'Z':
		return 3
	}
	return 0
}

func checkReleaseEnvironment(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo + "/environments")
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"release-environment", s, d}
	}
	if code/100 != 2 {
		return Result{"release-environment", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
	}
	var envs struct {
		Environments []struct {
			Name string `json:"name"`
		} `json:"environments"`
	}
	_ = json.Unmarshal(body, &envs)
	for _, e := range envs.Environments {
		if e.Name == "release" {
			return checkReleaseEnvironmentDetail(f, repo)
		}
	}
	return Result{"release-environment", NotReady, "no protected `release` environment"}
}

func checkReleaseEnvironmentDetail(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo + "/environments/release")
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"release-environment", s, d}
	}
	if code == 404 {
		return Result{"release-environment", NotReady, "release environment exists in list but detail readback returned 404"}
	}
	if code/100 != 2 {
		return Result{"release-environment", Unknown, fmt.Sprintf("unexpected HTTP %d reading release environment detail", code)}
	}
	var env struct {
		ProtectionRules []struct {
			Type      string `json:"type"`
			Reviewers []any  `json:"reviewers"`
		} `json:"protection_rules"`
		DeploymentBranchPolicy *struct {
			ProtectedBranches    bool `json:"protected_branches"`
			CustomBranchPolicies bool `json:"custom_branch_policies"`
		} `json:"deployment_branch_policy"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return Result{"release-environment", Unknown, "could not parse release environment detail: " + err.Error()}
	}
	hasReviewerProtection := false
	for _, rule := range env.ProtectionRules {
		if rule.Type == "required_reviewers" && len(rule.Reviewers) > 0 {
			hasReviewerProtection = true
			break
		}
	}
	if !hasReviewerProtection {
		return Result{"release-environment", NotReady, "release environment has no required reviewer protection"}
	}
	if env.DeploymentBranchPolicy == nil || (!env.DeploymentBranchPolicy.ProtectedBranches && !env.DeploymentBranchPolicy.CustomBranchPolicies) {
		return Result{"release-environment", NotReady, "release environment has no deployment branch/tag policy"}
	}
	return Result{"release-environment", Ready, "required reviewers and deployment branch/tag policy read back"}
}

func checkVulnerabilityAlerts(f Fetcher, repo string) Result {
	code, _, err := f.Get("/repos/" + repo + "/vulnerability-alerts")
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"vulnerability-alerts", s, d}
	}
	switch code {
	case 204:
		return Result{"vulnerability-alerts", Ready, "enabled"}
	case 404:
		return Result{"vulnerability-alerts", NotReady, "disabled"}
	}
	return Result{"vulnerability-alerts", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
}

func checkSecretScanning(f Fetcher, repo string) Result {
	code, body, err := f.Get("/repos/" + repo)
	if s, d, cont := unreadable(code, err); !cont {
		return Result{"secret-scanning", s, d}
	}
	if code/100 != 2 {
		return Result{"secret-scanning", Unknown, fmt.Sprintf("unexpected HTTP %d", code)}
	}
	var r struct {
		SecurityAndAnalysis *struct {
			SecretScanning               *struct{ Status string } `json:"secret_scanning"`
			SecretScanningPushProtection *struct{ Status string } `json:"secret_scanning_push_protection"`
		} `json:"security_and_analysis"`
	}
	_ = json.Unmarshal(body, &r)
	if r.SecurityAndAnalysis == nil || r.SecurityAndAnalysis.SecretScanning == nil {
		return Result{"secret-scanning", Unknown, "security_and_analysis not reported (needs admin token / supported plan)"}
	}
	if r.SecurityAndAnalysis.SecretScanning.Status == "enabled" {
		return Result{"secret-scanning", Ready, "enabled"}
	}
	return Result{"secret-scanning", NotReady, "disabled"}
}

// httpFetcher is the real, read-only GitHub API client.
type httpFetcher struct {
	token string
	base  string
}

func (h *httpFetcher) Get(path string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, h.base+path, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, body, nil
}
