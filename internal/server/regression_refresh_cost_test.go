package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"tailscale.com/ipn/ipnstate"
	"testing"
	"time"
)

func TestReReviewMeasureExpiryRefreshCost(t *testing.T) {
	for _, n := range []int{32, 128, 512} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, dir := healthTestServer(t)
			now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
			expires := now.Add(30 * 24 * time.Hour)
			data, _ := json.Marshal(ipnstate.Status{Self: &ipnstate.PeerStatus{KeyExpiry: &expires}})
			var reads atomic.Int32
			r := health.NewRecorder(filepath.Join(dir, health.StateFile), health.NotifierConfig{})
			s.healthStates = map[string]serviceHealth{}
			for i := 0; i < n; i++ {
				svc := registry.Service{Name: fmt.Sprintf("app%04d", i), Type: registry.TypeProxy, Target: "http://localhost:1234", Health: &registry.HealthConfig{Interval: "1d"}}
				lc := localapitest.NewClient(localapitest.RoundTripFunc(func(*http.Request) (*http.Response, error) {
					reads.Add(1)
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
				}))
				node := &ServiceNode{service: svc, tsnetSrv: &fakeTSNetServer{localClient: lc}}
				s.nodes[svc.Name] = node
				h := health.State{State: health.Healthy, Kind: svc.Type, LastChecked: &now}
				r.ObserveHealth(svc.Name, healthIdentity(svc), h, now)
				s.healthStates[svc.Name] = serviceHealth{Identity: healthIdentity(svc), Health: h, NodeKey: health.ExpiryAt(&expires, "localclient", now, nil), Node: node, NodeChecked: now}
			}
			r.Commit(context.Background(), nil, now)
			journalWrites := 0
			oldWrite := r.WriteFile
			r.WriteFile = func(path string, data []byte) error { journalWrites++; return oldWrite(path, data) }
			writes := 0
			bytes := int64(0)
			oldSave := runtimeSaveSnapshotFn
			runtimeSaveSnapshotFn = func(path string, snapshot tsruntime.Snapshot) error {
				writes++
				b, _ := json.Marshal(snapshot)
				bytes += int64(len(b))
				return oldSave(path, snapshot)
			}
			defer func() { runtimeSaveSnapshotFn = oldSave }()
			var probes atomic.Int32
			start := time.Now()
			s.healthCycle(context.Background(), r, now.Add(time.Minute), func(context.Context, registry.Service) string { probes.Add(1); return "" })
			t.Logf("services=%d expiry_reads=%d backend_probes=%d snapshot_writes=%d snapshot_JSON_bytes=%d journal_writes=%d elapsed=%s", n, reads.Load(), probes.Load(), writes, bytes, journalWrites, time.Since(start))
			if journalWrites > 1 {
				t.Errorf("unchanged expiry journal wrote %d times; want at most one ready batch", journalWrites)
			}
			if writes > 2 {
				t.Errorf("expiry refresh wrote %d full snapshots; want at most two ready batches", writes)
			}
			// A second unchanged refresh updates only the private polling cadence.
			before, err := os.ReadFile(r.Path)
			if err != nil {
				t.Fatal(err)
			}
			writes, journalWrites = 0, 0
			s.healthCycle(context.Background(), r, now.Add(2*time.Minute), func(context.Context, registry.Service) string { probes.Add(1); return "" })
			after, err := os.ReadFile(r.Path)
			if err != nil {
				t.Fatal(err)
			}
			if writes != 0 || journalWrites != 0 || string(before) != string(after) {
				t.Errorf("unchanged refresh wrote snapshot=%d journal=%d", writes, journalWrites)
			}
			if int(reads.Load()) != 2*n || probes.Load() != 0 {
				t.Errorf("refresh fixture did not independently refresh all nodes")
			}
		})
	}
}
