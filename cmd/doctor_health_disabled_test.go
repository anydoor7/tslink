package cmd

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestDoctorSkipsActiveProbeWhenHealthDisabled(t *testing.T) {
	// Real loopback TCP listener for the disabled service: any dial would accept
	// and increment accepted, proving zero network I/O reaches the backend.
	disabledLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer disabledLn.Close()
	var disabledAccepted atomic.Int32
	go func() {
		for {
			conn, err := disabledLn.Accept()
			if err != nil {
				return
			}
			disabledAccepted.Add(1)
			_ = conn.Close()
		}
	}()

	// Control service (probing enabled) proves doctor still probes normal targets.
	enabledLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer enabledLn.Close()
	var enabledAccepted atomic.Int32
	go func() {
		for {
			conn, err := enabledLn.Accept()
			if err != nil {
				return
			}
			enabledAccepted.Add(1)
			_ = conn.Close()
		}
	}()

	var httpProbed []string
	oldHTTP := doctorHTTPProbeFn
	doctorHTTPProbeFn = func(_ context.Context, svc registry.Service) string {
		httpProbed = append(httpProbed, svc.Name)
		return ""
	}
	t.Cleanup(func() { doctorHTTPProbeFn = oldHTTP })

	var loopbackChecked []string
	oldLoopback := doctorLoopbackSharedFn
	doctorLoopbackSharedFn = func(address string) (bool, error) {
		loopbackChecked = append(loopbackChecked, address)
		return false, nil
	}
	t.Cleanup(func() { doctorLoopbackSharedFn = oldLoopback })

	oldProbe := doctorProbeTargetFn
	doctorProbeTargetFn = defaultDoctorProbeTarget
	t.Cleanup(func() { doctorProbeTargetFn = oldProbe })

	result := &DoctorResult{
		canonicalHosts: map[string]string{},
	}
	result.Daemon.Running = true
	opts := doctorOptions{}

	// Disabled TCP, proxy, and file services.
	disabledTCP := registry.Service{
		Name:   "amber-link",
		Type:   registry.TypeTCP,
		Target: disabledLn.Addr().String(),
		Health: &registry.HealthConfig{Disabled: true},
	}
	disabledProxy := registry.Service{
		Name:   "quiet-web",
		Type:   registry.TypeProxy,
		Target: "http://" + disabledLn.Addr().String(),
		Health: &registry.HealthConfig{Disabled: true},
	}
	disabledFile := registry.Service{
		Name:   "quiet-files",
		Type:   registry.TypeFile,
		Path:   "/nonexistent/should-not-stat",
		Health: &registry.HealthConfig{Disabled: true},
	}
	enabledTCP := registry.Service{
		Name:   "active-tcp",
		Type:   registry.TypeTCP,
		Target: enabledLn.Addr().String(),
	}

	diagnoseNetworkTarget(result, disabledTCP, opts, inspect.WarningCodeTCPNonLoopbackTarget)
	diagnoseNetworkTarget(result, disabledProxy, opts, inspect.WarningCodeProxyNonLoopbackTarget)
	diagnoseFileTarget(result, disabledFile)
	diagnoseNetworkTarget(result, enabledTCP, opts, inspect.WarningCodeTCPNonLoopbackTarget)
	result.finalize()

	// Brief yield so any stray accept goroutine would record.
	time.Sleep(20 * time.Millisecond)

	if got := disabledAccepted.Load(); got != 0 {
		t.Fatalf("disabled listener accepted %d connection(s), want 0", got)
	}
	if len(httpProbed) != 0 {
		t.Fatalf("HTTP probe ran for disabled services: %v", httpProbed)
	}
	if got := enabledAccepted.Load(); got != 1 {
		t.Fatalf("control enabled listener accepted %d connection(s), want 1", got)
	}
	if len(loopbackChecked) != 1 || loopbackChecked[0] != enabledLn.Addr().String() {
		t.Fatalf("loopback exclusivity checked %v, want only control [%s]", loopbackChecked, enabledLn.Addr().String())
	}

	if result.HealthStatus != doctorStatusOK || result.HealthExitCode != output.ExitSuccess {
		t.Fatalf("doctor health status=%q exit=%d findings=%+v, want ok/0", result.HealthStatus, result.HealthExitCode, result.Findings)
	}

	skipped := map[string]DoctorFinding{}
	for _, f := range result.Findings {
		if f.Code == inspect.WarningCodeTargetProbeSkippedDisabled {
			skipped[f.Service] = f
		}
	}
	for _, name := range []string{"amber-link", "quiet-web", "quiet-files"} {
		f, ok := skipped[name]
		if !ok {
			t.Fatalf("missing %s finding for %s; findings=%+v", inspect.WarningCodeTargetProbeSkippedDisabled, name, result.Findings)
		}
		if f.Severity != doctorSeverityInfo || f.Area != "target_probe" {
			t.Fatalf("finding for %s = %+v, want severity=info area=target_probe", name, f)
		}
	}
	if _, ok := skipped["active-tcp"]; ok {
		t.Fatalf("control enabled service unexpectedly marked skipped")
	}
}
