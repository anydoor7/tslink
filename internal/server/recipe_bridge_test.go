package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/recipes"
)

// Run the catalog's actual Nginx snippets in a disposable foreground process.
// CI does not require Nginx; author verification supplies an isolated binary.
func TestRecipeNginxBridge(t *testing.T) {
	binary := os.Getenv("RECIPE_PROXY_NGINX")
	if binary == "" {
		t.Skip("set RECIPE_PROXY_NGINX to run the disposable Nginx consumer")
	}
	for _, id := range []string{"immich", "uptime-kuma"} {
		t.Run(id, func(t *testing.T) {
			origin := "https://" + id + ".review.example"
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if dir := os.Getenv("RECIPE_PROXY_HEADER_RECEIPTS"); dir != "" {
					b, err := json.Marshal(map[string]string{"host": req.Host, "origin": req.Header.Get("Origin"), "forwarded_for": req.Header.Get("X-Forwarded-For"), "real_ip": req.Header.Get("X-Real-IP")})
					if err != nil {
						t.Error(err)
						return
					}
					if err := os.WriteFile(filepath.Join(dir, id+"-bridge.json"), append(b, '\n'), 0600); err != nil {
						t.Error(err)
						return
					}
				}
				// Kuma compares Origin to Host; Immich documents Host/X-Real-IP.
				if req.Header.Get("Origin") != "https://"+req.Host || req.Header.Get("X-Forwarded-Proto") != "https" || req.Header.Get("X-Real-IP") != "192.0.2.10" || req.Header.Get("Upgrade") != "websocket" || req.Header.Get("Connection") != "upgrade" {
					http.Error(w, "bridge header contract rejected", http.StatusForbidden)
					return
				}
				fmt.Fprint(w, "bridge headers accepted")
			}))
			defer backend.Close()
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			r, _ := recipes.Lookup(id)
			snippet := r.Notes[0].Snippet
			// Only ports/hostname are substituted; proxy directives are unchanged.
			snippet = strings.ReplaceAll(snippet, "YOUR-TAILNET.ts.net", "review.example")
			snippet = strings.ReplaceAll(snippet, strings.TrimPrefix(r.DefaultTarget, "http://"), address)
			upstream := "127.0.0.1:2283"
			if id == "uptime-kuma" {
				upstream = "127.0.0.1:3001"
			}
			snippet = strings.ReplaceAll(snippet, "http://"+upstream, backend.URL)
			dir := t.TempDir()
			config := fmt.Sprintf("daemon off;\nmaster_process off;\nerror_log %s;\npid %s;\nevents {}\nhttp { access_log off; client_body_temp_path %s; proxy_temp_path %s; %s }\n", filepath.Join(dir, "error.log"), filepath.Join(dir, "nginx.pid"), filepath.Join(dir, "body"), filepath.Join(dir, "proxy"), snippet)
			path := filepath.Join(dir, "nginx.conf")
			if err := os.WriteFile(path, []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "-p", dir, "-c", path)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			t.Cleanup(func() { _ = command.Process.Kill(); <-done })
			ready := false
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				conn, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
				if err == nil {
					_ = conn.Close()
					ready = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !ready {
				t.Fatal("disposable Nginx did not accept connections; catalog config rejected")
			}
			proxy, err := NewProxyHandler("http://"+address, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, origin+"/socket.io/", nil)
			req.RemoteAddr = "192.0.2.10:1234"
			req.Header.Set("Origin", origin)
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Connection", "upgrade")
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || rec.Body.String() != "bridge headers accepted" {
				t.Fatalf("real proxy/Nginx/backend: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
