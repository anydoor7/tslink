package testenv

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"runtime/debug"
	"strings"
	"sync"
)

const NetworkGuardReportEnv = "TSLINK_NETWORK_GUARD_REPORT"

type nonLoopbackDialAttempt struct {
	network string
	stack   []byte
}

// RunWithNonLoopbackDialGuard runs one package's complete test binary while
// intercepting the real TCP dial hook used by http.DefaultTransport. Loopback
// dials remain available for httptest servers; every non-loopback attempt is
// blocked, recorded, and turns an otherwise-passing package into a failure.
func RunWithNonLoopbackDialGuard(run func() int, packageName string) int {
	original := http.DefaultTransport
	base, ok := original.(*http.Transport)
	if !ok {
		fmt.Fprintf(os.Stderr, "network guard [%s]: http.DefaultTransport has type %T, want *http.Transport\n", packageName, original)
		return 1
	}

	guarded := base.Clone()
	dialContext := guarded.DialContext
	if dialContext == nil {
		dialContext = (&net.Dialer{}).DialContext
	}
	var mu sync.Mutex
	var attempts []nonLoopbackDialAttempt
	guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if strings.HasPrefix(network, "tcp") && !isLoopbackDialAddress(address) {
			mu.Lock()
			attempts = append(attempts, nonLoopbackDialAttempt{network: network, stack: debug.Stack()})
			mu.Unlock()
			return nil, fmt.Errorf("test network guard blocked a non-loopback TCP dial")
		}
		return dialContext(ctx, network, address)
	}

	http.DefaultTransport = guarded
	code := run()
	guarded.CloseIdleConnections()
	http.DefaultTransport = original

	mu.Lock()
	hits := append([]nonLoopbackDialAttempt(nil), attempts...)
	mu.Unlock()
	if os.Getenv(NetworkGuardReportEnv) == "1" || len(hits) > 0 {
		fmt.Fprintf(os.Stderr, "network guard [%s]: non-loopback real TCP dial attempts = %d\n", packageName, len(hits))
	}
	for i, hit := range hits {
		fmt.Fprintf(os.Stderr, "network guard [%s]: blocked attempt %d over %s; address redacted\n%s", packageName, i+1, hit.network, hit.stack)
	}
	if len(hits) > 0 {
		return 1
	}
	return code
}

func isLoopbackDialAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.Unmap().IsLoopback()
}
