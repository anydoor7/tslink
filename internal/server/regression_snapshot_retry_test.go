package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"tailscale.com/ipn/ipnstate"
)

func TestHealthSnapshotWriteFailureDoesNotLoseDirtyChange(t *testing.T) {
	s, dir := healthTestServer(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	oldExpiry := now.Add(30*24*time.Hour + 23*time.Hour)
	newExpiry := now.Add(2*24*time.Hour + 23*time.Hour)
	var body atomic.Value
	var reads atomic.Int32
	set := func(expires time.Time) {
		b, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &expires}})
		body.Store(string(b))
	}
	set(oldExpiry)
	lc := localapitest.NewClient(localapitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
		reads.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body.Load().(string)))}, nil
	}))
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "1d"}}
	s.nodes[svc.Name] = &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
	probe := func(context.Context, registry.Service) string { return "" }
	s.healthCycle(context.Background(), r, now, probe)
	disk, err := tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil || disk.Services[0].NodeKey.ExpiresAt == nil || !disk.Services[0].NodeKey.ExpiresAt.Equal(oldExpiry) {
		t.Fatal("initial persisted positive control", disk, err)
	}
	realSave := runtimeSaveSnapshotFn
	defer func() { runtimeSaveSnapshotFn = realSave }()
	attempts := 0
	fail := true
	runtimeSaveSnapshotFn = func(path string, snap tsruntime.Snapshot) error {
		attempts++
		if fail {
			return errors.New("review fixture: transient snapshot failure")
		}
		return realSave(path, snap)
	}
	set(newExpiry)
	s.healthCycle(context.Background(), r, now.Add(time.Minute), probe)
	if attempts == 0 || !s.healthStates[svc.Name].NodeKey.ExpiresAt.Equal(newExpiry) {
		t.Fatal("change/failure injection did not execute")
	}
	disk, err = tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil || !disk.Services[0].NodeKey.ExpiresAt.Equal(oldExpiry) {
		t.Fatal("snapshot failure positive control", disk, err)
	}
	failedAttempts := attempts
	fail = false
	for i := 2; i <= 4; i++ {
		s.healthCycle(context.Background(), r, now.Add(time.Duration(i)*time.Minute), probe)
	}
	disk, err = tsruntime.Load(filepath.Join(dir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LocalAPI reads=%d, failed snapshot attempts=%d, recovered-storage attempts=%d; memory expiry=%s, disk expiry=%s", reads.Load(), failedAttempts, attempts-failedAttempts, s.healthStates[svc.Name].NodeKey.ExpiresAt, disk.Services[0].NodeKey.ExpiresAt)
	if attempts == failedAttempts || !disk.Services[0].NodeKey.ExpiresAt.Equal(newExpiry) {
		t.Errorf("real expiry change forgotten after snapshot write failure; unchanged polls never retry publication")
	}
	if failedAttempts != 1 || attempts-failedAttempts != 1 {
		t.Errorf("retry must stay batched and stop after success: failed=%d recovered=%d", failedAttempts, attempts-failedAttempts)
	}
}

func TestHealthSnapshotRetryWithNoDueReads(t *testing.T) {
	for _, failure := range []string{"write", "path"} {
		t.Run(failure, func(t *testing.T) {
			s, dir := healthTestServer(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "1d"}}
			s.nodes[svc.Name] = &ServiceNode{service: svc}
			r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
			path := filepath.Join(dir, "runtime.json")
			// The real atomic writer rejects a directory at the file path.
			if failure == "write" {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			originalPath, originalSave := runtimeSnapshotPathFn, runtimeSaveSnapshotFn
			t.Cleanup(func() { runtimeSnapshotPathFn, runtimeSaveSnapshotFn = originalPath, originalSave })
			blocked := true
			attempts, writes, probes := 0, 0, 0
			runtimeSnapshotPathFn = func() (string, error) {
				attempts++
				if failure == "path" && blocked {
					return "", errors.New("snapshot path unavailable")
				}
				return path, nil
			}
			runtimeSaveSnapshotFn = func(path string, snap tsruntime.Snapshot) error {
				writes++
				return originalSave(path, snap)
			}
			probe := func(context.Context, registry.Service) string { probes++; return "" }
			s.healthCycle(context.Background(), r, now, probe)
			if attempts != 1 || r.Previous(svc.Name, healthIdentity(svc)).State != health.Healthy {
				t.Fatal("failed publication/real observation control", attempts, r.State)
			}
			s.healthCycle(context.Background(), r, now.Add(10*time.Second), probe)
			if attempts != 2 {
				t.Errorf("persistent failure must retry once on the next idle cycle: attempts=%d", attempts)
			}
			blocked = false
			if failure == "write" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			s.healthCycle(context.Background(), r, now.Add(20*time.Second), probe)
			disk, err := tsruntime.Load(path)
			if err != nil || len(disk.Services) != 1 || disk.Services[0].Health.State != health.Healthy {
				t.Fatal("idle recovery did not persist the original observation", disk, err)
			}
			s.healthCycle(context.Background(), r, now.Add(30*time.Second), probe)
			wantWrites := 3
			if failure == "path" {
				wantWrites = 1
			}
			if attempts != 3 || writes != wantWrites || probes != 1 {
				t.Errorf("idle retry must stop after success: paths=%d writes=%d probes=%d", attempts, writes, probes)
			}
		})
	}
}
