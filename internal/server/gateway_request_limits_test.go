package server

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

// A caller granted one routed app is admitted by the connection gate and may
// then reach every other route's HTTP layer, where it is refused. Such a
// refusal leaves the request body unread. The route must still dispose of that
// body within the owner's read bound, as an app's own node does: otherwise a
// caller who holds any grant can pin the route's connection slots with
// incomplete bodies and starve the callers who are allowed.
func TestGatewayRefusedRequestBodyIsDisposedWithinTheReadBound(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			// The bound under test is the owner's read-idle budget (200ms here);
			// the hang guard only has to outlast it plus Go's close grace, so a
			// regression fails in seconds rather than at the 2 minute budget.
			const bound, guard = 200 * time.Millisecond, 8 * time.Second
			f := newGatewayFixture(t, func(f *gatewayFixture) {
				if _, err := registry.MutateService(f.path, "payroll", func(s registry.Service) (registry.Service, error) {
					s.RequestLimits = &registry.RequestLimits{ReadTimeout: bound.String()}
					return s, nil
				}); err != nil {
					t.Fatal(err)
				}
			})
			f.waitRoute(t, "payroll", "running")
			// alice holds photos only, so the TLS gate admits her and payroll
			// refuses her request.
			conn, err := tls.Dial("tcp", f.node.addr("8444"), &tls.Config{RootCAs: f.pool, ServerName: "home.tailnet.ts.net", MinVersion: tls.VersionTLS12})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			start := time.Now()
			_ = conn.SetDeadline(start.Add(guard))
			framing, body := "Content-Length: 9\r\n", "x"
			if chunked {
				framing, body = "Transfer-Encoding: chunked\r\n", "1\r\nx\r\n"
			}
			// The body is never completed and the client never closes.
			if _, err := fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: home.tailnet.ts.net:8444\r\n%s\r\n%s", framing, body); err != nil {
				t.Fatal(err)
			}
			br := bufio.NewReader(conn)
			resp, err := http.ReadResponse(br, nil)
			if err != nil {
				t.Fatalf("no refusal within %s (the server is still waiting on the unread body): %v", guard, err)
			}
			text, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(text), "You do not have access to this app") {
				t.Fatalf("status=%d body=%q, want the 403 refusal", resp.StatusCode, text)
			}
			if f.backend[1].Load() != 0 {
				t.Fatal("a refused request reached the upstream")
			}
			// The server, not this client, must end the connection.
			if _, err := br.ReadByte(); err == nil {
				t.Fatal("server sent more bytes after the refusal")
			} else if isTimeout(err) || !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) && !strings.Contains(err.Error(), "reset") {
				t.Fatalf("connection still held %s after the refusal (bound %s): %v", time.Since(start).Round(time.Millisecond), bound, err)
			}
			t.Logf("connection released %s after the request (bound %s)", time.Since(start).Round(time.Millisecond), bound)
		})
	}
}
