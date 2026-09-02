package tailapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tailscale "tailscale.com/client/tailscale/v2"
)

func saveAPIClientState(t *testing.T) {
	t.Helper()
	oldStored := storedTailscaleClientFn
	oldUserStored := storedUserOwnedTailscaleClientFn
	oldLookup := lookupAPIClientEnvFn
	oldWarn := warnAPIBaseURLRedirectFn
	t.Cleanup(func() {
		storedTailscaleClientFn = oldStored
		storedUserOwnedTailscaleClientFn = oldUserStored
		lookupAPIClientEnvFn = oldLookup
		warnAPIBaseURLRedirectFn = oldWarn
	})
}

func TestAPIBaseURLRejectsNonLoopbackAndCredentialBearingURLs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "public IPv4", url: "http://203.0.113.10:8080"},
		{name: "public domain", url: "https://api.example.com"},
		{name: "explicit evil domain", url: "http://evil.example.com"},
		{name: "userinfo even on loopback", url: "http://user:password@127.0.0.1:8080"},
		{name: "localhost suffix domain", url: "http://localhost.attacker.com:8080"},
		{name: "non HTTP scheme", url: "ftp://127.0.0.1/resource"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveAPIClientState(t)
			t.Setenv(APIBaseURLEnv, tc.url)
			t.Setenv(apiKeyEnv, "test-api-key-not-secret")
			storedCalls := 0
			warnCalls := 0
			storedTailscaleClientFn = func() (*tailscale.Client, error) {
				storedCalls++
				return &tailscale.Client{}, nil
			}
			warnAPIBaseURLRedirectFn = func(string) { warnCalls++ }

			client, err := newTailscaleClient()
			if err == nil {
				t.Fatalf("newTailscaleClient() accepted unsafe base URL %q", tc.url)
			}
			if client != nil {
				t.Fatalf("newTailscaleClient() client = %#v, want nil on rejection", client)
			}
			if storedCalls != 0 {
				t.Fatalf("stored credential path calls = %d, want 0 for rejected override", storedCalls)
			}
			if warnCalls != 0 {
				t.Fatalf("redirect warning calls = %d, want 0 for rejected override", warnCalls)
			}
			if strings.Contains(err.Error(), "test-api-key-not-secret") {
				t.Fatalf("error exposed API credential: %v", err)
			}
		})
	}
}

func TestAPIBaseURLOverrideUsesOnlyEnvironmentCredentialAndWarns(t *testing.T) {
	saveAPIClientState(t)
	server := httptest.NewServer(nil)
	t.Cleanup(server.Close)
	t.Setenv(APIBaseURLEnv, server.URL)
	t.Setenv(apiKeyEnv, "test-api-key-not-secret")

	storedCalls := 0
	storedTailscaleClientFn = func() (*tailscale.Client, error) {
		storedCalls++
		return nil, errors.New("stored credential path must remain closed")
	}
	storedUserOwnedTailscaleClientFn = func() (*tailscale.Client, error) {
		storedCalls++
		return nil, errors.New("stored user-owned credential path must remain closed")
	}
	var warnings []string
	warnAPIBaseURLRedirectFn = func(baseURL string) {
		warnings = append(warnings, baseURL)
	}

	client, err := newTailscaleClient()
	if err != nil {
		t.Fatalf("newTailscaleClient() error = %v", err)
	}
	if storedCalls != 0 {
		t.Fatalf("stored/keychain credential constructor calls = %d, want 0", storedCalls)
	}
	if client == nil || client.BaseURL == nil || client.BaseURL.String() != server.URL {
		t.Fatalf("redirected client BaseURL = %#v, want %q", client, server.URL)
	}
	if client.APIKey != "test-api-key-not-secret" {
		t.Fatal("redirected client did not use the process-local API key")
	}
	userOwnedClient, err := newUserOwnedTailscaleClient()
	if err != nil {
		t.Fatalf("newUserOwnedTailscaleClient() error = %v", err)
	}
	if storedCalls != 0 {
		t.Fatalf("stored/keychain credential constructor calls after user-owned client = %d, want 0", storedCalls)
	}
	if userOwnedClient == nil || userOwnedClient.BaseURL == nil || userOwnedClient.BaseURL.String() != server.URL || userOwnedClient.APIKey != "test-api-key-not-secret" {
		t.Fatalf("redirected user-owned client = %#v, want environment-only client at %q", userOwnedClient, server.URL)
	}
	if len(warnings) != 2 || warnings[0] != server.URL || warnings[1] != server.URL {
		t.Fatalf("redirect warnings = %v, want one per client construction for %q", warnings, server.URL)
	}
}

