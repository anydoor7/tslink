package cmd

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// Retention of a share's registration starts with the grant phase. A failure
// to settle the tentative registration before the first grant side effect
// leaves nothing to retain: the registration this call created is rolled back.
func TestShareRollsBackTentativeRegistrationWhenKeepFailsBeforeGrants(t *testing.T) {
	dir := t.TempDir()
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "missing.pid")}
	restoreShareSeams(t)
	shareIsRunningFn = func(string) bool { return false }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	registered := 0
	shareStartDaemonFn = func(context.Context, io.Writer) (shareDaemonStart, error) {
		// Positive control: the probe below can see this call's registration.
		reg, err := registry.Load(paths.Registry)
		if err != nil {
			t.Fatal(err)
		}
		registered = len(reg.Services)
		// The request ends once the daemon is up, before any grant begins, so
		// settling the tentative registration fails.
		cancel()
		return shareDaemonStart{}, nil
	}
	_, err := executeShare(ctx, paths, shareRequest{Target: "3000", Ephemeral: true, Allow: []string{"alice@example.com"}}, 0, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("share error = %v, want the cancellation that stopped the keep", err)
	}
	if registered != 1 {
		t.Fatalf("share registered %d services before the daemon start, want 1", registered)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil || len(reg.Services) != 0 {
		t.Fatalf("tentative registration survived a failure before any grant: %+v, %v", reg.Services, err)
	}
}
