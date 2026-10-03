package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestRepairLiveDaemonRequiresSupervision(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    Supervision
		ok   bool
	}{
		{"manual", Supervision{Manager: "manual"}, false},
		{"none", Supervision{Manager: "none"}, false},
		{"unknown", Supervision{Manager: "other", Installed: true, Autostart: true, RestartOnExit: true}, false},
		{"uninstalled", Supervision{Manager: "launchd", Autostart: true, RestartOnExit: true}, false},
		{"disabled", Supervision{Manager: "launchd", Installed: true, RestartOnExit: true}, false},
		{"no_restart", Supervision{Manager: "systemd", Installed: true, Autostart: true}, false},
		{"launchd_owned", Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true}, true},
		{"systemd_owned", Supervision{Manager: "systemd", Installed: true, Autostart: true, RestartOnExit: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateBootstrap(t)
			isRunningFn = func(string) bool { return true }
			inspected := 0
			detectSupervisionFn = func(context.Context, string, bool, int) Supervision { inspected++; return tc.s }
			err := ensureDaemon(context.Background(), io.Discard, false)
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var coded registry.CodedError
				if !errors.As(err, &coded) || coded.Code != "daemon_supervision_unverified" {
					t.Fatalf("unsafe live daemon accepted or wrong recovery: %v", err)
				}
				for _, action := range []string{"tslink doctor", "tslink stop", "tslink install"} {
					if !strings.Contains(err.Error(), action) {
						t.Errorf("missing recovery %q: %v", action, err)
					}
				}
			}
			if inspected != 1 {
				t.Errorf("ownership inspections=%d, want 1", inspected)
			}
		})
	}
}

func TestRepairOptOutDoesNotClaimSupervision(t *testing.T) {
	isolateBootstrap(t)
	isRunningFn = func(string) bool { return true }
	detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
		t.Fatal("opt-out inspected supervisor")
		return Supervision{}
	}
	if err := ensureDaemon(context.Background(), io.Discard, true); err != nil {
		t.Fatal(err)
	}
}

func TestRepairLiveDaemonRejectsUnreadablePID(t *testing.T) {
	for _, bad := range []string{"unreadable", "zero"} {
		t.Run(bad, func(t *testing.T) {
			isolateBootstrap(t)
			isRunningFn = func(string) bool { return true }
			readPIDFn = func(string) (int, error) {
				if bad == "zero" {
					return 0, nil
				}
				return 0, errors.New("PID unreadable")
			}
			detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
				t.Error("invalid PID reached ownership detector")
				return Supervision{}
			}
			err := ensureDaemon(context.Background(), io.Discard, false)
			var coded registry.CodedError
			if !errors.As(err, &coded) || coded.Code != "daemon_supervision_unverified" {
				t.Fatalf("invalid PID accepted: %v", err)
			}
		})
	}
}

