package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testwait"
	"github.com/coder/websocket"
)

func TestGuestIdleStreamTermination(t *testing.T) {
	if path := os.Getenv("GUEST_REVOKE_CHILD_PATH"); path != "" {
		if _, err := registry.RevokeGuest(path, os.Getenv("GUEST_REVOKE_CHILD_ID"), accessTestTime); err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, transport := range []string{"sse-h1", "sse-h2", "websocket"} {
		for _, end := range []string{"revoke", "external-revoke", "expiry"} {
			t.Run(transport+"/"+end, func(t *testing.T) {
				f := newGuestFixture(t, "", transport == "sse-h2", true)
				if _, _, err := registry.CreateGuest(f.path, registry.CreateGuestOptions{App: "photos", Value: "4h", PublicAck: true, Now: accessTestTime}); err != nil {
					t.Fatal(err)
				}
				stopped := make(chan struct{})
				replaceGuestBackend(t, f, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(stopped)
					if strings.HasPrefix(transport, "sse") {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: ready\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					} else {
						conn, err := websocket.Accept(w, r, nil)
						if err != nil {
							return
						}
						defer conn.CloseNow()
						typ, message, err := conn.Read(r.Context())
						if err != nil {
							return
						}
						if err = conn.Write(r.Context(), typ, message); err != nil {
							return
						}
						_, _, _ = conn.Read(r.Context())
					}
				}))
				cookies := f.login()
				releaseRead := f.holdCounterFlush()
				defer func() {
					if releaseRead != nil {
						releaseRead()
					}
				}()
				if strings.HasPrefix(transport, "sse") {
					req, _ := http.NewRequest("GET", f.base+"/events", nil)
					for _, cookie := range cookies {
						req.AddCookie(cookie)
					}
					resp, err := f.client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if line, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || line != "data: ready\n" {
						t.Fatal("stream control", line, err)
					}
					if transport == "sse-h2" && resp.ProtoMajor != 2 {
						t.Fatal("HTTP/2 control")
					}
				} else {
					req, _ := http.NewRequest("GET", f.base, nil)
					for _, cookie := range cookies {
						req.AddCookie(cookie)
					}
					ctx, cancel := context.WithTimeout(t.Context(), testwait.Budget(t))
					defer cancel()
					conn, _, err := websocket.Dial(ctx, strings.Replace(f.base, "https:", "wss:", 1)+"/socket", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: req.Header})
					if err != nil {
						t.Fatal(err)
					}
					defer conn.CloseNow()
					if err = conn.Write(ctx, websocket.MessageText, []byte("ready")); err != nil {
						t.Fatal(err)
					}
					if _, msg, err := conn.Read(ctx); err != nil || string(msg) != "ready" {
						t.Fatal("WebSocket control", err)
					}
				}
				releaseRead()
				releaseRead = nil
				start := time.Now()
				switch end {
				case "revoke":
					if _, err := registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
						t.Fatal(err)
					}
				case "external-revoke":
					child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGuestIdleStreamTermination$", "-test.count=1")
					child.Env = append(os.Environ(), "GUEST_REVOKE_CHILD_PATH="+f.path, "GUEST_REVOKE_CHILD_ID="+f.grant.ID)
					if out, err := child.CombinedOutput(); err != nil {
						t.Fatal("revoke process", err, string(out))
					}
				case "expiry":
					f.now.Store(f.grant.ExpiresAt.UnixNano())
				}
				testwait.Recv(t, stopped, "idle stream terminated after "+end)
				t.Logf("idle stream terminated after %s in %s", end, time.Since(start))
			})
		}
	}
}

func TestGuestHijackedConnectionAfterHandlerReturns(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	f.s.stopNodeLocked("photos")
	completed := make(chan struct{})
	gate := newGuestGate(f.path, f.svc, func() time.Time { return time.Unix(0, f.now.Load()) }, f.store, http.NotFoundHandler(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		if err = rw.Flush(); err != nil {
			conn.Close()
			return
		}
		go func() {
			defer close(completed)
			defer conn.Close()
			for {
				line, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if _, err = rw.WriteString(line); err != nil {
					return
				}
				if err = rw.Flush(); err != nil {
					return
				}
			}
		}()
	})).(*guestGate)
	t.Cleanup(func() { gate.Close() })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gate.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accessFunnelKey{}, true)))
	})}
	t.Cleanup(func() { srv.Close() })
	go srv.Serve(tls.NewListener(ln, f.tlsConfig))
	base := "https://" + ln.Addr().String()
	releaseRead := f.holdCounterFlush()
	defer func() {
		if releaseRead != nil {
			releaseRead()
		}
	}()
	resp, err := f.client.Get(base + "/guest/" + f.token)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatal("session control", resp.StatusCode)
	}
	conn, err := tls.Dial("tcp", ln.Addr().String(), f.client.Transport.(*http.Transport).TLSClientConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(testwait.Budget(t)))
	req, _ := http.NewRequest("GET", base+"/socket", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "test")
	for _, cookie := range resp.Cookies() {
		req.AddCookie(cookie)
	}
	if err = req.Write(conn); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, req)
	if err != nil || upgrade.StatusCode != 101 {
		t.Fatal("upgrade control", err)
	}
	if _, err = conn.Write([]byte("ready\n")); err != nil {
		t.Fatal(err)
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal("registered connection closed on handler return", line, err)
	}
	releaseRead()
	releaseRead = nil
	if _, err = registry.RevokeGuest(f.path, f.grant.ID, accessTestTime); err != nil {
		t.Fatal(err)
	}
	testwait.Recv(t, completed, "returned handler's connection closed on revoke")
}
