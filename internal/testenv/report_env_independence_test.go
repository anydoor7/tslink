package testenv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// reportEnvIndependenceChildEnv marks the re-executed copy of this test binary
// so the parent below does not recurse into itself.
const reportEnvIndependenceChildEnv = "TSLINK_TESTENV_REPORT_ENV_INDEPENDENCE_CHILD"

var failedTestLine = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)

// TestNoTestInThisPackageDependsOnTheReportEnvValue asserts the property that a
// single fixed test cannot: that *no* test in this package changes colour when
// TSLINK_SERVICE_MANAGER_GUARD_REPORT flips.
//
// Why the property needs its own test. This package owns the only code that
// reads that env, and several tests here assert on the guard's stderr, so
// "assert on output that the env changes, forget to pin the env" is a mistake
// this package will keep being able to make. It already made it once: the
// control group below required complete silence, which is only true with the
// env unset, so `go test ./...` was green and
// `TSLINK_SERVICE_MANAGER_GUARD_REPORT=1 go test ./...` was red. Whoever
// introduced that ran the suite and saw green, because they ran it the way
// they always run it.
//
// That env is not a debugging convenience. An external runner may set
// it on purpose: without the zero-hit summary line there is no way to tell
// "the guard was installed and blocked nothing" from "the guard was never
// installed", and the second one is how `go test` took a developer's own
// installed daemon down on 2026-09-16. A suite that is only green with the env unset depends on the caller's
// environment.
//
// How it works: re-exec this same test binary twice, once with the env removed
// and once with it set, and require the two runs to agree on exit code and on
// the set of failing tests. Re-executing the built binary rather than shelling
// out to `go test` keeps it to roughly one extra package-run per direction and
// keeps it honest -- it is the very binary the operator is running.
//
// What it deliberately does not do: compare stderr. The guard's reports embed
// debug.Stack(), so the two runs differ byte-for-byte for legitimate reasons.
// The claim under test is about results, so results are what it compares.
func TestNoTestInThisPackageDependsOnTheReportEnvValue(t *testing.T) {
	if os.Getenv(reportEnvIndependenceChildEnv) == "1" {
		t.Skip("child re-exec; the parent is comparing this binary's outcome under both values of " + ServiceManagerGuardReportEnv)
	}
	if testing.Short() {
		t.Skip("re-executes this test binary twice")
	}

	type outcome struct {
		code   int
		failed []string
		log    string
	}
	runs := map[string]outcome{}

	for _, value := range []string{"", "1"} {
		label := ServiceManagerGuardReportEnv + " unset"
		if value != "" {
			label = ServiceManagerGuardReportEnv + "=" + value
		}
		code, log := runSelfWithReportEnv(t, value)
		failed := []string{}
		for _, match := range failedTestLine.FindAllStringSubmatch(log, -1) {
			failed = append(failed, match[1])
		}
		sort.Strings(failed)
		runs[label] = outcome{code: code, failed: failed, log: log}
	}

	off, on := runs[ServiceManagerGuardReportEnv+" unset"], runs[ServiceManagerGuardReportEnv+"=1"]

	// Control: both children have to have actually run the package. Without
	// this, two children that died before reaching any test would agree
	// perfectly and this test would be green while proving nothing.
	for label, got := range runs {
		if !strings.Contains(got.log, "--- PASS: ") {
			t.Fatalf("child with %s ran no passing test, so the comparison below is vacuous:\n%s", label, got.log)
		}
	}

	if off.code != on.code {
		t.Fatalf("this package's exit code depends on %s: unset -> %d, =1 -> %d.\n"+
			"A test that is only green in one environment is unreliable for callers that set this variable.\n"+
			"--- with the env unset ---\n%s\n--- with the env set ---\n%s",
			ServiceManagerGuardReportEnv, off.code, on.code, off.log, on.log)
	}
	if strings.Join(off.failed, ",") != strings.Join(on.failed, ",") {
		t.Fatalf("which tests fail depends on %s: unset -> %v, =1 -> %v.\n"+
			"Pin the env in the offending test (t.Setenv) or assert on something the env does not change.",
			ServiceManagerGuardReportEnv, off.failed, on.failed)
	}
}

// runSelfWithReportEnv re-runs this test binary with the report env forced to
// value, where the empty string means removed from the environment entirely
// rather than set to "". Those are the same thing to os.Getenv today, but they
// are not to os.LookupEnv, and "unset" is the state an operator's bare shell is
// actually in.
func runSelfWithReportEnv(t *testing.T, value string) (int, string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	child := exec.CommandContext(ctx, os.Args[0], "-test.run", ".", "-test.v", "-test.count=1")

	env := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, ServiceManagerGuardReportEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, reportEnvIndependenceChildEnv+"=1")
	if value != "" {
		env = append(env, ServiceManagerGuardReportEnv+"="+value)
	}
	child.Env = env

	var combined bytes.Buffer
	child.Stdout = &combined
	child.Stderr = &combined

	err := child.Run()
	if ctx.Err() != nil {
		t.Fatalf("child with %s=%q timed out:\n%s", ServiceManagerGuardReportEnv, value, combined.String())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, combined.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), combined.String()
	default:
		t.Fatalf("child with %s=%q: %v\n%s", ServiceManagerGuardReportEnv, value, err, combined.String())
		return -1, ""
	}
}
