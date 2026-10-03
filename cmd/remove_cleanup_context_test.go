package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// blockingDeleteDevices stands in for a Tailscale API that accepted the
// connection and never answers: it returns only when its context is done, or
// after a safety bound that the tests treat as "cancellation never arrived".
func blockingDeleteDevices(t *testing.T) (started, cancelled chan struct{}) {
	t.Helper()
	started, cancelled = make(chan struct{}, 1), make(chan struct{}, 1)
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	deleteDevicesFn = func(ctx context.Context, _ tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
			cancelled <- struct{}{}
			return tailapi.CleanupResult{}, ctx.Err()
		case <-time.After(5 * time.Second):
			return tailapi.CleanupResult{}, errors.New("cleanup was never cancelled")
		}
	}
	return started, cancelled
}

func registerRemovableService(t *testing.T) (regPath, ownershipPath string) {
	t.Helper()
	dir := t.TempDir()
	regPath = filepath.Join(dir, "registry.json")
	if _, err := registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	return regPath, filepath.Join(dir, "node-ownership.json")
}

func TestRemoveDeviceCleanupStopsWhenItsContextIsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		regPath, ownershipPath := registerRemovableService(t)
		started, cancelled := blockingDeleteDevices(t)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-started
			cancel()
		}()
		begin := time.Now()
		result, err := removeServiceResultContext(ctx, regPath, ownershipPath, "web")
		if err != nil {
			t.Fatalf("remove error = %v", err)
		}
		select {
		case <-cancelled:
		default:
			t.Fatal("cleanup did not observe request cancellation")
		}
		if time.Since(begin) != 0 {
			t.Fatal("cleanup waited after cancellation")
		}

		if !result.Removed || !strings.Contains(result.DeviceWarning, context.Canceled.Error()) {
			t.Fatalf("result = %+v, want the service removed and the cancelled cleanup reported", result)
		}
	})
}

// The MCP unshare tool must hand the request's context to device cleanup, so
// that ending the session, by the caller or by the post-EOF watchdog, reaches
// a cleanup call stuck on the Tailscale API.
func TestMCPUnshareDeviceCleanupSeesSessionCancellation(t *testing.T) {
	call := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"unshare","arguments":{"name":"web"}}}`)

	t.Run("caller cancels", func(t *testing.T) {
		regPath, ownershipPath := registerRemovableService(t)
		started, cancelled := blockingDeleteDevices(t)
		actions := defaultMCPActions(sharePaths{Registry: regPath, Ownership: ownershipPath}, io.Discard)
		inReader, inWriter := io.Pipe()
		defer inWriter.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- runMCPStdio(ctx, inReader, io.Discard, actions) }()
		go func() { _, _ = io.WriteString(inWriter, call) }()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("unshare never reached device cleanup")
		}
		cancel()
		select {
		case <-cancelled:
		case <-time.After(2 * time.Second):
			t.Fatal("device cleanup did not see the session's cancellation")
		}
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("session error = %v", err)
		}
	})

	t.Run("post-EOF watchdog", func(t *testing.T) {
		withMCPEOFWatchdog(t, 300*time.Millisecond, 2*time.Second)
		regPath, ownershipPath := registerRemovableService(t)
		_, cancelled := blockingDeleteDevices(t)
		actions := defaultMCPActions(sharePaths{Registry: regPath, Ownership: ownershipPath}, os.Stderr)
		var stdout bytes.Buffer
		err := runMCPStdio(context.Background(), strings.NewReader(call), &stdout, actions)
		if !errors.Is(err, errMCPEOFWatchdog) {
			t.Fatalf("session error = %v, want the post-EOF watchdog", err)
		}
		select {
		case <-cancelled:
		default:
			t.Fatal("the watchdog did not reach the stuck device cleanup")
		}
	})
}
