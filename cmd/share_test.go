package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

const stopLivenessHelperReady = "tslink-stop-liveness-helper-ready"

func TestMain(m *testing.M) {
	if os.Getenv("TSLINK_STOP_LIVENESS_HELPER") == "1" {
		fmt.Fprintln(os.Stdout, stopLivenessHelperReady)
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode := os.Getenv("TSLINK_SHARE_DAEMON_HELPER"); mode != "" {
		fmt.Fprintln(os.Stderr, "helper diagnostic")
		switch mode {
		case "login":
			fmt.Print(`{"type":"tslink.result","ok":true,"schema_version":1,"command":"serve","code":0,"data":{"status":"needs_login","auth_url":"https://login.tailscale.com/a/helper"}}`)
		case "ready":
			fmt.Print(`{"type":"tslink.result","ok":true,"schema_version":1,"command":"serve","code":0,"data":{"daemon":true,"pid":42}}`)
		default:
			fmt.Print(`not-json`)
		}
		os.Exit(0)
	}

	// Tests that reach config.Dir() without their own isolation must never
	// touch the operator's real ~/.config/tslink. Point the whole package at a
	// throwaway config dir; individual tests still override it with t.Setenv.
	if os.Getenv(config.ConfigDirEnv) == "" {
		if isolated, err := os.MkdirTemp("", "tslink-cmd-test-config-"); err == nil {
			os.Setenv(config.ConfigDirEnv, isolated)
			defer os.RemoveAll(isolated)
		}
	}

	code := testenv.RunWithNonLoopbackDialGuard(m.Run, "cmd")

	// Own the teardown of the package's single compiled-binary build root.
	// compiledTSLinkBinary creates it lazily and publishes the path here; see
	// the tslinkBinaryRoot comment in api_binary_contract_test.go for why the
	// root cannot be a t.TempDir() and why creation stays lazy. Before this,
	// the root was never removed at all and leaked ~47 MiB per test process.
	if tslinkBinaryRoot != "" {
		if err := os.RemoveAll(tslinkBinaryRoot); err != nil {
			fmt.Fprintf(os.Stderr, "warning: remove compiled binary root %s: %v\n", tslinkBinaryRoot, err)
		}
	}
	exitTestMain(code)
}

// exitTestMain runs deferred cleanups (the isolated config dir) before exiting.
func exitTestMain(code int) {
	if code != 0 {
		defer os.Exit(code)
		return
	}
	defer os.Exit(0)
}

func restoreShareSeams(t *testing.T) {
	t.Helper()
	oldEnsure := shareEnsureDirFn
	oldRegistryPath := shareRegistryPathFn
	oldOwnershipPath := shareOwnershipPathFn
	oldPIDPath := sharePIDPathFn
	oldSnapshotPath := shareSnapshotPathFn
	oldAuthPath := shareAuthHandoffPathFn
	oldRunning := shareIsRunningFn
	oldStart := shareStartDaemonFn
	oldResolve := shareResolveEndpointOnceFn
	oldStatus := sharePollableStatusFn
	oldAdd := shareAddIfMissingFn
	t.Cleanup(func() {
		shareEnsureDirFn = oldEnsure
		shareRegistryPathFn = oldRegistryPath
		shareOwnershipPathFn = oldOwnershipPath
		sharePIDPathFn = oldPIDPath
		shareSnapshotPathFn = oldSnapshotPath
		shareAuthHandoffPathFn = oldAuthPath
		shareIsRunningFn = oldRunning
		shareStartDaemonFn = oldStart
		shareResolveEndpointOnceFn = oldResolve
		sharePollableStatusFn = oldStatus
		shareAddIfMissingFn = oldAdd
	})
}

func TestInferShareTarget(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "actual")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "Report Final.html")
	if err := os.WriteFile(file, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}

	directory, err := inferShareTarget(dir, true)
	canonicalDir, canonicalErr := filepath.EvalSymlinks(dir)
	if err != nil || canonicalErr != nil || directory.Service.Type != registry.TypeFile || directory.Service.Path != canonicalDir || !directory.Service.Ephemeral || directory.FileName != "" {
		t.Fatalf("directory = %+v err=%v", directory, err)
	}
	regular, err := inferShareTarget(file, false)
	if err != nil || regular.Service.Path != canonicalDir || regular.FileName != "Report Final.html" || regular.Service.Ephemeral {
		t.Fatalf("file = %+v err=%v", regular, err)
	}
	port, err := inferShareTarget("3000", true)
	if err != nil || port.Service.Target != "http://localhost:3000" || port.NameBase != "port-3000" {
		t.Fatalf("port = %+v err=%v", port, err)
	}
	hostPort, err := inferShareTarget("127.0.0.1:8080", true)
	if err != nil || hostPort.Service.Target != "http://127.0.0.1:8080" {
		t.Fatalf("hostPort = %+v err=%v", hostPort, err)
	}
	uppercaseHost, err := inferShareTarget("LOCALHOST:3000", true)
	if err != nil || uppercaseHost.Service.Target != "http://localhost:3000" || uppercaseHost.Service.Target != port.Service.Target {
		t.Fatalf("uppercaseHost = %+v port=%+v err=%v", uppercaseHost, port, err)
	}
	ipv6, err := inferShareTarget("[::1]:8443", true)
	if err != nil || ipv6.Service.Target != "http://[::1]:8443" {
		t.Fatalf("ipv6 = %+v err=%v", ipv6, err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(dir, link); err != nil {
		t.Logf("symlink normalization not available: %v", err)
	} else {
		linked, linkErr := inferShareTarget(link, true)
		if linkErr != nil || linked.Service.Path != directory.Service.Path || !sameShareTarget(linked.Service, directory.Service) {
			t.Fatalf("linked = %+v directory=%+v err=%v", linked, directory, linkErr)
		}
	}

	for _, invalid := range []string{"", "0", "+3000", "65536", "localhost", ":3000", "https://localhost:3000"} {
		if _, err := inferShareTarget(invalid, true); err == nil {
			t.Errorf("inferShareTarget(%q) error = nil", invalid)
		}
	}
}

func TestShareNamesAndCollisionNeverUpsert(t *testing.T) {
	if got := sanitizeShareName(" Report FINAL.html "); got != "report-final-html" {
		t.Fatalf("sanitize = %q", got)
	}
	unicodeName := sanitizeShareName("中文")
	if unicodeName == "share" || unicodeName != sanitizeShareName("中文") || !strings.HasPrefix(unicodeName, "share-") {
		t.Fatalf("unicode sanitize = %q, want stable hashed fallback", unicodeName)
	}
	long := sanitizeShareName(strings.Repeat("a", 80))
	if len(long) != 63 {
		t.Fatalf("long name len = %d", len(long))
	}
	if got := suffixedShareName(long, 12); len(got) != 63 || !strings.HasSuffix(got, "-12") {
		t.Fatalf("suffixed = %q len=%d", got, len(got))
	}

	restoreShareSeams(t)
	regPath := filepath.Join(t.TempDir(), "registry.json")
	spec := shareTargetSpec{Service: registry.Service{Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true}, NameBase: "Demo App"}
	first, firstCreated, err := registerShare(regPath, spec, "")
	if err != nil || !firstCreated {
		t.Fatal(err)
	}
	differentTarget := spec
	differentTarget.Service.Target = "http://localhost:3001"
	second, secondCreated, err := registerShare(regPath, differentTarget, "")
	if err != nil || !secondCreated {
		t.Fatal(err)
	}
	if first.Name != "demo-app" || second.Name != "demo-app-2" {
		t.Fatalf("names = %q, %q", first.Name, second.Name)
	}
	if _, created, err := registerShare(regPath, spec, "requested-renamed-share"); err == nil || created || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), first.Name) {
		t.Fatalf("mismatched requested name created=%v err=%v", created, err)
	}
	reused, reusedCreated, err := registerShare(regPath, spec, first.Name)
	if err != nil || reusedCreated || reused.Name != first.Name {
		t.Fatalf("matching requested name reuse = %+v created=%v err=%v", reused, reusedCreated, err)
	}
	reg, err := registry.Load(regPath)
	if err != nil || len(reg.Services) != 2 || !reg.Services[0].Ephemeral {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
	if _, _, err := registerShare(regPath, spec, "Bad_Name"); err == nil {
		t.Fatal("invalid explicit name accepted")
	}
}

