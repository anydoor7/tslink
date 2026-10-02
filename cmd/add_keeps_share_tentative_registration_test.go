package cmd

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

// shareWaitingOnItsRegistration starts a share that creates a registration and
// then waits for its URL, and returns once it waits. cancel ends the share, and
// its rollback runs; done yields its error. The share has returned before the
// test's cleanup restores any seam.
func shareWaitingOnItsRegistration(t *testing.T, paths sharePaths, req shareRequest) (cancel func(), done <-chan error) {
	t.Helper()
	shareIsRunningFn = func(string) bool { return true }
	sharePollableStatusFn = func(string, string, string, string) (StatusResult, error) { return StatusResult{}, nil }
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return serviceURLResolution{}, registry.URLNotReadyError(name)
	}
	ctx, cancelShare := context.WithCancel(context.Background())
	shareDone, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		_, err := executeShare(ctx, paths, req, time.Minute, io.Discard)
		shareDone <- err
	}()
	select {
	case <-entered:
	case err := <-shareDone:
		t.Fatalf("the share exited before its URL wait: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the share never reached its URL wait")
	}
	var once atomic.Bool
	cancel = func() {
		if once.CompareAndSwap(false, true) {
			cancelShare()
			close(release)
		}
	}
	t.Cleanup(func() {
		cancel()
		<-finished
	})
	return cancel, shareDone
}

// TestIdenticalAddDuringAShareWaitKeepsTheRegistration is B7-4 (B6a open risk
// 2). While a share that created a registration waits for its URL, tslink add
// with the same definition reports the registration present and unchanged. The
// share's claim on it stayed, so when the share was then cancelled its rollback
// deleted the registration the add had just reported. An add that changes
// nothing now keeps the registration as stored, as a share reusing it does
// (B6a-5), which takes the rollback away. Without the add, the rollback still
// removes what the cancelled share created.
func TestIdenticalAddDuringAShareWaitKeepsTheRegistration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		add          bool
		wantServices int
	}{
		{"identical add", true, 1},
		{"control: no add", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreShareSeams(t)
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
			cancel, shareDone := shareWaitingOnItsRegistration(t, paths, shareRequest{Target: "127.0.0.1:3000", Name: "added-share"})

			if tc.add {
				reg, err := registry.Load(paths.Registry)
				if err != nil || len(reg.Services) != 1 {
					t.Fatalf("control: registry while the share waits = %+v, %v", reg, err)
				}
				// The definition the share registered, as tslink add would build it.
				svc := reg.Services[0]
				svc.CreatedAt = time.Time{}
				result, _, err := executeAdd(context.Background(), svc, paths.Registry, paths.PID, paths.Snapshot, true, 0, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if result.Created || len(result.ReplacedFields) != 0 {
					t.Fatalf("control: add = created %v, replaced %v; want the unchanged path", result.Created, result.ReplacedFields)
				}
			}
			cancel()
			if err := <-shareDone; !errors.Is(err, context.Canceled) {
				t.Fatalf("control: the share = %v; want it cancelled so its rollback runs", err)
			}

			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			if len(reg.Services) != tc.wantServices {
				t.Fatalf("services after the cancelled share's rollback = %+v; want %d", reg.Services, tc.wantServices)
			}
		})
	}
}

// If the waiting share's rollback runs between the add's write and its keep,
// the registration is gone before the add can keep it. The add then reports it
// missing instead of present and unchanged.
func TestIdenticalAddDoesNotReportARegistrationRolledBackBeforeItsKeep(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
	shareWaitingOnItsRegistration(t, paths, shareRequest{Target: "127.0.0.1:3000", Name: "added-share"})
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 1 {
		t.Fatalf("control: registry while the share waits = %+v, %v", reg, err)
	}
	stored := reg.Services[0]

	oldKeep := addKeepIfUnchangedFn
	t.Cleanup(func() { addKeepIfUnchangedFn = oldKeep })
	addKeepIfUnchangedFn = func(path string, expected registry.Service) (bool, error) {
		// The share's rollback, as executeShare runs it, wins the race.
		if removed, err := registry.RemoveIfUnchanged(path, stored); err != nil || !removed {
			t.Errorf("control: the rollback removed = %v, %v; want the registration gone", removed, err)
		}
		return oldKeep(path, expected)
	}
	svc := stored
	svc.CreatedAt = time.Time{}
	result, _, err := executeAdd(context.Background(), svc, paths.Registry, paths.PID, paths.Snapshot, true, 0, time.Now())
	if err == nil || !strings.Contains(err.Error(), "persisted service not found after successful add") {
		t.Fatalf("add = %+v, %v; want it to report the registration missing", result, err)
	}
	if reg, err := registry.Load(paths.Registry); err != nil || len(reg.Services) != 0 {
		t.Fatalf("control: registry after the rollback = %+v, %v; want it empty", reg, err)
	}
}
