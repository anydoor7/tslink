package testenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// isolationProbeEnv selects the child branch of the tests below. It is
// deliberately not a TSLINK_ variable: a top-level child scrubs those before
// any test runs, and the probe has to survive that scrub to report on it.
const isolationProbeEnv = "TESTENV_ISOLATION_PROBE"

const isolationReportPrefix = "testenv-isolation-report: "

// isolationReport is what a probe child sees after its own TestMain ran.
type isolationReport struct {
	Root       string            `json:"root"`
	UserHome   string            `json:"user_home"`
	UserConfig string            `json:"user_config"`
	UserCache  string            `json:"user_cache"`
	Env        map[string]string `json:"env"`
}

func writeIsolationReport(t *testing.T) {
	t.Helper()
	report := isolationReport{Root: Root(), Env: map[string]string{}}
	var err error
	if report.UserHome, err = os.UserHomeDir(); err != nil {
		t.Fatal(err)
	}
	if report.UserConfig, err = os.UserConfigDir(); err != nil {
		t.Fatal(err)
	}
	if report.UserCache, err = os.UserCacheDir(); err != nil {
		t.Fatal(err)
	}
	for _, kv := range HomeEnv("unused") {
		report.Env[kv[0]] = os.Getenv(kv[0])
	}
	for _, name := range goToolchainLocationEnvs {
		report.Env[name] = os.Getenv(name)
	}
	for _, name := range tslinkEnvNames() {
		report.Env[name] = os.Getenv(name)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(os.Stdout, isolationReportPrefix+string(encoded))
}

// runIsolationProbe re-runs one test of this binary as a child and returns its
// exit code, combined output and, when it printed one, its report.
func runIsolationProbe(t *testing.T, testName string, env []string, dir string) (int, string, *isolationReport) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.v", "-test.count=1")
	cmd.Env = env
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run probe child: %v\n%s", err, out)
		}
		code = exitErr.ExitCode()
	}
	for _, line := range strings.Split(string(out), "\n") {
		if encoded, ok := strings.CutPrefix(strings.TrimSpace(line), isolationReportPrefix); ok {
			var report isolationReport
			if err := json.Unmarshal([]byte(encoded), &report); err != nil {
				t.Fatalf("decode probe report %q: %v", encoded, err)
			}
			return code, string(out), &report
		}
	}
	return code, string(out), nil
}

// environWithout copies this process's environment minus every variable in
// names and every TSLINK_ variable.
func environWithout(names ...string) []string {
	drop := map[string]bool{}
	for _, name := range names {
		drop[name] = true
	}
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if drop[name] || strings.HasPrefix(name, "TSLINK_") {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func isUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// privateRootParent keeps a child's roots out of unrelated binaries' startup
// sweeps. Tests that inspect a dead child's root must own its entire lifecycle:
// a global sweep could otherwise remove it before an existence assertion, or
// make a broken cleanup look successful. Children still use the real Main.
func privateRootParent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Unix uses TMPDIR. Windows uses TMP/TEMP, or SystemTemp when
	// GetTempPath2 selects the SYSTEM account's temporary directory.
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "SystemTemp"} {
		t.Setenv(name, dir)
	}
	return dir
}

