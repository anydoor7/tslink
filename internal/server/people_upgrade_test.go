package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestPeopleWebSocketKeepsRootRoutingAndDeniesReconnect(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/socket" || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "wrong root routing", 400)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := rw.Flush(); err != nil {
			return
		}
		for {
			message := make([]byte, 4)
			if _, err := io.ReadFull(rw, message); err != nil {
				return
			}
			rw.Write(message)
			if err := rw.Flush(); err != nil {
				return
			}
		}
	}))
	defer backend.Close()
	svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: backend.URL}
	if _, err := registry.Add(path, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ChangePerson(path, "alice", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	client := fakeWhoIsClient(t, &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice"}}, nil)
	proxy, err := NewProxyHandler(backend.URL, NewStaticIdentityResolver(client))
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(peopleMiddleware(path, svc, func() (*LocalClient, error) { return client, nil }, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) })(proxy))
	defer front.Close()
	connect := func(want string) (net.Conn, *bufio.Reader) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(front.URL, "http://"), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(conn, "GET /socket HTTP/1.1\r\nHost: photos.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil || !strings.Contains(line, want) {
			t.Fatalf("response=%s expected=%s err=%v", line, want, err)
		}
		for {
			line, err = reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line == "\r\n" {
				break
			}
		}
		return conn, reader
	}
	conn, reader := connect("101 Switching Protocols")
	defer conn.Close()
	echo := func() {
		t.Helper()
		if _, err := conn.Write([]byte("PING")); err != nil {
			t.Fatal(err)
		}
		message := make([]byte, 4)
		if _, err := io.ReadFull(reader, message); err != nil || string(message) != "PING" {
			t.Fatal("upgrade tunnel failed", string(message), err)
		}
	}
	echo()
	if _, err := registry.RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	echo() // Explicit existing-connection limitation, described in the guide.
	rejected, _ := connect("403 Forbidden")
	rejected.Close()
}
