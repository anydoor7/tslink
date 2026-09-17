package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func plantForTest(t *testing.T) (dir, logPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no service manager fake is planted on Windows; see PlantServiceManagerShims")
	}
	dir = t.TempDir()
	logPath, planted, err := PlantServiceManagerShims(dir)
	if err != nil {
		t.Fatalf("plant shims: %v", err)
	}
	// The planted list is what the teardown report uses to tell "no child
	// called a manager" apart from "no fake existed to call". A planter that
	// reported fewer names than it wrote would make that distinction lie.
	if len(planted) != len(serviceManagerShimBinaries) {
		t.Fatalf("planted = %v, want one entry per %v", planted, serviceManagerShimBinaries)
	}
	for _, binary := range planted {
		if _, err := os.Stat(filepath.Join(dir, binary)); err != nil {
			t.Fatalf("planted names %s but it is not on disk: %v", binary, err)
		}
	}
	return dir, logPath
}

// TestPlantedShimRefusesAndRecords is the control for every other assertion in
// this file: one call, executed directly, produces one parseable record and no
// side effect.
func TestPlantedShimRefusesAndRecords(t *testing.T) {
	dir, logPath := plantForTest(t)

	for _, binary := range serviceManagerShimBinaries {
		t.Run(binary, func(t *testing.T) {
			out, err := exec.Command(filepath.Join(dir, binary), "bootout", "gui/0/com.example.absent").CombinedOutput()
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run planted %s: err = %v, want a non-zero exit", binary, err)
			}
			if exitErr.ExitCode() != ServiceManagerShimExitCode {
				t.Fatalf("exit = %d, want %d", exitErr.ExitCode(), ServiceManagerShimExitCode)
			}
			if !strings.Contains(string(out), ServiceManagerShimStderrPrefix) {
				t.Fatalf("output = %q, want the shim marker", out)
			}
		})
	}

	calls, err := ReadServiceManagerShimCalls(logPath)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}
	if len(calls) != len(serviceManagerShimBinaries) {
		t.Fatalf("recorded %d calls, want %d: %v", len(calls), len(serviceManagerShimBinaries), calls)
	}
	for i, call := range calls {
		if call.Manager != serviceManagerShimBinaries[i] {
			t.Fatalf("call %d manager = %q, want %q", i, call.Manager, serviceManagerShimBinaries[i])
		}
		if strings.Join(call.Args, " ") != "bootout gui/0/com.example.absent" {
			t.Fatalf("call %d args = %q, want the argv as typed", i, call.Args)
		}
	}
}

// TestPlantedShimRecordsOneLinePerConcurrentCall pins the property the first
// full-suite run under this shim broke: the cmd package spawns its children
// concurrently, and a record assembled by several printf calls into one open
// append interleaves between processes. The observed corruption was
// `launchctl printlaunchctl gui/501/com.tslink.daemon`, which the verb
// classifier then read as a state change and failed the package on.
//
// A parse that survives is not enough to assert here: two interleaved records
// still parse. The assertion is the exact multiset of argv, which a spliced
// line cannot satisfy.
func TestPlantedShimRecordsOneLinePerConcurrentCall(t *testing.T) {
	dir, logPath := plantForTest(t)
	const concurrent = 48

	binary := filepath.Join(dir, serviceManagerShimBinaries[0])
	var wait sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wait.Add(1)
		go func(n int) {
			defer wait.Done()
			_ = exec.Command(binary, "print", "gui/0/com.example.probe"+strconv.Itoa(n)).Run()
		}(i)
	}
	wait.Wait()

	calls, err := ReadServiceManagerShimCalls(logPath)
	if err != nil {
		t.Fatalf("read shim log: %v", err)
	}
	if len(calls) != concurrent {
		t.Fatalf("recorded %d lines for %d concurrent calls; fields from different calls were spliced together", len(calls), concurrent)
	}
	seen := map[string]bool{}
	for _, call := range calls {
		if call.Manager != serviceManagerShimBinaries[0] {
			t.Fatalf("recorded manager = %q, want %q; this line is two calls spliced together: %s", call.Manager, serviceManagerShimBinaries[0], call)
		}
		if len(call.Args) != 2 || call.Args[0] != "print" {
			t.Fatalf("recorded argv = %q, want exactly [print <target>]", call.Args)
		}
		if seen[call.Args[1]] {
			t.Fatalf("target %q recorded twice", call.Args[1])
		}
		seen[call.Args[1]] = true
	}
	for i := 0; i < concurrent; i++ {
		if target := "gui/0/com.example.probe" + strconv.Itoa(i); !seen[target] {
			t.Fatalf("call %d never reached the log: %q missing", i, target)
		}
	}
}