func TestSameShareTargetRequiresMatchingExposurePosture(t *testing.T) {
	plain := registry.Service{Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true}
	if !sameShareTarget(plain, plain) {
		t.Fatal("identical plain share target did not match")
	}
	cases := []struct {
		name   string
		mutate func(*registry.Service)
	}{
		{"funnel", func(svc *registry.Service) { svc.Funnel = true }},
		{"public acknowledgement", func(svc *registry.Service) { svc.PublicAck = true }},
		{"allow list", func(svc *registry.Service) { svc.AllowedUsers = []string{"nobody@example.com"} }},
		{"custom domain", func(svc *registry.Service) { svc.Domain = "preview.example.com" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := plain
			tc.mutate(&existing)
			if sameShareTarget(existing, plain) {
				t.Fatalf("%s posture was silently reusable: %s", tc.name, shareExposurePosture(existing))
			}
			t.Logf("posture=%s same_share_target=false (%s)", tc.name, shareExposurePosture(existing))
		})
	}
}

func TestExecuteShareRejectsConflictingExposurePosture(t *testing.T) {
	cases := []struct {
		name         string
		service      registry.Service
		wantFragment string
	}{
		{
			name: "public funnel",
			service: registry.Service{
				Name: "public-demo", Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true,
				Funnel: true, PublicAck: true,
			},
			wantFragment: "funnel=true",
		},
		{
			name: "allow list",
			service: registry.Service{
				Name: "restricted-demo", Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true,
				AllowedUsers: []string{"nobody@example.com"},
			},
			wantFragment: "allowed_users=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restoreShareSeams(t)
			regPath := filepath.Join(t.TempDir(), "registry.json")
			tc.service.CreatedAt = time.Unix(1, 0).UTC()
			fixture := registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{tc.service}}
			data, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(regPath, data, 0o600); err != nil {
				t.Fatal(err)
			}

			_, err = executeShare(context.Background(), sharePaths{Registry: regPath}, "3000", "", true, time.Second, io.Discard)
			if err == nil || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), tc.service.Name) || !strings.Contains(err.Error(), tc.wantFragment) {
				t.Fatalf("conflict err = %v", err)
			}
			reg, loadErr := registry.Load(regPath)
			if loadErr != nil || len(reg.Services) != 1 || reg.Services[0].Name != tc.service.Name {
				t.Fatalf("registry = %+v err=%v", reg, loadErr)
			}
			t.Logf("posture=%s result=ERROR %q registry_services=%d retained=%q", tc.name, err, len(reg.Services), reg.Services[0].Name)
		})
	}
}

