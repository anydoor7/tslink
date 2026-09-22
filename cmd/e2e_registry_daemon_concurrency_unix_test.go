//go:build !windows

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// Concurrent registry mutation while a daemon owns the config directory.
//
// internal/registry already proves 12-process concurrent Add() converges
// exactly (TestAddConcurrentProcessesPreservesExactRegistry). This scenario
// deliberately does not repeat that. What it adds is the half that test cannot
// construct: a live daemon holding the PID file and a runtime snapshot in the
// same directory while independent CLI processes mutate the registry.
//
// The concrete risks that only exist with a daemon present:
//   - a CLI mutation clobbering or truncating the daemon's runtime.json
//   - a CLI mutation disturbing the daemon's PID or identity evidence, which is
//     the entry condition for the double-daemon incidents
//   - a concurrent reader observing a torn registry.json mid-write
//   - `status` failing or emitting a non-envelope while writes are in flight
//
// SCOPE LIMIT, stated plainly: the brief also asks for "hot reload converges"
// and "no service is stuck half-started". Those are properties of a real serve
// daemon reconciling tsnet nodes, and a real serve daemon requires the control
// plane. The fake daemon here does not reload anything, so this scenario makes
// no claim about hot reload. See report §7.
func TestE2ERegistryConcurrencyWithDaemonPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level e2e: forks real daemons")
	}
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	daemonBinary := e2eFakeDaemonBinary(t)

	regPath := filepath.Join(configDir, "registry.json")
	const keepCount, dropCount, addCount = 4, 4, 8
	for i := 0; i < keepCount; i++ {
		seedConcurrencyService(t, regPath, fmt.Sprintf("keep-%02d", i), 3000+i)
	}
	for i := 0; i < dropCount; i++ {
		seedConcurrencyService(t, regPath, fmt.Sprintf("drop-%02d", i), 3100+i)
	}

	handle := e2eStartFakeDaemon(t, configDir)
	pidPath := filepath.Join(configDir, "tslink.pid")
	identityPath := pidPath + ".identity"

	// Give the daemon a runtime snapshot, the way a real serve daemon would.
	// Its bytes are the canary for "did a CLI mutation stomp daemon state".
	runtimePath := filepath.Join(configDir, "runtime.json")
	writeConcurrencyRuntimeSnapshot(t, regPath, runtimePath, handle.PID, pidPath)
	runtimeBefore, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatalf("read runtime snapshot: %v", err)
	}
	identityBefore, err := os.ReadFile(identityPath)
	if err != nil {
		t.Fatalf("read identity sidecar: %v", err)
	}

	// Concurrent readers run for the whole mutation window and fail the test if
	// they ever observe a registry that does not parse.
	stopReaders := make(chan struct{})
	var readerWG sync.WaitGroup
	var tornReads, readSamples, statusSamples atomic.Int64
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
			}
			data, readErr := os.ReadFile(regPath)
			if readErr != nil {
				if !os.IsNotExist(readErr) {
					tornReads.Add(1)
				}
				continue
			}
			readSamples.Add(1)
			var probe registry.Registry
			if json.Unmarshal(data, &probe) != nil {
				tornReads.Add(1)
			}
		}
	}()

	statusFailures := make(chan string, 8)
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
			}
			run := e2eRunBinary(t, binary, configDir, "", e2eEnv(configDir), "status", "--json")
			statusSamples.Add(1)
			if run.ExitCode != output.ExitSuccess || run.Stderr != "" {
				select {
				case statusFailures <- fmt.Sprintf("status exit=%d stderr=%q stdout=%s", run.ExitCode, run.Stderr, run.Stdout):
				default:
				}
				return
			}
			if results := parseCompiledJSONLines(t, run.Stdout); len(results) != 1 {
				select {
				case statusFailures <- fmt.Sprintf("status emitted %d envelopes", len(results)):
				default:
				}
				return
			}
		}
	}()

	// Interleave adds and removes across independent processes.
	type mutation struct {
		args []string
		desc string
	}
	mutations := make([]mutation, 0, addCount+dropCount)
	for i := 0; i < addCount; i++ {
		name := fmt.Sprintf("new-%02d", i)
		mutations = append(mutations, mutation{
			args: []string{"add", name, "--proxy", fmt.Sprintf("localhost:%d", 3200+i), "--json"},
			desc: "add " + name,
		})
	}
	for i := 0; i < dropCount; i++ {
		name := fmt.Sprintf("drop-%02d", i)
		mutations = append(mutations, mutation{
			args: []string{"remove", name, "--json"},
			desc: "remove " + name,
		})
	}

	failures := make(chan string, len(mutations))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, m := range mutations {
		wg.Add(1)
		go func(m mutation) {
			defer wg.Done()
			<-start
			run := e2eRunBinary(t, binary, configDir, "", e2eEnv(configDir), m.args...)
			if run.ExitCode != output.ExitSuccess || run.Stderr != "" {
				failures <- fmt.Sprintf("%s: exit=%d stderr=%q stdout=%s", m.desc, run.ExitCode, run.Stderr, run.Stdout)
				return
			}
			if results := parseCompiledJSONLines(t, run.Stdout); len(results) != 1 || !results[0].OK {
				failures <- fmt.Sprintf("%s: envelope=%+v", m.desc, results)
			}
		}(m)
	}
	close(start)
	wg.Wait()
	close(stopReaders)
	readerWG.Wait()
	close(failures)
	close(statusFailures)

	for f := range failures {
		t.Errorf("concurrent mutation failed: %s", f)
	}
	for f := range statusFailures {
		t.Errorf("status during concurrent mutation: %s", f)
	}
	if t.Failed() {
		t.FailNow()
	}

	if got := tornReads.Load(); got != 0 {
		t.Fatalf("observed %d unparseable registry states across %d concurrent reads; writes are not atomic",
			got, readSamples.Load())
	}
	if readSamples.Load() == 0 || statusSamples.Load() == 0 {
		t.Fatalf("concurrency probes never sampled (reads=%d status=%d); the scenario proved nothing",
			readSamples.Load(), statusSamples.Load())
	}
	t.Logf("registry reads sampled=%d, status invocations=%d", readSamples.Load(), statusSamples.Load())

	// Exact convergence: keeps survived, drops are gone, adds all landed.
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load final registry: %v", err)
	}
	got := make(map[string]struct{}, len(reg.Services))
	for _, svc := range reg.Services {
		got[svc.Name] = struct{}{}
	}
	want := make(map[string]struct{}, keepCount+addCount)
	for i := 0; i < keepCount; i++ {
		want[fmt.Sprintf("keep-%02d", i)] = struct{}{}
	}
	for i := 0; i < addCount; i++ {
		want[fmt.Sprintf("new-%02d", i)] = struct{}{}
	}
	if len(got) != len(want) {
		t.Fatalf("final service count = %d, want %d; got=%v", len(got), len(want), got)
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Fatalf("service %q missing from converged registry; got=%v", name, got)
		}
	}
	for i := 0; i < dropCount; i++ {
		if _, ok := got[fmt.Sprintf("drop-%02d", i)]; ok {
			t.Fatalf("removed service drop-%02d is still registered; got=%v", i, got)
		}
	}

	// Daemon state must be exactly as the daemon left it.
	if handle.Exited() {
		t.Fatal("daemon exited during concurrent registry mutation")
	}
	pids := e2eAssertProcessCount(t, daemonBinary, 1, "after concurrent mutation")
	if pids[0] != handle.PID {
		t.Fatalf("daemon PID = %d, want unchanged %d", pids[0], handle.PID)
	}
	if currentPID, err := daemon.ReadPID(pidPath); err != nil || currentPID != handle.PID {
		t.Fatalf("PID file = %d (err=%v), want unchanged %d", currentPID, err, handle.PID)
	}
	identityAfter, err := os.ReadFile(identityPath)
	if err != nil || string(identityAfter) != string(identityBefore) {
		t.Fatalf("identity sidecar changed during CLI mutation (err=%v)", err)
	}
	runtimeAfter, err := os.ReadFile(runtimePath)
	if err != nil || string(runtimeAfter) != string(runtimeBefore) {
		t.Fatalf("runtime.json was rewritten by CLI mutations (err=%v)", err)
	}
	if _, err := tsruntime.Load(runtimePath); err != nil {
		t.Fatalf("runtime snapshot no longer loads after concurrent mutation: %v", err)
	}
	e2eAssertProcessCount(t, binary, 0, "no CLI process leaked")
}

func seedConcurrencyService(t *testing.T, regPath, name string, port int) {
	t.Helper()
	if _, err := registry.Add(regPath, registry.Service{
		Name:   name,
		Type:   registry.TypeProxy,
		Target: fmt.Sprintf("http://localhost:%d", port),
	}); err != nil {
		t.Fatalf("seed service %s: %v", name, err)
	}
}

func writeConcurrencyRuntimeSnapshot(t *testing.T, regPath, runtimePath string, pid int, pidPath string) {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry for snapshot: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("fingerprint registry: %v", err)
	}
	info, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("stat pid file: %v", err)
	}
	states := make([]tsruntime.ServiceState, 0, len(reg.Services))
	for _, svc := range reg.Services {
		states = append(states, tsruntime.ServiceState{
			Service:     svc,
			RuntimeHost: svc.Name + ".tailnet-example.ts.net",
		})
	}
	snapshot := tsruntime.NewSnapshot(pid, info.ModTime(), fingerprint, info.ModTime().Add(time.Second), states)
	if err := tsruntime.Save(runtimePath, snapshot); err != nil {
		t.Fatalf("save runtime snapshot: %v", err)
	}
}
