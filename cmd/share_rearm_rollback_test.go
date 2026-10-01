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

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func TestFailedShareRearmRestoresExpiredFunnel(t *testing.T) {
	regPath := filepath.Join(t.TempDir(), "registry.json")
	req := shareRequest{Target: "3000", Ephemeral: true, Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true}
	spec, err := inferShareTarget(req.Target, req.Ephemeral)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = applyShareExposure(spec, req)
	if err != nil {
		t.Fatal(err)
	}
	created, err := registerShareWithOutcome(regPath, spec, "")
	if err != nil || !created.Created {
		t.Fatalf("initial share: %+v err=%v", created, err)
	}
	expireShareFunnel(t, regPath, created.Service.Name, true)
	before, err := registry.Load(regPath)
	if err != nil || len(before.Services) != 1 || before.Services[0].Funnel {
		t.Fatalf("expired fixture: %+v err=%v", before, err)
	}
	oldRunning, oldStart := shareIsRunningFn, shareStartDaemonFn
	t.Cleanup(func() { shareIsRunningFn, shareStartDaemonFn = oldRunning, oldStart })
	shareIsRunningFn = func(string) bool { return false }
	startCalls := 0
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		startCalls++
		return shareDaemonStart{}, errors.New("synthetic daemon setup failure")
	}
	paths := sharePaths{Registry: regPath, PID: filepath.Join(t.TempDir(), "missing.pid")}
	control := req
	control.NoDaemonInstall = true
	if _, err := executeShare(context.Background(), paths, control, 0, io.Discard); err == nil {
		t.Fatal("no-install control unexpectedly succeeded")
	}
	controlState, err := registry.Load(regPath)
	if err != nil || controlState.Services[0].Funnel || startCalls != 0 {
		t.Fatalf("control mutated registry or started daemon: %+v err=%v calls=%d", controlState, err, startCalls)
	}
	if _, err := executeShare(context.Background(), paths, req, time.Millisecond, io.Discard); err == nil {
		t.Fatal("synthetic setup failure was not returned")
	}
	if startCalls != 1 {
		t.Fatalf("start calls=%d, want 1", startCalls)
	}
	after, err := registry.Load(regPath)
	if err != nil || len(after.Services) != 1 {
		t.Fatalf("after failed share: %+v err=%v", after, err)
	}
	if after.Services[0].Funnel || after.Services[0].FunnelExpiresAt == nil || !after.Services[0].FunnelExpiresAt.Before(time.Now()) {
		t.Fatalf("failed share left public Funnel rearmed: %+v", after.Services[0])
	}
}

func expiredFunnelShareFixture(t *testing.T) (sharePaths, shareRequest, registry.Service) {
	t.Helper()
	dir := t.TempDir()
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "missing.pid")}
	req := shareRequest{Target: "3000", Ephemeral: true, Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true}
	spec, err := inferShareTarget(req.Target, req.Ephemeral)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = applyShareExposure(spec, req)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := registerShareWithOutcome(paths.Registry, spec, "")
	if err != nil || !registered.Created {
		t.Fatalf("register: %+v err=%v", registered, err)
	}
	expireShareFunnel(t, paths.Registry, registered.Service.Name, true)
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Funnel {
		t.Fatalf("expired fixture: %+v err=%v", reg, err)
	}
	return paths, req, reg.Services[0]
}

func TestShareRearmOutcomeAndRollbackBoundaries(t *testing.T) {
	t.Run("URL_wait_failure_restores", func(t *testing.T) {
		paths, req, before := expiredFunnelShareFixture(t)
		restoreShareSeams(t)
		shareIsRunningFn = func(string) bool { return true }
		shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		sharePollableStatusFn = func(_, _, _, _ string) (StatusResult, error) { return StatusResult{}, nil }
		_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
		if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
			t.Fatalf("wait error=%v", err)
		}
		reg, loadErr := registry.Load(paths.Registry)
		if loadErr != nil || len(reg.Services) != 1 || !reflect.DeepEqual(reg.Services[0], before) {
			t.Fatalf("URL failure did not restore prior service: %+v err=%v", reg, loadErr)
		}
	})
	t.Run("successful_rearm_keeps_new_deadline", func(t *testing.T) {
		paths, req, before := expiredFunnelShareFixture(t)
		restoreShareSeams(t)
		shareIsRunningFn = func(string) bool { return false }
		shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
			return shareDaemonStart{Status: authStatusNeedsLogin, AuthURL: "https://login.tailscale.com/a/fake"}, nil
		}
		result, err := executeShare(context.Background(), paths, req, 0, io.Discard)
		if err != nil || !result.FunnelRearmed || result.Status != authStatusNeedsLogin {
			t.Fatalf("success result=%+v err=%v", result, err)
		}
		reg, loadErr := registry.Load(paths.Registry)
		if loadErr != nil || len(reg.Services) != 1 || !reg.Services[0].Funnel || !reg.Services[0].FunnelExpiresAt.After(*before.FunnelExpiresAt) {
			t.Fatalf("successful rearm lost: %+v err=%v", reg, loadErr)
		}
	})
	t.Run("concurrent_edit_kept_and_reported", func(t *testing.T) {
		paths, req, _ := expiredFunnelShareFixture(t)
		restoreShareSeams(t)
		shareIsRunningFn = func(string) bool { return false }
		shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
			_, mutationErr := registry.MutateService(paths.Registry, "port-3000", func(svc registry.Service) (registry.Service, error) {
				svc.Tags = []string{"tag:concurrent"}
				return svc, nil
			})
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			return shareDaemonStart{}, errors.New("synthetic startup failure")
		}
		_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
		if err == nil || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "changed concurrently") || !strings.Contains(err.Error(), "synthetic startup failure") {
			t.Fatalf("conflict not explicit: %v", err)
		}
		reg, loadErr := registry.Load(paths.Registry)
		if loadErr != nil || len(reg.Services) != 1 || !reg.Services[0].Funnel || !reflect.DeepEqual(reg.Services[0].Tags, []string{"tag:concurrent"}) {
			t.Fatalf("concurrent edit overwritten: %+v err=%v", reg, loadErr)
		}
	})
	t.Run("rollback_error_reports_possible_public_state", func(t *testing.T) {
		paths, req, _ := expiredFunnelShareFixture(t)
		restoreShareSeams(t)
		shareIsRunningFn = func(string) bool { return false }
		shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
			return shareDaemonStart{}, errors.New("synthetic startup failure")
		}
		shareReplaceIfUnchangedFn = func(string, registry.Service, registry.Service) (bool, error) {
			return false, errors.New("synthetic registry write failure")
		}
		_, err := executeShare(context.Background(), paths, req, 0, io.Discard)
		if err == nil || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "public authorization may remain enabled") || !strings.Contains(err.Error(), "synthetic registry write failure") {
			t.Fatalf("partial state not reported: %v", err)
		}
	})
}