func TestDirectFileURL(t *testing.T) {
	got, err := directFileURL("https://files.tail.ts.net", "Report #1.html")
	if err != nil || got != "https://files.tail.ts.net/Report%20%231.html" {
		t.Fatalf("directFileURL = %q err=%v", got, err)
	}
	got, err = directFileURL("https://files.tail.ts.net", "")
	if err != nil || got != "https://files.tail.ts.net" {
		t.Fatalf("base = %q err=%v", got, err)
	}
}

func TestExecuteShareStartsDaemonAndSurfacesNeedsLogin(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
	}
	shareIsRunningFn = func(string) bool { return false }
	started := false
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		started = true
		return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/unit"}, nil
	}
	result, err := executeShare(context.Background(), paths, "3000", "", true, time.Second, io.Discard)
	if err != nil || !started || result.Status != authStatusNeedsLogin || result.AuthURL == "" {
		t.Fatalf("result = %+v started=%v err=%v", result, started, err)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Name != "port-3000" || !reg.Services[0].Ephemeral {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
}

func TestExecuteShareNeedsLoginRetriesReuseSingleService(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		PID:         filepath.Join(dir, "tslink.pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
	}
	daemonUp := false
	shareIsRunningFn = func(string) bool { return daemonUp }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		daemonUp = true
		return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/retry"}, nil
	}
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{}, registry.URLNotReadyError(name)
	}
	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) {
		return StatusResult{AuthStatus: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/retry"}, nil
	}

	for attempt := 1; attempt <= 5; attempt++ {
		result, err := executeShare(context.Background(), paths, "3000", "", true, time.Second, io.Discard)
		if err != nil || result.Status != authStatusNeedsLogin || result.serviceName != "port-3000" {
			t.Fatalf("attempt %d result=%+v err=%v", attempt, result, err)
		}
		reg, loadErr := registry.Load(paths.Registry)
		if loadErr != nil || len(reg.Services) != 1 || reg.Services[0].Name != "port-3000" {
			t.Fatalf("attempt %d registry=%+v err=%v", attempt, reg, loadErr)
		}
		t.Logf("attempt %d: status=%s service=%s registry_services=%d", attempt, result.Status, result.serviceName, len(reg.Services))
	}
}

