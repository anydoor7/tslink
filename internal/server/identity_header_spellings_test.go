package server

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestProxyIdentityHeaderSpellingsAtCGIBackend(t *testing.T) {
	for _, identified := range []bool{false, true} {
		name := "WhoIsFailure"
		if identified {
			name = "WhoIsSuccess"
		}
		t.Run(name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				env := map[string][]string{}
				for key, values := range r.Header {
					// CGI/WSGI folds hyphens and underscores to the same variable.
					key = "HTTP_" + strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
					if strings.HasPrefix(key, "HTTP_X_TAILSCALE_") || strings.HasPrefix(key, "HTTP_TAILSCALE_") {
						env[key] = append(env[key], values...)
					}
				}
				_ = json.NewEncoder(w).Encode(env)
			}))
			t.Cleanup(backend.Close)
			var who *apitype.WhoIsResponse
			whoErr := errors.New("whois unavailable")
			want := map[string][]string{}
			if identified {
				whoErr = nil
				who = &apitype.WhoIsResponse{UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com", DisplayName: "Alice"}}
				want["HTTP_X_TAILSCALE_USER_LOGIN"] = []string{"alice@example.com"}
				want["HTTP_X_TAILSCALE_USER_NAME"] = []string{"Alice"}
			}
			handler, err := NewProxyHandler(backend.URL, NewStaticIdentityResolver(fakeWhoIsClient(t, who, whoErr)))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "http://incoming.example/", nil)
			req.RemoteAddr = "100.64.0.1:1234"
			for _, header := range []string{
				"X-Tailscale-User-Login", "Tailscale-User-Login", "Tailscale-User-Name", "Tailscale-Headers-Info",
				"X_Tailscale_User_Login", "X-Tailscale_User-Login", "Tailscale_User_Login", "TaIlScAlE_Headers-Info",
			} {
				req.Header.Set(header, "spoofed")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("backend response = %d: %s", rec.Code, rec.Body.String())
			}
			var got map[string][]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if !maps.EqualFunc(got, want, slices.Equal[[]string]) {
				t.Fatalf("CGI identity = %v, want only verified identity %v", got, want)
			}
		})
	}
}
