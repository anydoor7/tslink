package cmd

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/cliargs"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestShareRearmCompensatesPostRenameSyncFailure(t *testing.T) {
	for _, failSync := range []bool{false, true} {
		name := "startup_failure_control"
		if failSync {
			name = "post_rename_sync_failure"
		}
		t.Run(name, func(t *testing.T) {
			paths, req, before := expiredFunnelShareFixture(t)
			restoreShareSeams(t)
			shareIsRunningFn = func(string) bool { return false }
			startupCalls := 0
			shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
				startupCalls++
				return shareDaemonStart{}, errors.New("synthetic startup failure")
			}
			syncCalls := 0
			syncErr := errors.New("synthetic directory sync failure")
			if failSync {
				t.Cleanup(atomicfile.SetDirectorySyncForTest(func(string) error {
					syncCalls++
					if syncCalls == 1 {
						return syncErr
					}
					return nil
				}))
			}
			_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
			if err == nil {
				t.Fatal("failure not reported")
			}
			if failSync && !errors.Is(err, syncErr) {
				t.Fatalf("wrong error: %v", err)
			}
			reg, loadErr := registry.Load(paths.Registry)
			if loadErr != nil || len(reg.Services) != 1 {
				t.Fatalf("registry: %+v %v", reg, loadErr)
			}
			if !reflect.DeepEqual(reg.Services[0], before) {
				t.Fatalf("failed rearm persisted: %+v; sync=%d startup=%d", reg.Services[0], syncCalls, startupCalls)
			}
			if failSync && (syncCalls < 2 || startupCalls != 0) {
				t.Fatalf("post-rename failure did not compensate before startup: sync=%d startup=%d", syncCalls, startupCalls)
			}
		})
	}
}

func TestSharePostRenameConflictAndPartialReport(t *testing.T) {
	for _, mode := range []string{"concurrent_edit", "concurrent_delete", "rollback_error"} {
		t.Run(mode, func(t *testing.T) {
			paths, req, _ := expiredFunnelShareFixture(t)
			restoreShareSeams(t)
			shareIsRunningFn = func(string) bool { return false }
			shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
				t.Fatal("startup after persistence failure")
				return shareDaemonStart{}, nil
			}
			syncErr := errors.New("synthetic post-rename directory sync failure")
			calls := 0
			t.Cleanup(atomicfile.SetDirectorySyncForTest(func(string) error {
				calls++
				if calls == 1 {
					return syncErr
				}
				return nil
			}))
			actualReplace := shareReplaceIfUnchangedFn
			shareReplaceIfUnchangedFn = func(path string, expected, replacement registry.Service) (bool, error) {
				switch mode {
				case "concurrent_edit":
					_, err := registry.MutateService(path, expected.Name, func(s registry.Service) (registry.Service, error) { s.Tags = []string{"tag:concurrent"}; return s, nil })
					if err != nil {
						t.Fatal(err)
					}
				case "concurrent_delete":
					if _, err := registry.Remove(path, expected.Name); err != nil {
						t.Fatal(err)
					}
				case "rollback_error":
					return false, errors.New("synthetic compensation failure")
				}
				return actualReplace(path, expected, replacement)
			}
			_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "public authorization") {
				t.Fatalf("missing explicit partial/conflict report: %v", err)
			}
			reg, loadErr := registry.Load(paths.Registry)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			switch mode {
			case "concurrent_edit":
				if len(reg.Services) != 1 || !reflect.DeepEqual(reg.Services[0].Tags, []string{"tag:concurrent"}) {
					t.Fatalf("concurrent edit overwritten: %+v", reg.Services)
				}
			case "concurrent_delete":
				if len(reg.Services) != 0 {
					t.Fatalf("deleted share resurrected: %+v", reg.Services)
				}
			case "rollback_error":
				if len(reg.Services) != 1 || !reg.Services[0].Funnel {
					t.Fatalf("partial state fixture absent: %+v", reg.Services)
				}
			}
		})
	}
}

func TestShareNewRegistrationCompensatesPostRenameSyncFailure(t *testing.T) {
	restoreShareSeams(t)
	paths := sharePaths{Registry: filepath.Join(t.TempDir(), "registry.json"), PID: filepath.Join(t.TempDir(), "missing.pid")}
	req := shareRequest{Target: "3000", Ephemeral: true}
	shareIsRunningFn = func(string) bool { return false }
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		t.Fatal("startup after persistence failure")
		return shareDaemonStart{}, nil
	}
	syncErr := errors.New("synthetic post-rename directory sync failure")
	calls := 0
	t.Cleanup(atomicfile.SetDirectorySyncForTest(func(string) error {
		calls++
		if calls == 1 {
			return syncErr
		}
		return nil
	}))
	_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
	if !errors.Is(err, syncErr) {
		t.Fatalf("wrong failure: %v", err)
	}
	reg, loadErr := registry.Load(paths.Registry)
	if loadErr != nil || len(reg.Services) != 0 || calls < 2 {
		t.Fatalf("new share was not compensated: %+v loadErr=%v syncCalls=%d", reg.Services, loadErr, calls)
	}
}