// TestMainScrubsTheContributorEnvironmentAndMovesEveryHomeLocation starts this
// binary the way `go test` does on a contributor's machine (no marked root),
// with fake credentials, knobs and a fake home exported, and checks what the
// test bodies then see.
func TestMainScrubsTheContributorEnvironmentAndMovesEveryHomeLocation(t *testing.T) {
	if os.Getenv(isolationProbeEnv) == "top-level" {
		writeIsolationReport(t)
		return
	}
	parent := privateRootParent(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go command on PATH, so the toolchain locations this test compares cannot be resolved: %v", err)
	}

	contributor := t.TempDir()
	var names []string
	for _, kv := range HomeEnv(contributor) {
		names = append(names, kv[0])
	}
	env := environWithout(append(names, goToolchainLocationEnvs...)...)
	for _, kv := range HomeEnv(contributor) {
		env = append(env, kv[0]+"="+kv[1])
	}
	env = append(env,
		"TSLINK_API_KEY=tskey-api-<test-only-CANARY0827-notasecret>",
		"TSLINK_CLIENT_SECRET=tskey-client-CANARY0827-notasecret",
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_BASE_URL=https://canary0827.invalid",
		// A root variable without Main's marker is what a contributor could
		// export by accident; it must not switch the scrub off.
		RootEnv+"="+contributor,
		ServiceManagerGuardReportEnv+"=1",
	)
	// Run outside the module so neither go env nor the child can pick up a
	// toolchain switch from go.mod.
	workDir := t.TempDir()

	// The oracle for the toolchain pins: what the go command resolves in the
	// contributor's environment, computed independently of Main.
	resolve := exec.Command(goBin, "env", "-json", "GOCACHE", "GOMODCACHE", "GOPATH", "GOENV")
	resolve.Env = env
	resolve.Dir = workDir
	resolved, err := resolve.Output()
	if err != nil {
		t.Fatalf("go env in the contributor environment: %v", err)
	}
	want := map[string]string{}
	if err := json.Unmarshal(resolved, &want); err != nil {
		t.Fatal(err)
	}

	code, out, report := runIsolationProbe(t, t.Name(), append(env, isolationProbeEnv+"=top-level"), workDir)
	if code != 0 || report == nil {
		t.Fatalf("top-level probe child: exit %d, report %v\n%s", code, report, out)
	}
	if filepath.Dir(report.Root) != parent || !strings.HasPrefix(filepath.Base(report.Root), RootPrefix) {
		t.Fatalf("child root = %q, want a fresh %s* directory under %q", report.Root, RootPrefix, parent)
	}
	if _, err := os.Stat(report.Root); !os.IsNotExist(err) {
		t.Fatalf("child root %s still exists after the child exited: %v", report.Root, err)
	}

	home := filepath.Join(report.Root, "home")
	wantTSLink := map[string]string{
		configDirEnv:                 ConfigDir(home),
		RootEnv:                      report.Root,
		DoctorSkipTailscaleSSHEnv:    "1",
		ServiceManagerGuardReportEnv: "1",
	}
	gotTSLink := map[string]string{}
	for name, value := range report.Env {
		if strings.HasPrefix(name, "TSLINK_") {
			gotTSLink[name] = value
		}
	}
	if strings.Join(sortedKeys(gotTSLink), ",") != strings.Join(sortedKeys(wantTSLink), ",") {
		t.Fatalf("TSLINK_ variables a test sees = %v, want exactly %v", sortedKeys(gotTSLink), sortedKeys(wantTSLink))
	}
	for name, value := range wantTSLink {
		if gotTSLink[name] != value {
			t.Fatalf("%s = %q, want %q", name, gotTSLink[name], value)
		}
	}
	for _, kv := range HomeEnv(home) {
		if report.Env[kv[0]] != kv[1] {
			t.Fatalf("%s = %q, want %q inside the isolation root", kv[0], report.Env[kv[0]], kv[1])
		}
	}
	for label, path := range map[string]string{"os.UserHomeDir": report.UserHome, "os.UserConfigDir": report.UserConfig, "os.UserCacheDir": report.UserCache} {
		if !isUnder(path, home) {
			t.Fatalf("%s = %q, want it under %q", label, path, home)
		}
	}
	for _, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"} {
		if want[name] == "" {
			continue
		}
		if report.Env[name] != want[name] {
			t.Fatalf("%s = %q, want the contributor's %q (moving HOME must not move the Go toolchain)", name, report.Env[name], want[name])
		}
		if isUnder(report.Env[name], report.Root) {
			t.Fatalf("%s = %q lies inside the isolation root", name, report.Env[name])
		}
	}
}