// TestShareDaemonStartIsGatedOnTheRunningPredicate covers the `share` half of
// the B16 incident shape: 83 orphaned `tslink serve` processes.
//
// `share` starts a daemon only when shareIsRunningFn (daemon.IsRunning) says
// none is running, and its failure path rolls back the registry entry it
// created without reclaiming any daemon it started. So if that predicate ever
// false-negatives on a live daemon — which E1 shows happens on the
// cross-binary-identity path — every `share` invocation starts another daemon
// and nothing removes them. That is per-invocation and measurable, unlike the
// "accumulation over hours" framing that Round C-1 filed as operational.
//
// Two things this test deliberately does NOT do, and why:
//
//  1. It does not run at process level. Making an accumulation assertion go red
//     requires the running predicate to lie, and under that mutation the real
//     `share` execs a real `tslink serve`, which contacts the Tailscale control
//     plane. That is forbidden here, so a process-level version of this
//     scenario could only ever be observed green. A green-only assertion is the
//     same defect class Round C-1's review found in E5, and adding one would be
//     worse than not adding it.
//
//  2. It does not assert that `share` ought to reclaim the daemon. It should
//     not. The daemon is a shared resource: other registered services and other
//     concurrent `share` invocations bind to it, so tearing it down on this
//     invocation's failure would break them. Rolling back the registry entry
//     `share` created while leaving the daemon it did not exclusively own is
//     the right ownership boundary.
//
// What is falsifiable here, and is asserted: `share` must consult the predicate
// before starting a daemon. Removing that gate is a realistic regression ("just
// always make sure the daemon is up") and it leaks one daemon per invocation
// even when the predicate is working correctly.
//
// TODO(daemon-mutual-exclusion): the invariant this file cannot express is "at
// most one daemon per config dir".
//
// `share` enforces it through shareIsRunningFn (daemon.IsRunning), a process
// *identity* predicate, and E1 shows that predicate false-negatives on the
// cross-binary-identity path. Under that false negative every `share` invocation
// starts another daemon and nothing reclaims them. The enforcement point is not
// `share` — it asks the only question it can — but the daemon: an
// identity-independent mutual exclusion held for the daemon's lifetime.
// internal/filelock already exists in this repository; its only direct test file
// is //go:build !windows, so its 100% statement coverage is the current Unix
// target's, not a cross-platform guarantee — the Windows LockFileEx/UnlockFileEx
// path is compiled and vetted but never executed. That is a production change,
// outside this round's scope, and it is recorded here rather than in a test.
//
// A previous revision recorded it in a test instead, with a second subtest
// asserting the current broken behaviour (five invocations under a false
// negative produce five daemons) on the theory that it would go red the day
// somebody adds the mutex, making that change deliberate. The review disproved
// the theory rather than disagreeing with the trade-off: that subtest replaced
// shareStartDaemonFn wholesale with a counting stub, so the real
// startShareDaemon body — and therefore anything the daemon might do about
// mutual exclusion — could not affect the count it asserted. Putting
// panic("real daemon-start path reached") at the top of the real
// startShareDaemon left both subtests green. A test that cannot observe the
// event it claims to watch is not protection; it is a defect wearing the
// costume of a contract, and the count it locks in is a number nobody should
// have to preserve. It was deleted, and this comment is where the defect lives
// now.
func TestShareDaemonStartIsGatedOnTheRunningPredicate(t *testing.T) {
	newPaths := func(t *testing.T) sharePaths {
		t.Helper()
		dir := t.TempDir()
		return sharePaths{
			Registry:    filepath.Join(dir, "registry.json"),
			PID:         filepath.Join(dir, "tslink.pid"),
			Snapshot:    filepath.Join(dir, "runtime.json"),
			AuthHandoff: filepath.Join(dir, "auth-handoff.json"),
		}
	}

	t.Run("a recognised live daemon is never given a second one", func(t *testing.T) {
		restoreShareSeams(t)
		paths := newPaths(t)
		starts := 0
		shareIsRunningFn = func(string) bool { return true }
		shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
			starts++
			return shareDaemonStart{}, nil
		}
		shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }

		const invocations = 5
		for attempt := 1; attempt <= invocations; attempt++ {
			if _, err := executeShare(context.Background(), paths, "3000", "", true, 0, io.Discard); codeOf(err) != registry.CodeURLNotReady {
				t.Fatalf("attempt %d err = %v, want url_not_ready", attempt, err)
			}
		}
		if starts != 0 {
			t.Fatalf("share started %d daemon(s) across %d invocations while one was already running; want 0",
				starts, invocations)
		}
	})
}

