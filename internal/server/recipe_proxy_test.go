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
	for _, id := range []string{"home-assistant", "jellyfin", "immich", "nextcloud", "open-webui", "ollama", "comfyui", "grafana", "jupyter", "uptime-kuma", "paperless-ngx", "vaultwarden", "syncthing", "portainer", "generic-web"} {
		t.Run(id, func(t *testing.T) {
			r, ok := recipes.Lookup(id)
			if !ok {
				t.Fatal("recipe missing")
			}
			var got map[string]string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				got = map[string]string{"host": req.Host, "forwarded_host": req.Header.Get("X-Forwarded-Host"), "forwarded_proto": req.Header.Get("X-Forwarded-Proto"), "forwarded_for": req.Header.Get("X-Forwarded-For"), "origin": req.Header.Get("Origin"), "source": req.RemoteAddr}
				w.WriteHeader(http.StatusOK)
			}))
			defer backend.Close()
			proxy, err := NewProxyHandler(backend.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			externalHost := id + ".review.example"
			origin := "https://" + externalHost
			if id == "ollama" {
				origin = "https://open-webui.review.example"
			}
			req := httptest.NewRequest(http.MethodPost, "https://"+externalHost+"/", nil)
			req.Header.Set("Origin", origin)
			// Forged forwarded headers must not influence the recipe consumer.
			req.Header.Set("X-Forwarded-Host", "attacker.invalid")
			req.Header.Set("X-Forwarded-Proto", "http")
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			upstream, _ := url.Parse(backend.URL)
			if rec.Code != http.StatusOK || got["host"] != upstream.Host || got["forwarded_host"] != externalHost || got["forwarded_proto"] != "https" || got["origin"] != origin {
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
				require("proxy_set_header Host " + got["forwarded_host"] + ";")
				require("proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;")
				require("proxy_set_header Upgrade $http_upgrade;")
				if id == "immich" {
					require("proxy_set_header X-Real-IP $http_x_forwarded_for;")
				}
				bridge, err := url.Parse(r.DefaultTarget)
				if err != nil {
					t.Fatal(err)
				}
				require("listen " + bridge.Host + ";")
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
				require("--enable-cors-header " + got["origin"])
			case "grafana":
				require("root_url = " + got["origin"] + "/")
				require("enabled = false")
			case "jupyter":
				require("c.ServerApp.allow_origin = \"" + got["origin"] + "\"")
				require("c.ServerApp.disable_check_xsrf = False")
			case "paperless-ngx":
				require("PAPERLESS_USE_X_FORWARD_HOST=true")
				require("PAPERLESS_ALLOWED_HOSTS=" + got["forwarded_host"])
				require("PAPERLESS_TRUSTED_PROXIES=127.0.0.1,::1")
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
				require("\"--trusted-origins\", \"" + got["origin"] + "\"")
				// Current Portainer uses this standard-library consumer for CSRF.
				guard := http.NewCrossOriginProtection()
				proxied := httptest.NewRequest(http.MethodPost, "http://"+got["host"]+"/", nil)
				proxied.Host = got["host"]
				proxied.Header.Set("Origin", got["origin"])
				if err := guard.Check(proxied); err == nil {
					t.Fatal("unconfigured Origin rejection control did not reject")
				}
				if strings.Contains(snippet, "\"--trusted-origins\", \""+got["origin"]+"\"") {
					if err := guard.AddTrustedOrigin(got["origin"]); err != nil {
						t.Fatal(err)
					}
				}
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