// TestMainKeepsTestChosenTSLinkEnvInAChildOfAnIsolatedBinary: a helper child
// that a test starts inherits the parent's marked root, so the TSLINK_
// variables it sees are the test's. Locations still move to its own root.
func TestMainKeepsTestChosenTSLinkEnvInAChildOfAnIsolatedBinary(t *testing.T) {
	if os.Getenv(isolationProbeEnv) == "child" {
		writeIsolationReport(t)
		return
	}
	parent := privateRootParent(t)
	parentRoot := Root()
	if parentRoot == "" {
		t.Fatal("this test binary is not running under Main")
	}
	inherited := t.TempDir()
	env := append(os.Environ(),
		isolationProbeEnv+"=child",
		"TSLINK_TEST_HELPER_CANARY=kept",
		configDirEnv+"="+inherited,
		"HOME="+inherited,
	)
	code, out, report := runIsolationProbe(t, t.Name(), env, "")
	if code != 0 || report == nil {
		t.Fatalf("child probe: exit %d, report %v\n%s", code, report, out)
	}
	if got := report.Env["TSLINK_TEST_HELPER_CANARY"]; got != "kept" {
		t.Fatalf("test-chosen TSLINK_TEST_HELPER_CANARY in the child = %q, want kept", got)
	}
	if filepath.Dir(report.Root) != parent || report.Root == parentRoot {
		t.Fatalf("child root = %q, want a fresh root under %q distinct from the parent's %q", report.Root, parent, parentRoot)
	}
	if _, err := os.Stat(report.Root); !os.IsNotExist(err) {
		t.Fatalf("child root %s still exists after the child exited: %v", report.Root, err)
	}
	home := filepath.Join(report.Root, "home")
	for _, kv := range HomeEnv(home) {
		if report.Env[kv[0]] != kv[1] {
			t.Fatalf("%s = %q, want %q inside the child's own root", kv[0], report.Env[kv[0]], kv[1])
		}
	}
	for _, name := range goToolchainLocationEnvs {
		if report.Env[name] != os.Getenv(name) {
			t.Fatalf("%s = %q in the child, want the parent's %q", name, report.Env[name], os.Getenv(name))
		}
	}
}

// TestMainFailsThePackageWhenAHostSeamDefaultRuns: the probe child's test
// passes, and the binary must still exit non-zero and name the seam.
func TestMainFailsThePackageWhenAHostSeamDefaultRuns(t *testing.T) {
	const seam = "testenv.exampleHostSeam"
	if os.Getenv(isolationProbeEnv) == "unfaked-seam" {
		err := UnfakedHostSeam(seam)
		if !errors.Is(err, ErrUnfakedHostSeam) || !strings.Contains(err.Error(), seam) {
			t.Fatalf("UnfakedHostSeam() = %v, want ErrUnfakedHostSeam naming %s", err, seam)
		}
		return
	}
	code, out, _ := runIsolationProbe(t, t.Name(), append(os.Environ(), isolationProbeEnv+"=unfaked-seam"), "")
	if !strings.Contains(out, "--- PASS: "+t.Name()) {
		t.Fatalf("probe child did not run its passing body, so its exit code proves nothing:\n%s", out)
	}
	if code != 1 {
		t.Fatalf("probe child exit = %d, want 1 after an unfaked host seam ran\n%s", code, out)
	}
	if !strings.Contains(out, "unfaked host seam 1: "+seam) || !strings.Contains(out, "TestMainFailsThePackageWhenAHostSeamDefaultRuns") {
		t.Fatalf("probe child output does not name the seam and the calling test:\n%s", out)
	}
}

func TestRealHostMainRefusesAnEmptyReason(t *testing.T) {
	ran := false
	if code := RealHostMain(nil, " ", func() int { ran = true; return 0 }); code != 2 || ran {
		t.Fatalf("RealHostMain with a blank reason = %d (ran %v), want 2 without running", code, ran)
	}
}

func TestSetHomeMovesEveryHomeLocation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	configDir := SetHome(t, home)
	if configDir != ConfigDir(home) {
		t.Fatalf("SetHome() = %q, want %q", configDir, ConfigDir(home))
	}
	for _, kv := range HomeEnv(home) {
		if got := os.Getenv(kv[0]); got != kv[1] {
			t.Fatalf("%s = %q, want %q", kv[0], got, kv[1])
		}
	}
	for label, lookup := range map[string]func() (string, error){
		"os.UserHomeDir": os.UserHomeDir, "os.UserConfigDir": os.UserConfigDir, "os.UserCacheDir": os.UserCacheDir,
	} {
		got, err := lookup()
		if err != nil || !isUnder(got, home) {
			t.Fatalf("%s = %q, %v; want it under %q", label, got, err, home)
		}
	}
}