// Exercise registry writes and the shipped command/tool handlers, not a canned
// coded error: the refusal must leave a discoverable service and its diagnosis.
func TestRepairSavedConfigurationRefusal(t *testing.T) {
	for _, scenario := range []string{
		"live/cli_add", "live/cli_template", "live/mcp_add", "live/mcp_template",
		"live/cli_template_existing", "live/mcp_template_existing",
		"setup/cli_add", "setup/cli_template", "setup/mcp_add", "setup/mcp_template",
		"setup/cli_template_existing", "setup/mcp_template_existing",
	} {
		t.Run(scenario, func(t *testing.T) {
			mode, entry, _ := strings.Cut(scenario, "/")
			existing := strings.HasSuffix(entry, "_existing")
			entry = strings.TrimSuffix(entry, "_existing")
			dir := isolateBootstrap(t)
			resetRootJSONFlag(t)
			paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
			var before []byte
			if existing {
				if _, err := applyTemplate("local-web", paths.Registry, false); err != nil {
					t.Fatal(err)
				}
				var err error
				before, err = os.ReadFile(paths.Registry)
				if err != nil {
					t.Fatal(err)
				}
				oldAdd := templateAddIfMissingFn
				t.Cleanup(func() { templateAddIfMissingFn = oldAdd })
				templateAddIfMissingFn = func(string, registry.Service) (bool, error) {
					t.Fatal("all-existing template attempted a registry write")
					return false, nil
				}
			}
			isRunningFn = func(string) bool { return mode == "live" }
			inspections := 0
			checkPersisted := func() {
				inspections++
				reg, err := registry.Load(paths.Registry)
				if err != nil || len(reg.Services) == 0 {
					t.Fatalf("supervision checked before persistence: %+v, %v", reg, err)
				}
			}
			detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
				checkPersisted()
				return unmanagedSupervision(true, "fixture manager does not own PID 4242")
			}
			installDaemonFn = func(context.Context, io.Writer) error {
				checkPersisted()
				return errors.New("fixture install failure")
			}
			wantCode := "daemon_supervision_unverified"
			wantNext := []string{"tslink doctor", "tslink stop", "tslink install"}
			wantText := []string{"fixture manager does not own PID 4242", "no process was taken over"}
			if mode == "setup" {
				wantCode = "daemon_setup_failed"
				wantNext = []string{"tslink logs", "tslink doctor", "tslink install"}
				wantText = []string{"fixture install failure", "No supervisor definition was found"}
			}
			var failure output.Result
			var human string
			switch entry {
			case "cli_add":
				_, err := runAddCmdOutput(t, []string{"saved-app"}, map[string]string{"proxy": "localhost:3000"})
				if err == nil {
					t.Fatal("unverified live daemon accepted")
				}
				human = err.Error()
				failure = output.NewFailureForError("add", err)
			case "cli_template":
				command, _, err := rootCmd.Find([]string{"template", "apply"})
				if err != nil {
					t.Fatal(err)
				}
				resetCommandLocalFlags(t, command)
				setCommandTestContext(t, command)
				if err := command.Flags().Set("yes", "true"); err != nil {
					t.Fatal(err)
				}
				err = command.RunE(command, []string{"local-web"})
				if err == nil {
					t.Fatal("unverified live daemon accepted")
				}
				human = err.Error()
				failure = output.NewFailureForError("template apply", err)
			default:
				tool, args := "add", `{"name":"saved-app","type":"proxy","target":"localhost:3000"}`
				if entry == "mcp_template" {
					tool, args = "template_apply", `{"name":"local-web"}`
				}
				result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), tool, json.RawMessage(args))
				if err != nil || result == nil || !result.IsError {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				// The failure object is the text of the result (A3-1).
				failure = mcpToolResultFailure(t, result)
				human = failure.Error.Message
			}
			if inspections != 1 {
				t.Fatalf("inspections=%d, want 1", inspections)
			}
			if failure.OK || failure.Code != 1 || failure.Error == nil || failure.Error.Code != wantCode {
				t.Fatalf("wrong refusal: %+v", failure)
			}
			for _, message := range []string{human, failure.Error.Message} {
				for _, want := range append(append(wantText, wantNext...), "Configuration remains in the registry", "tslink list", "retry the original command") {
					if !strings.Contains(message, want) {
						t.Errorf("missing %q in %q", want, message)
					}
				}
			}
			if !reflect.DeepEqual(failure.Error.Next, wantNext) {
				t.Fatalf("recovery commands changed: %v", failure.Error.Next)
			}
			reg, err := registry.Load(paths.Registry)
			if err != nil || len(reg.Services) == 0 {
				t.Fatalf("saved registry lost: %+v, %v", reg, err)
			}
			if existing {
				after, err := os.ReadFile(paths.Registry)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("all-existing template changed registry bytes: %v", err)
				}
				for _, message := range []string{human, failure.Error.Message} {
					if strings.Contains(message, "has been saved") {
						t.Errorf("no-op template claims this invocation saved configuration: %s", message)
					}
				}
			}
			if strings.HasSuffix(entry, "add") && (len(reg.Services) != 1 || reg.Services[0].Name != "saved-app" || reg.Services[0].Target != "http://localhost:3000") {
				t.Fatalf("saved service changed: %+v", reg)
			}
			// The advertised list command must actually expose the saved names.
			command, _, err := rootCmd.Find([]string{"list"})
			if err != nil {
				t.Fatal(err)
			}
			resetCommandLocalFlags(t, command)
			var listed bytes.Buffer
			command.SetOut(&listed)
			defer command.SetOut(nil)
			if err := command.RunE(command, nil); err != nil {
				t.Fatal(err)
			}
			for _, service := range reg.Services {
				if !strings.Contains(listed.String(), service.Name) {
					t.Errorf("list omitted saved service %q", service.Name)
				}
			}
		})
	}
}

