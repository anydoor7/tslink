package cmd

import (
	"context"
	"net"
	"runtime"
	"strconv"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// A backend on a wildcard address lets another local process bind 127.0.0.1
// on the same port and take the connections TSLink proxies to it (the W0220
// incident: a bench job took over a dashboard this way). Doctor reports that
// as backend_loopback_shared, naming the service and port. These tests use the
// real bind probe against real listeners.

// wildcardSharesLoopback is the platform fact the probe measures: macOS and
// Windows let a specific bind sit beside a wildcard listener, Linux refuses it.
func wildcardSharesLoopback() bool { return runtime.GOOS != "linux" }

func listenForLoopbackTest(t *testing.T, address string) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen %s: %v", address, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
}

func TestDefaultDoctorLoopbackSharedMeasuresTheSecondBind(t *testing.T) {
	_, wildPort := listenForLoopbackTest(t, ":0")
	shared, err := defaultDoctorLoopbackShared(net.JoinHostPort("127.0.0.1", wildPort))
	if err != nil {
		t.Fatalf("probe beside a wildcard listener: %v", err)
	}
	if shared != wildcardSharesLoopback() {
		t.Fatalf("wildcard listener: shared = %v, want %v on %s", shared, wildcardSharesLoopback(), runtime.GOOS)
	}

	_, loopPort := listenForLoopbackTest(t, "127.0.0.1:0")
	shared, err = defaultDoctorLoopbackShared(net.JoinHostPort("127.0.0.1", loopPort))
	if err != nil {
		t.Fatalf("probe beside a 127.0.0.1 listener: %v", err)
	}
	if shared {
		t.Fatal("127.0.0.1 listener: shared = true, want false (the second bind must fail with EADDRINUSE)")
	}
}

func TestDefaultDoctorLoopbackSharedReleasesItsBind(t *testing.T) {
	ln, port := listenForLoopbackTest(t, "127.0.0.1:0")
	_ = ln.Close()
	address := net.JoinHostPort("127.0.0.1", port)
	if shared, err := defaultDoctorLoopbackShared(address); err != nil || !shared {
		t.Fatalf("free address: shared=%v err=%v, want true, nil", shared, err)
	}
	// The probe closed its listener: the backend can still bind the address.
	again, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("rebind after probe: %v", err)
	}
	_ = again.Close()
}

func TestLoopbackBindAddress(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"localhost", "127.0.0.1:8080"},
		{"", "127.0.0.1:8080"},
		{"0.0.0.0", "127.0.0.1:8080"},
		{"127.0.0.1", "127.0.0.1:8080"},
		{"::1", "[::1]:8080"},
		{"192.168.1.5", ""},
	} {
		if got := loopbackBindAddress(doctorTarget{Host: tc.host, Port: "8080"}); got != tc.want {
			t.Errorf("loopbackBindAddress(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}
}

func doctorLoopbackFindings(t *testing.T, target string) []DoctorFinding {
	t.Helper()
	oldShared, oldDial, oldHTTP := doctorLoopbackSharedFn, doctorProbeTargetFn, doctorHTTPProbeFn
	t.Cleanup(func() { doctorLoopbackSharedFn, doctorProbeTargetFn, doctorHTTPProbeFn = oldShared, oldDial, oldHTTP })
	doctorLoopbackSharedFn = defaultDoctorLoopbackShared
	doctorProbeTargetFn = defaultDoctorProbeTarget
	doctorHTTPProbeFn = func(context.Context, registry.Service) string { return "" }

	result := DoctorResult{Findings: []DoctorFinding{}, canonicalHosts: map[string]string{}}
	result.Daemon.Running = true
	svc := registry.Service{Name: "web", Type: registry.TypeProxy, Target: target}
	diagnoseNetworkTarget(&result, svc, doctorOptions{}, inspect.WarningCodeProxyNonLoopbackTarget)
	return result.Findings
}

func findDoctorCode(findings []DoctorFinding, code string) *DoctorFinding {
	for i := range findings {
		if findings[i].Code == code {
			return &findings[i]
		}
	}
	return nil
}

func TestDoctorReportsWildcardBackendAsLoopbackShared(t *testing.T) {
	_, port := listenForLoopbackTest(t, ":0")
	findings := doctorLoopbackFindings(t, "http://localhost:"+port)
	got := findDoctorCode(findings, inspect.WarningCodeBackendLoopbackShared)
	if !wildcardSharesLoopback() {
		if got != nil {
			t.Fatalf("%s refuses the second bind, yet doctor reported %+v", runtime.GOOS, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("no %s finding for a wildcard backend; findings: %+v", inspect.WarningCodeBackendLoopbackShared, findings)
	}
	if got.Service != "web" || got.Severity != doctorSeverityWarning || got.Evidence["port"] != port ||
		got.Evidence["bind_probe"] != "127.0.0.1:"+port {
		t.Fatalf("finding = %+v, want service web, warning, port %s, bind_probe 127.0.0.1:%s", *got, port, port)
	}
}

func TestDoctorAcceptsExclusiveLoopbackBackend(t *testing.T) {
	_, port := listenForLoopbackTest(t, "127.0.0.1:0")
	findings := doctorLoopbackFindings(t, "http://127.0.0.1:"+port)
	if got := findDoctorCode(findings, inspect.WarningCodeBackendLoopbackShared); got != nil {
		t.Fatalf("127.0.0.1 backend reported as shared: %+v", *got)
	}
	if got := findDoctorCode(findings, inspect.WarningCodeBackendLoopbackProbeFailed); got != nil {
		t.Fatalf("probe failed beside a 127.0.0.1 backend: %+v", *got)
	}
}

func TestDoctorSkipsLoopbackProbeWhenBackendIsDown(t *testing.T) {
	// Nothing listens: the dial fails first, so no bind probe runs and the
	// finding is the refused dial, not a false backend_loopback_shared.
	ln, port := listenForLoopbackTest(t, "127.0.0.1:0")
	_ = ln.Close()
	findings := doctorLoopbackFindings(t, "http://127.0.0.1:"+port)
	if got := findDoctorCode(findings, inspect.WarningCodeBackendLoopbackShared); got != nil {
		t.Fatalf("down backend reported as shared: %+v", *got)
	}
	if findDoctorCode(findings, inspect.WarningCodeTargetProbeRefused) == nil {
		t.Fatalf("down backend: want %s, findings %+v", inspect.WarningCodeTargetProbeRefused, findings)
	}
}