func TestExecuteShareFailuresRollBackNewRegistration(t *testing.T) {
	t.Run("daemon start failure", func(t *testing.T) {
		restoreShareSeams(t)
		dir := t.TempDir()
		paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid")}
		shareIsRunningFn = func(string) bool { return false }
		shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
			return shareDaemonStart{}, errors.New("daemon failed")
		}
		if _, err := executeShare(context.Background(), paths, "3000", "", true, time.Second, io.Discard); err == nil {
			t.Fatal("daemon failure error = nil")
		}
		reg, err := registry.Load(paths.Registry)
		if err != nil || len(reg.Services) != 0 {
			t.Fatalf("registry after daemon failure = %+v err=%v", reg, err)
		}
	})

	t.Run("URL timeout", func(t *testing.T) {
		restoreShareSeams(t)
		dir := t.TempDir()
		paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid")}
		shareIsRunningFn = func(string) bool { return true }
		shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }
		if _, err := executeShare(context.Background(), paths, "3000", "", true, 0, io.Discard); codeOf(err) != registry.CodeURLNotReady {
			t.Fatalf("URL timeout err = %v", err)
		}
		reg, err := registry.Load(paths.Registry)
		if err != nil || len(reg.Services) != 0 {
			t.Fatalf("registry after URL timeout = %+v err=%v", reg, err)
		}
	})
}

func TestExecuteShareRunningReturnsExactFileURL(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "report final.html")
	if err := os.WriteFile(file, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json")}
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net", State: "exact"}}, nil
	}
	result, err := executeShare(context.Background(), paths, file, "preview", false, time.Second, io.Discard)
	if err != nil || result.Name != "preview" || result.Status != shareStatusReady || result.URL != "https://preview.tail.ts.net/report%20final.html" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || reg.Services[0].Ephemeral {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
}

func TestWaitForShareOutcomePollsAndHandlesLoginTimeoutAndContext(t *testing.T) {
	restoreShareSeams(t)
	paths := sharePaths{}
	calls := 0
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		calls++
		if calls < 2 {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://ready.tail.ts.net"}}, nil
	}
	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }
	result, err := waitForShareOutcome(context.Background(), paths, "ready", "", 500*time.Millisecond)
	if err != nil || result.URL != "https://ready.tail.ts.net" || calls < 2 {
		t.Fatalf("result = %+v calls=%d err=%v", result, calls, err)
	}

	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{}, registry.URLNotReadyError(name)
	}
	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) {
		return StatusResult{AuthStatus: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/poll"}, nil
	}
	result, err = waitForShareOutcome(context.Background(), paths, "login", "", time.Second)
	if err != nil || result.Status != authStatusNeedsLogin {
		t.Fatalf("login result = %+v err=%v", result, err)
	}

	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }
	if _, err := waitForShareOutcome(context.Background(), paths, "timeout", "", 0); codeOf(err) != registry.CodeURLNotReady {
		t.Fatalf("zero wait err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForShareOutcome(ctx, paths, "cancel", "", time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err = %v", err)
	}
}

func codeOf(err error) string {
	code, _ := registry.ErrorCode(err)
	return code
}