func TestStatusDeadlineCrossingNormalizesFunnel(t *testing.T) {
	for _, cross := range []bool{false, true} {
		name := "before_deadline_control"
		if cross {
			name = "crosses_deadline"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			regPath, pidPath, snapshotPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json")
			deadline := time.Now().Add(time.Hour)
			started := deadline.Add(-time.Hour)
			svc := addStatusTestService(t, regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline})
			snapshot := tsruntime.NewSnapshot(4242, started, statusRegistryFingerprint(t, regPath), deadline.Add(-time.Second), []tsruntime.ServiceState{{Service: svc, RuntimeHost: "public.tailnet.ts.net."}})
			if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
				t.Fatal(err)
			}
			withStatusURLSeams(t, true, 4242, started)
			oldNow := statusNowFn
			t.Cleanup(func() { statusNowFn = oldNow })
			calls := 0
			statusNowFn = func() time.Time {
				calls++
				if cross && calls >= 3 {
					return deadline
				}
				return deadline.Add(-time.Nanosecond)
			}
			ordinary, err := getPollableStatus(pidPath, regPath, snapshotPath, filepath.Join(dir, "handoff.json"))
			if err != nil {
				t.Fatal(err)
			}
			urls, err := getStatusURLs(pidPath, regPath, snapshotPath)
			if err != nil {
				t.Fatal(err)
			}
			got, url := ordinary.Services[0], findStatusService(t, urls, "public")
			if cross {
				if got.FunnelRequested || got.FunnelActive || got.FunnelState != tsruntime.FunnelStateNotRequested || got.FunnelRemaining == nil || *got.FunnelRemaining != "0s" {
					t.Fatalf("ordinary status after expiry: %+v", got)
				}
				if url.FunnelRequested || url.FunnelActive || url.FunnelState != tsruntime.FunnelStateNotRequested {
					t.Fatalf("URL status after expiry: %+v", url)
				}
			} else if !got.FunnelRequested || !got.FunnelActive || !url.FunnelRequested || !url.FunnelActive {
				t.Fatalf("positive control: %+v %+v", got, url)
			}
		})
	}
}

func TestActualServeParserContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"canonical", []string{"serve", "--no-browser"}, true},
		{"leading_global_false", []string{"--json=false", "serve"}, true},
		{"leading_local_bool_equals", []string{"--no-browser=true", "serve"}, true},
		{"leading_local_string", []string{"--control-url=http://localhost:3000", "serve"}, true},
		{"leading_false_local", []string{"--no-browser=false", "serve"}, true},
		{"value_contains_serve", []string{"--control-url=serve", "serve"}, true},
		{"trailing_value_contains_serve", []string{"serve", "--control-url=serve"}, true},
		{"local_bool_separate", []string{"--no-browser", "serve"}, false},
		{"invalid_bool", []string{"serve", "--json=serve"}, false},
		{"invalid_flag", []string{"serve", "--bogus"}, false},
		{"invalid_positionals", []string{"serve", "status"}, false},
		{"value_is_serve", []string{"--control-url", "serve", "status"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command, remaining, err := rootCmd.Find(tc.args)
			if command != nil {
				command.Flags().VisitAll(func(f *pflag.Flag) {
					value, changed := f.Value.String(), f.Changed
					t.Cleanup(func() { _ = f.Value.Set(value); f.Changed = changed })
				})
			}
			accepted := false
			if err == nil && command != nil && command.Name() == "serve" {
				err = command.ParseFlags(remaining)
				if err == nil {
					err = command.ValidateArgs(command.Flags().Args())
				}
				accepted = err == nil
			}
			if accepted != tc.want {
				t.Fatalf("Cobra oracle changed: accepted=%t want=%t err=%v", accepted, tc.want, err)
			}
			if got := cliargs.IsRunnableServe(tc.args); got != tc.want {
				t.Fatalf("identity grammar disagrees with CLI: got=%t want=%t", got, tc.want)
			}
		})
	}
}

func TestCobraHelpAndVersionNeverRunServe(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		runs    bool
		wantErr bool
	}{
		{"runnable_control", []string{"serve", "--no-browser"}, true, false},
		{"help", []string{"serve", "--help"}, false, false},
		{"short_help", []string{"serve", "-h"}, false, false},
		{"version", []string{"serve", "--version"}, false, true},
		{"leading_version", []string{"--version", "serve"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := &cobra.Command{Use: "tslink", Version: "test-version"}
			serve := &cobra.Command{Use: "serve", Args: cobra.NoArgs}
			cliargs.RegisterRootFlags(root)
			var daemon bool
			cliargs.RegisterServeFlags(serve, &daemon)
			runs := false
			serve.RunE = func(*cobra.Command, []string) error { runs = true; return nil }
			root.AddCommand(serve)
			root.SetArgs(tc.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "unknown flag: --version") {
					t.Fatalf("Cobra trailing version error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if runs != tc.runs {
				t.Fatalf("Cobra executed serve=%t want=%t", runs, tc.runs)
			}
			if got := cliargs.IsRunnableServe(tc.args); got != tc.runs {
				t.Fatalf("identity runnable=%t want=%t", got, tc.runs)
			}
		})
	}
}