func TestRepairShareRefusalDoesNotClaimSavedConfiguration(t *testing.T) {
	for _, scenario := range []string{"live/cli", "live/mcp", "install/cli", "install/mcp", "settle/cli", "settle/mcp"} {
		t.Run(scenario, func(t *testing.T) {
			mode, entry, _ := strings.Cut(scenario, "/")
			dir := isolateBootstrap(t)
			restoreShareSeams(t)
			resetRootJSONFlag(t)
			paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
			// Model a daemon appearing between share's liveness check and the
			// locked setup check, where share really can receive this refusal.
			shareIsRunningFn = func(string) bool { return false }
			shareStartDaemonFn = startShareDaemon
			isRunningFn = func(string) bool { return mode == "live" }
			installs := 0
			installDaemonFn = func(context.Context, io.Writer) error {
				installs++
				reg, err := registry.Load(paths.Registry)
				if err != nil || len(reg.Services) != 1 {
					t.Fatalf("share setup did not follow registration: %+v, %v", reg, err)
				}
				if mode == "install" {
					return errors.New("fixture share install failure")
				}
				return nil // Remains stopped: the real supervision-settle gate fails.
			}
			wantCode := "daemon_supervision_unverified"
			wantNext := []string{"tslink doctor", "tslink stop", "tslink install"}
			if mode != "live" {
				wantCode = "daemon_setup_failed"
				wantNext = []string{"tslink logs", "tslink doctor", "tslink install"}
			}
			var failure output.Result
			var message string
			if entry == "cli" {
				command, _, err := rootCmd.Find([]string{"share"})
				if err != nil {
					t.Fatal(err)
				}
				resetCommandLocalFlags(t, command)
				setCommandTestContext(t, command)
				err = command.RunE(command, []string{"3000"})
				if code, _ := registry.ErrorCode(err); code != wantCode {
					t.Fatalf("err=%v", err)
				}
				failure = output.NewFailureForError("share", err)
				message = err.Error()
			} else {
				result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "share", json.RawMessage(`{"target":"3000"}`))
				if err != nil || result == nil || !result.IsError {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				// The failure object is the text of the result (A3-1).
				failure = mcpToolResultFailure(t, result)
				message = failure.Error.Message
			}
			if failure.OK || failure.Code != 1 || failure.Error == nil || failure.Error.Code != wantCode || !reflect.DeepEqual(failure.Error.Next, wantNext) {
				t.Fatalf("wrong share failure/recovery: %+v", failure)
			}
			for _, message := range []string{message, failure.Error.Message} {
				if strings.Contains(strings.ToLower(message), "saved") || strings.Contains(message, "tslink list") || strings.Contains(message, "remains in the registry") {
					t.Errorf("rolled-back share claims saved configuration: %s", message)
				}
				for _, next := range wantNext {
					if !strings.Contains(message, next) {
						t.Errorf("missing recovery %q: %s", next, message)
					}
				}
			}
			wantInstalls := 0
			if mode != "live" {
				wantInstalls = 1
			}
			if installs != wantInstalls {
				t.Fatalf("installs=%d, want %d", installs, wantInstalls)
			}
			reg, err := registry.Load(paths.Registry)
			if err != nil || len(reg.Services) != 0 {
				t.Fatalf("share rollback changed: %+v, %v", reg, err)
			}
		})
	}
}

