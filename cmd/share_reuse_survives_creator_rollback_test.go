package cmd

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

// B6a-5 (audit X4-5). Share A creates a registration and waits for its URL.
// Identical share B reuses that registration and returns ready. A is then
// cancelled, and its rollback used to delete the registration B had just
// reported ready, because B's reuse left nothing a content comparison could
// see. Reuse now keeps the registration under the registry lock, which takes
// A's rollback away.
func TestShareReusedByAnIdenticalCallSurvivesTheCreatorsCancellation(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json")}
	shareIsRunningFn = func(string) bool { return true }
	sharePollableStatusFn = func(string, string, string, string) (StatusResult, error) { return StatusResult{}, nil }
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		}
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://share.example.invalid", State: "exact"}}, nil
	}
	req := shareRequest{Target: "127.0.0.1:3000", Name: "replayed-share"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstDone := make(chan error, 1)
	go func() { _, err := executeShare(ctx, paths, req, time.Minute, io.Discard); firstDone <- err }()
	select {
	case <-entered:
	case err := <-firstDone:
		t.Fatalf("first share exited before its URL wait: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first share never reached its URL wait")
	}

	second, secondErr := executeShare(context.Background(), paths, req, time.Second, io.Discard)
	cancel()
	close(release)
	firstErr := <-firstDone

	if secondErr != nil || second.Status != shareStatusReady {
		t.Fatalf("control: the replay = %+v, %v; want ready", second, secondErr)
	}
	if !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("control: the first share = %v; want it cancelled so its rollback runs", firstErr)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Name != req.Name {
		t.Fatalf("services after the first share's rollback = %+v; want the service the replay reported ready", reg.Services)
	}
}

// A share that succeeds settles the registration it created: a rollback that
// ran for it later, with the very service it created, removes nothing.
func TestShareThatSucceedsSettlesTheRegistrationItCreated(t *testing.T) {
	restoreShareSeams(t)
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json")}
	shareIsRunningFn = func(string) bool { return true }
	shareResolveEndpointOnceFn = func(_, _, _, name string) (serviceURLResolution, error) {
		return serviceURLResolution{Result: URLResult{Name: name, URL: "https://share.example.invalid", State: "exact"}}, nil
	}
	var created registry.Service
	shareAddIfMissingFn = func(path string, svc registry.Service) (bool, error) {
		made, err := registry.AddTentative(path, svc)
		created = svc
		return made, err
	}

	result, err := executeShare(context.Background(), paths, shareRequest{Target: "127.0.0.1:3000", Name: "settled-share"}, time.Second, io.Discard)
	if err != nil || result.Status != shareStatusReady {
		t.Fatalf("executeShare() = %+v, %v", result, err)
	}
	if created.Name != "settled-share" {
		t.Fatalf("control: the share did not create its service through the seam: %+v", created)
	}
	removed, err := registry.RemoveIfUnchanged(paths.Registry, created)
	if err != nil || removed {
		t.Fatalf("late rollback of a successful share = %v, %v; want nothing removed", removed, err)
	}
}