func TestAPIBaseURLDefaultWarningIsProminent(t *testing.T) {
	saveAPIClientState(t)
	var logOutput bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logOutput, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	warnAPIBaseURLRedirectFn("http://127.0.0.1:12345")
	got := logOutput.String()
	for _, want := range []string{
		"level=WARN",
		"SECURITY WARNING",
		"base_url=http://127.0.0.1:12345",
		"credential_source=TSLINK_API_KEY",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning = %q, want %q", got, want)
		}
	}
}

func TestAPIBaseURLUnsetPreservesStoredCredentialConstructorExactly(t *testing.T) {
	cases := []struct {
		name       string
		wantClient *tailscale.Client
		wantErr    error
	}{
		{name: "client identity", wantClient: &tailscale.Client{Tailnet: "stored-tailnet", APIKey: "stored-placeholder"}},
		{name: "constructor error identity", wantErr: errors.New("stored constructor sentinel")},
		{name: "no configured credential"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			saveAPIClientState(t)
			t.Setenv(APIBaseURLEnv, "")
			calls := 0
			storedTailscaleClientFn = func() (*tailscale.Client, error) {
				calls++
				return tc.wantClient, tc.wantErr
			}
			warnAPIBaseURLRedirectFn = func(string) {
				t.Fatal("redirect warning emitted without an override")
			}

			gotClient, gotErr := newTailscaleClient()
			if calls != 1 {
				t.Fatalf("stored constructor calls = %d, want 1", calls)
			}
			if gotClient != tc.wantClient {
				t.Fatalf("client identity changed: got %p want %p", gotClient, tc.wantClient)
			}
			if gotErr != tc.wantErr {
				t.Fatalf("error identity changed: got %v want %v", gotErr, tc.wantErr)
			}
		})
	}
}

func TestAPIBaseURLAcceptsOnlyLiteralLoopbackHosts(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:8080",
		"https://LOCALHOST:8443/api-root",
		"http://127.42.0.9:8080",
		"http://[::1]:8080",
		"http://[::ffff:127.0.0.1]:8080",
	} {
		t.Run(url.PathEscape(raw), func(t *testing.T) {
			parsed, err := parseLoopbackAPIBaseURL(raw)
			if err != nil {
				t.Fatalf("parseLoopbackAPIBaseURL(%q) error = %v", raw, err)
			}
			if parsed.String() != raw {
				t.Fatalf("parsed URL = %q, want byte-equivalent %q", parsed.String(), raw)
			}
		})
	}
}

func TestAPIBaseURLRequiresEnvironmentAPIKeyWithoutStoredFallback(t *testing.T) {
	saveAPIClientState(t)
	t.Setenv(APIBaseURLEnv, "http://127.0.0.1:1")
	t.Setenv(apiKeyEnv, "")
	storedCalls := 0
	storedTailscaleClientFn = func() (*tailscale.Client, error) {
		storedCalls++
		return &tailscale.Client{APIKey: "stored-placeholder"}, nil
	}

	client, err := newTailscaleClient()
	if err == nil || !strings.Contains(err.Error(), apiKeyEnv) {
		t.Fatalf("newTailscaleClient() = (%#v, %v), want environment-key requirement", client, err)
	}
	if storedCalls != 0 {
		t.Fatalf("stored/keychain credential constructor calls = %d, want 0", storedCalls)
	}
}