func TestParseAndRunShareDaemon(t *testing.T) {
	login := output.NewSuccess("serve", map[string]any{"status": authStatusNeedsLogin, "auth_url": "https://login.tailscale.com/a/parse"})
	encoded, _ := json.Marshal(login)
	result, err := parseShareDaemonResult(encoded, nil)
	if err != nil || result.Status != authStatusNeedsLogin || result.AuthURL == "" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
	failure, _ := json.Marshal(output.NewFailure("serve", output.ExitConflict, "already running"))
	if _, err := parseShareDaemonResult(failure, nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("failure err = %v", err)
	}
	failureWithoutObject := []byte(`{"ok":false,"code":1}`)
	if _, err := parseShareDaemonResult(failureWithoutObject, nil); err == nil || !strings.Contains(err.Error(), "exit code 1") {
		t.Fatalf("failure without object err = %v", err)
	}
	if _, err := parseShareDaemonResult([]byte("bad"), errors.New("exit 1")); err == nil || !strings.Contains(err.Error(), "exit 1") {
		t.Fatalf("invalid err = %v", err)
	}
	if _, err := parseShareDaemonResult([]byte("bad"), nil); err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("invalid JSON err = %v", err)
	}

	t.Setenv("TSLINK_SHARE_DAEMON_HELPER", "login")
	var stderr bytes.Buffer
	result, err = startShareDaemon(context.Background(), &stderr)
	if err != nil || result.AuthURL != "https://login.tailscale.com/a/helper" || !strings.Contains(stderr.String(), "helper diagnostic") {
		t.Fatalf("helper result = %+v stderr=%q err=%v", result, stderr.String(), err)
	}
	t.Setenv("TSLINK_SHARE_DAEMON_HELPER", "ready")
	result, err = startShareDaemon(context.Background(), io.Discard)
	if err != nil || result.Status != "" {
		t.Fatalf("ready helper = %+v err=%v", result, err)
	}
}