// TestShimCallVerbClassificationIsAnAllowlist pins the direction the teardown
// policy fails in. Anything the allowlist does not name is treated as a state
// change, including an empty argv and a verb this repo has never seen.
func TestShimCallVerbClassificationIsAnAllowlist(t *testing.T) {
	cases := []struct {
		args     []string
		wantVerb string
		readOnly bool
	}{
		{args: []string{"print", "gui/0/x"}, wantVerb: "print", readOnly: true},
		{args: []string{"--user", "show", "tslink.service"}, wantVerb: "show", readOnly: true},
		{args: []string{"show-user", "someone", "--property=Linger"}, wantVerb: "show-user", readOnly: true},
		{args: []string{"bootout", "gui/0/x"}, wantVerb: "bootout", readOnly: false},
		{args: []string{"bootstrap", "gui/0", "/tmp/x.plist"}, wantVerb: "bootstrap", readOnly: false},
		{args: []string{"--user", "daemon-reload"}, wantVerb: "daemon-reload", readOnly: false},
		{args: []string{"enable-linger", "someone"}, wantVerb: "enable-linger", readOnly: false},
		// No verb at all, and a verb nobody has classified: both must land on
		// the fail-closed side rather than slipping through as "not a write".
		{args: nil, wantVerb: "", readOnly: false},
		{args: []string{"--user"}, wantVerb: "", readOnly: false},
		{args: []string{"some-future-verb"}, wantVerb: "some-future-verb", readOnly: false},
	}
	for _, tc := range cases {
		call := ServiceManagerShimCall{Manager: "launchctl", Args: tc.args}
		if got := call.Verb(); got != tc.wantVerb {
			t.Errorf("Verb(%q) = %q, want %q", tc.args, got, tc.wantVerb)
		}
		if got := call.IsReadOnly(); got != tc.readOnly {
			t.Errorf("IsReadOnly(%q) = %v, want %v", tc.args, got, tc.readOnly)
		}
	}
}

// TestReadServiceManagerShimCallsDistinguishesEmptyFromAbsent is why
// PlantServiceManagerShims creates the log up front. A missing file and a log
// with no calls both mean "no calls recorded" to a caller that ignores the
// error, and one of them means the shim was never planted.
func TestReadServiceManagerShimCallsDistinguishesEmptyFromAbsent(t *testing.T) {
	_, logPath := plantForTest(t)

	calls, err := ReadServiceManagerShimCalls(logPath)
	if err != nil || len(calls) != 0 {
		t.Fatalf("freshly planted log: calls=%v err=%v, want no calls and no error", calls, err)
	}
	if _, err := ReadServiceManagerShimCalls(filepath.Join(t.TempDir(), "never-planted.log")); err == nil {
		t.Fatal("reading an absent log returned no error; an unplanted shim would read as a clean run")
	}
}

// TestInstallServiceManagerPathShimPutsItselfFirstAndRestores covers the
// installation half in this package, where no TestMain installs one.
func TestInstallServiceManagerPathShimPutsItselfFirstAndRestores(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no service manager fake is planted on Windows")
	}
	pathBefore := os.Getenv("PATH")

	shim, restore, err := InstallServiceManagerPathShim()
	if err != nil {
		t.Fatalf("install shim: %v", err)
	}
	entries := filepath.SplitList(os.Getenv("PATH"))
	if len(entries) == 0 || entries[0] != shim.Dir {
		t.Fatalf("PATH[0] = %v, want %q", entries, shim.Dir)
	}
	resolved, err := exec.LookPath(serviceManagerShimBinaries[0])
	if err != nil || filepath.Dir(resolved) != shim.Dir {
		t.Fatalf("LookPath(%s) = %q (err=%v), want a file in %q", serviceManagerShimBinaries[0], resolved, err, shim.Dir)
	}
	if got := os.Getenv(ServiceManagerShimLogEnv); got != shim.LogPath {
		t.Fatalf("%s = %q, want %q", ServiceManagerShimLogEnv, got, shim.LogPath)
	}

	restore()
	if got := os.Getenv("PATH"); got != pathBefore {
		t.Fatalf("PATH after restore = %q, want %q", got, pathBefore)
	}
	if _, err := os.Stat(shim.Dir); !os.IsNotExist(err) {
		t.Fatalf("shim directory survived restore (stat err = %v)", err)
	}
}