func TestRepairSavedConfigurationErrorPreservesOtherErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New("storage failure"), registry.CodedError{Code: "daemon_identity_unverified", Message: "original identity diagnosis"}} {
		// CodedError contains a slice, so use reflect for the value variant.
		if got := daemonRegistryRetainedError(err); !reflect.DeepEqual(got, err) {
			t.Fatalf("changed unrelated error: %v -> %v", err, got)
		}
	}
}

// Check cancellation synchronously at the first post-open observation. This
// wraps a real cancel context, retaining its stable Err/Done contract; no sleep
// or scheduler ordering is needed to hit the pre-lock window.
type supervisorErrObserver struct {
	context.Context
	observe func()
}

func (ctx supervisorErrObserver) Err() error {
	ctx.observe()
	return ctx.Context.Err()
}

// probeTransactionDescriptor returns nil for an open file and os.ErrClosed for
// a closed one, on every platform. Stat does not: on Windows it asks
// GetFileType about the handle, so a file that was correctly closed answers
// "The handle is invalid" instead of os.ErrClosed and read as a leak. Seek
// consults the file's own closed state first, as Stat does on unix.
func probeTransactionDescriptor(f *os.File) error {
	_, err := f.Seek(0, io.SeekCurrent)
	return err
}

func TestRepairSupervisorTransactionFailureBranches(t *testing.T) {
	for _, scenario := range []string{"open_error", "lock_error", "cancel_initial", "cancel_before_lock", "cancel_after_lock", "cancel_waiting", "callback_error", "nil_context"} {
		t.Run(scenario, func(t *testing.T) {
			isolateBootstrap(t)
			path, err := supervisorPath()
			if err != nil {
				t.Fatal(err)
			}
			lockPath := path + ".bootstrap.lock"
			oldTry, oldOpen := trySupervisorLockFn, openSupervisorLockFn
			t.Cleanup(func() { trySupervisorLockFn, openSupervisorLockFn = oldTry, oldOpen })
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ctx context.Context = base
			var opened *os.File
			openSupervisorLockFn = func(name string, flag int, perm os.FileMode) (*os.File, error) {
				f, err := os.OpenFile(name, flag, perm)
				if err == nil {
					opened = f
					// Keep the real file reachable until the closure assertion;
					// clean up leaks after a failed mutation assertion as well.
					t.Cleanup(func() { f.Close() })
				}
				return f, err
			}
			var lockErr error
			var openErr *os.PathError
			attempts, callbacks := 0, 0
			callbackErr := errors.New("original callback failure")
			trySupervisorLockFn = func(f *os.File) (bool, error) {
				if f != opened {
					t.Fatal("lock seam did not receive the opened transaction descriptor")
				}
				attempts++
				if scenario == "lock_error" {
					// Ask the real platform backend to reject a closed descriptor;
					// return that exact error through the transaction, while leaving
					// its descriptor open so deferred Close is independently tested.
					closed, err := os.OpenFile(lockPath, os.O_RDWR, 0)
					if err != nil {
						t.Fatal(err)
					}
					if err := closed.Close(); err != nil {
						t.Fatal(err)
					}
					acquired, err := trySupervisorLock(closed)
					if acquired || err == nil {
						t.Fatalf("closed descriptor accepted: %t, %v", acquired, err)
					}
					lockErr = err
					return acquired, err
				}
				acquired, err := trySupervisorLock(f)
				if scenario == "cancel_after_lock" {
					if err != nil || !acquired {
						t.Fatalf("target lock not acquired: %t, %v", acquired, err)
					}
					assertSupervisorLockState(t, lockPath, false)
					cancel()
				}
				if scenario == "cancel_waiting" {
					if err != nil || acquired {
						t.Fatalf("waiter did not contend: %t, %v", acquired, err)
					}
					cancel()
				}
				return acquired, err
			}
			switch scenario {
			case "open_error":
				// A directory at the lock-file path reliably makes OpenFile fail,
				// even for privileged test runners; no chmod/EACCES assumption.
				if err := os.MkdirAll(lockPath, 0o700); err != nil {
					t.Fatal(err)
				}
				control, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
				if control != nil {
					control.Close()
				}
				if !errors.As(err, &openErr) {
					t.Fatalf("OpenFile control did not fail: %v", err)
				}
			case "cancel_initial":
				cancel()
			case "cancel_before_lock":
				checks := 0
				ctx = supervisorErrObserver{Context: base, observe: func() {
					checks++
					if checks == 2 {
						if opened == nil || attempts != 0 {
							t.Fatal("cancellation missed the post-open/pre-lock window")
						}
						if err := probeTransactionDescriptor(opened); err != nil {
							t.Fatalf("transaction descriptor was not open before cancellation: %v", err)
						}
						if _, err := os.Stat(lockPath); err != nil {
							t.Fatalf("cancel was not after open: %v", err)
						}
						cancel()
					}
				}}
			case "cancel_waiting":
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				holder, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				acquired, err := trySupervisorLock(holder)
				if err != nil || !acquired {
					t.Fatalf("holder lock=%t, %v", acquired, err)
				}
				defer filelock.Unlock(holder)
			case "nil_context":
				ctx = nil
			}
			err = withSupervisorTransaction(ctx, func() error {
				callbacks++
				assertSupervisorLockState(t, lockPath, false)
				if scenario == "callback_error" {
					return callbackErr
				}
				return nil
			})
			wantCallbacks, wantAttempts := 0, 1
			switch scenario {
			case "open_error":
				wantAttempts = 0
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Op != "open" || pathErr.Path != lockPath || !errors.Is(err, openErr.Err) {
					t.Fatalf("original OpenFile error lost: %T %v", err, err)
				}
			case "lock_error":
				if err != lockErr || lockErr == nil {
					t.Fatalf("original lock error lost: %v, want %v", err, lockErr)
				}
			case "cancel_initial", "cancel_before_lock", "cancel_after_lock", "cancel_waiting":
				if err != context.Canceled {
					t.Fatalf("cancellation replaced: %v", err)
				}
				if scenario == "cancel_initial" || scenario == "cancel_before_lock" {
					wantAttempts = 0
				}
			case "callback_error":
				wantCallbacks = 1
				if err != callbackErr {
					t.Fatalf("original callback error lost: %v", err)
				}
			case "nil_context":
				wantCallbacks = 1
				if err != nil {
					t.Fatal(err)
				}
			}
			if callbacks != wantCallbacks || attempts != wantAttempts {
				t.Fatalf("callbacks=%d attempts=%d, want %d/%d", callbacks, attempts, wantCallbacks, wantAttempts)
			}
			if scenario == "cancel_before_lock" && opened == nil {
				t.Fatal("post-open cancellation skipped the descriptor closure assertion")
			}
			if opened != nil {
				if err := probeTransactionDescriptor(opened); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("transaction descriptor leaked: %v", err)
				}
			}
			if scenario == "cancel_initial" {
				if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("initial cancellation touched lock path: %v", err)
				}
			} else if scenario == "open_error" {
				info, err := os.Stat(lockPath)
				if err != nil || !info.IsDir() {
					t.Fatalf("OpenFile failure changed existing directory: %v", err)
				}
			} else {
				assertSupervisorLockState(t, lockPath, scenario != "cancel_waiting")
			}
		})
	}
}

func assertSupervisorLockState(t *testing.T, path string, wantAvailable bool) {
	t.Helper()
	probe, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	acquired, err := trySupervisorLock(probe)
	if acquired {
		defer filelock.Unlock(probe)
	}
	if err != nil || acquired != wantAvailable {
		t.Fatalf("lock available=%t, want %t, err=%v", acquired, wantAvailable, err)
	}
}