func TestResolveSharePathsAndCommandOutput(t *testing.T) {
	restoreShareSeams(t)
	resetRootJSONFlag(t)
	dir := t.TempDir()
	paths := sharePaths{
		Registry:    filepath.Join(dir, "registry.json"),
		Ownership:   filepath.Join(dir, "node-ownership.json"),
		PID:         filepath.Join(dir, "pid"),
		Snapshot:    filepath.Join(dir, "runtime.json"),
		AuthHandoff: filepath.Join(dir, "auth.json"),
	}
	shareEnsureDirFn = func() error { return nil }
	shareRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	shareOwnershipPathFn = func() (string, error) { return paths.Ownership, nil }
	sharePIDPathFn = func() (string, error) { return paths.PID, nil }
	shareSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	shareAuthHandoffPathFn = func() (string, error) { return paths.AuthHandoff, nil }
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://" + name + ".tail.ts.net"}}, nil
	}

	resolved, err := resolveSharePaths()
	if err != nil || resolved != paths {
		t.Fatalf("paths = %+v err=%v", resolved, err)
	}
	shareCmd, _, err := rootCmd.Find([]string{"share"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shareCmd.SetOut(nil)
		shareCmd.SetErr(nil)
		_ = shareCmd.Flags().Set("name", "")
		_ = shareCmd.Flags().Set("ephemeral", "true")
		_ = shareCmd.Flags().Set("wait", "30s")
	})
	_ = shareCmd.Flags().Set("name", "one-shot")
	_ = shareCmd.Flags().Set("ephemeral", "true")
	_ = shareCmd.Flags().Set("wait", "30s")
	var stdout, stderr bytes.Buffer
	shareCmd.SetOut(&stdout)
	shareCmd.SetErr(&stderr)
	if err := shareCmd.RunE(shareCmd, []string{"8080"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "https://one-shot.tail.ts.net\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	setRootJSONFlag(t, true)
	_ = shareCmd.Flags().Set("name", "json-share")
	encoded := captureStdout(t, func() {
		if err := shareCmd.RunE(shareCmd, []string{"8081"}); err != nil {
			t.Fatal(err)
		}
	})
	var envelope output.Result
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil || !envelope.OK || envelope.Command != "share" {
		t.Fatalf("JSON = %q envelope=%+v err=%v", encoded, envelope, err)
	}
	setRootJSONFlag(t, false)
	shareIsRunningFn = func(string) bool { return false }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/command"}, nil
	}
	_ = shareCmd.Flags().Set("name", "login-share")
	stdout.Reset()
	stderr.Reset()
	if err := shareCmd.RunE(shareCmd, []string{"8082"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "https://login.tailscale.com/a/command\n" || !strings.Contains(stderr.String(), "authorization is required") {
		t.Fatalf("login stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	flag := shareCmd.Flags().Lookup("wait")
	if flag == nil || flag.NoOptDefVal != "30s" || flag.DefValue != "30s" {
		t.Fatalf("wait flag = %+v", flag)
	}
}

func TestShareErrorPaths(t *testing.T) {
	restoreShareSeams(t)
	paths := sharePaths{Registry: filepath.Join(t.TempDir(), "registry.json")}
	if _, err := executeShare(context.Background(), paths, "not-a-target", "", true, time.Second, io.Discard); err == nil {
		t.Fatal("invalid target error = nil")
	}
	shareAddIfMissingFn = func(string, registry.Service) (bool, error) { return false, errors.New("registry failed") }
	if _, err := executeShare(context.Background(), paths, "3000", "", true, time.Second, io.Discard); err == nil || !strings.Contains(err.Error(), "registry failed") {
		t.Fatalf("registry err = %v", err)
	}
	shareAddIfMissingFn = registry.AddIfMissing
	shareIsRunningFn = func(string) bool { return false }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		return shareDaemonStart{}, errors.New("daemon failed")
	}
	if _, err := executeShare(context.Background(), paths, "3001", "", true, time.Second, io.Discard); err == nil || !strings.Contains(err.Error(), "daemon failed") {
		t.Fatalf("daemon err = %v", err)
	}

	shareResolveEndpointOnceFn = func(_, _, _, _ string) (serviceURLResolution, error) {
		return serviceURLResolution{}, errors.New("resolve failed")
	}
	if _, _, err := shareOutcomeOnce(paths, "demo", ""); err == nil || !strings.Contains(err.Error(), "resolve failed") {
		t.Fatalf("resolve err = %v", err)
	}
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{}, registry.URLNotReadyError(name)
	}
	sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, errors.New("status failed") }
	if _, _, err := shareOutcomeOnce(paths, "demo", ""); err == nil || !strings.Contains(err.Error(), "status failed") {
		t.Fatalf("status err = %v", err)
	}

	if got := suffixedShareName("-", 2); got != "share-2" {
		t.Fatalf("empty suffix base = %q", got)
	}
	if _, err := directFileURL(":\x00", "file"); err == nil {
		t.Fatal("invalid base URL accepted")
	}
}

func TestResolveSharePathsErrors(t *testing.T) {
	restoreShareSeams(t)
	want := errors.New("path failed")
	shareRegistryPathFn = func() (string, error) { return "", want }
	if _, err := resolveSharePaths(); !errors.Is(err, want) {
		t.Fatalf("registry err = %v", err)
	}
	shareRegistryPathFn = func() (string, error) { return "registry", nil }
	shareOwnershipPathFn = func() (string, error) { return "", want }
	if _, err := resolveSharePaths(); !errors.Is(err, want) {
		t.Fatalf("ownership err = %v", err)
	}
	shareOwnershipPathFn = func() (string, error) { return "ownership", nil }
	sharePIDPathFn = func() (string, error) { return "", want }
	if _, err := resolveSharePaths(); !errors.Is(err, want) {
		t.Fatalf("pid err = %v", err)
	}
	sharePIDPathFn = func() (string, error) { return "pid", nil }
	shareSnapshotPathFn = func() (string, error) { return "", want }
	if _, err := resolveSharePaths(); !errors.Is(err, want) {
		t.Fatalf("snapshot err = %v", err)
	}
	shareSnapshotPathFn = func() (string, error) { return "snapshot", nil }
	shareAuthHandoffPathFn = func() (string, error) { return "", want }
	if _, err := resolveSharePaths(); !errors.Is(err, want) {
		t.Fatalf("auth err = %v", err)
	}
}
