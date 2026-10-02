package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/recipes"
)

// Keep recipe settings tied to the actual proxy request, rather than a
// hand-authored Host fixture. These are configuration contracts; the optional
// header receipt is also consumed by the upstream Django/Jupyter/ComfyUI checks.
func TestRecipeProxyConfiguration(t *testing.T) {
	for _, id := range []string{"home-assistant", "jellyfin", "plex", "immich", "nextcloud", "open-webui", "ollama", "comfyui", "grafana", "jupyter", "uptime-kuma", "paperless-ngx", "vaultwarden", "syncthing", "portainer", "generic-web"} {
		t.Run(id, func(t *testing.T) {
			r, ok := recipes.Lookup(id)
			if !ok {
				t.Fatal("recipe missing")
			}
			var got map[string]string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				got = map[string]string{"host": req.Host, "forwarded_host": req.Header.Get("X-Forwarded-Host"), "forwarded_proto": req.Header.Get("X-Forwarded-Proto"), "forwarded_for": req.Header.Get("X-Forwarded-For"), "origin": req.Header.Get("Origin"), "source": req.RemoteAddr, "upstream_host": req.URL.Host, "sec_fetch_site": req.Header.Get("Sec-Fetch-Site")}
				w.WriteHeader(http.StatusOK)
			}))
			defer backend.Close()
			externalHost := id + ".review.example"
			proxy, err := NewProxyHandlerWithOptions(backend.URL, nil, ProxyOptions{PreserveHost: r.PreserveHost, CanonicalHost: func() string { return externalHost }})
			if err != nil {
				t.Fatal(err)
			}
			origin := "https://" + externalHost
			if id == "ollama" {
				origin = "https://open-webui.review.example"
			}
			req := httptest.NewRequest(http.MethodPost, "https://"+externalHost+"/", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			// Forged forwarded headers must not influence the recipe consumer.
			req.Header.Set("X-Forwarded-Host", "attacker.invalid")
			req.Header.Set("X-Forwarded-Proto", "http")
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			upstream, _ := url.Parse(backend.URL)
			got["upstream_host"] = upstream.Host
			expectedHost := upstream.Host
			if r.PreserveHost {
				expectedHost = externalHost
			}
			if rec.Code != http.StatusOK || got["host"] != expectedHost || got["forwarded_host"] != externalHost || got["forwarded_proto"] != "https" || got["origin"] != origin {
				t.Fatalf("real proxy headers: %d %v", rec.Code, got)
			}
			snippet := ""
			for _, note := range r.Notes {
				snippet += note.Snippet + "\n"
			}
			snippet = strings.ReplaceAll(snippet, "YOUR-TAILNET.ts.net", "review.example")
			require := func(setting string) {
				t.Helper()
				if !strings.Contains(snippet, setting) {
					t.Errorf("%s recipe cannot consume proxied Host=%s Origin=%s: missing %s", id, got["host"], got["origin"], setting)
				}
			}
			switch id {
			case "home-assistant":
				require("use_x_forwarded_for: true")
				require("127.0.0.1")
			case "jellyfin":
				require("Known Proxies: 127.0.0.1")
			case "immich", "uptime-kuma":
				if !r.PreserveHost {
					t.Fatal("native app requires the external Host")
				}
				target, _ := url.Parse(r.DefaultTarget)
				port := "2283"
				if id == "uptime-kuma" {
					port = "3001"
				}
				if target.Host != "127.0.0.1:"+port {
					t.Fatalf("native target: %s", r.DefaultTarget)
				}
				require("127.0.0.1:" + port + ":" + port)
			case "nextcloud":
				require("'overwritehost' => '" + got["forwarded_host"] + "'")
				require("'overwriteprotocol' => '" + got["forwarded_proto"] + "'")
			case "open-webui":
				require("CORS_ALLOW_ORIGIN=" + got["origin"])
				require("WEBUI_AUTH=true")
			case "ollama":
				require("OLLAMA_ORIGINS=" + got["origin"])
				require("OLLAMA_HOST=127.0.0.1:11434")
			case "comfyui":
				require("--listen 127.0.0.1 --port 8188")
			case "grafana":
				require("root_url = " + got["origin"] + "/")
				require("enabled = false")
			case "jupyter":
				require("c.ServerApp.local_hostnames = [\"localhost\", \"" + got["host"] + "\"]")
				require("c.ServerApp.disable_check_xsrf = False")
			case "paperless-ngx":
				require("PAPERLESS_URL=" + got["origin"])
				require("PAPERLESS_PROXY_SSL_HEADER=[\"HTTP_X_FORWARDED_PROTO\",\"https\"]")
			case "vaultwarden":
				require("DOMAIN=" + got["origin"])
			case "syncthing":
				host, _, err := net.SplitHostPort(got["host"])
				if err != nil || !net.ParseIP(host).IsLoopback() {
					t.Fatalf("Syncthing default Host guard rejects %s: %v", got["host"], err)
				}
				require("GUI Listen Address: 127.0.0.1:8384")
			case "portainer":
				require("command: [\"--http-enabled\"]")
				// Portainer's actual stdlib CSRF consumer accepts matching Host.
				guard := http.NewCrossOriginProtection()
				proxied := httptest.NewRequest(http.MethodPost, "http://"+got["host"]+"/", nil)
				proxied.Host = got["host"]
				proxied.Header.Set("Origin", got["origin"])
				if err := guard.Check(proxied); err != nil {
					t.Fatalf("configured Portainer Origin rejected: %v", err)
				}
				proxied.Header.Set("Origin", "https://attacker.invalid")
				if err := guard.Check(proxied); err == nil {
					t.Fatal("untrusted Origin accepted")
				}
			case "generic-web":
				require("External URL: " + got["origin"])
			}
			if dir := os.Getenv("RECIPE_PROXY_HEADER_RECEIPTS"); dir != "" {
				// Only explicitly requested test receipts, never fetched app data.
				got["snippet"] = snippet
				b, err := json.MarshalIndent(got, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, id+".json"), append(b, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
