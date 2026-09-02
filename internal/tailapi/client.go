package tailapi

import (
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"

	"github.com/monody0007/tslink/internal/credentials"
	tailscale "tailscale.com/client/tailscale/v2"
)

// APIBaseURLEnv redirects the Tailscale REST API client to a loopback-only
// endpoint. It exists so command-layer tests can exercise the real tailapi
// implementation without contacting Tailscale.
const APIBaseURLEnv = "TSLINK_API_BASE_URL"

const apiKeyEnv = "TSLINK_API_KEY"

var (
	storedTailscaleClientFn          = credentials.NewTailscaleClient
	storedUserOwnedTailscaleClientFn = credentials.NewTailscaleClientWithUserOwnedAPIKey
	lookupAPIClientEnvFn             = os.LookupEnv
	warnAPIBaseURLRedirectFn         = func(baseURL string) {
		slog.Warn("SECURITY WARNING: Tailscale API base URL redirected to a loopback endpoint", "base_url", baseURL, "credential_source", apiKeyEnv)
	}
)

func newTailscaleClient() (*tailscale.Client, error) {
	client, overridden, err := redirectedTailscaleClient()
	if err != nil || overridden {
		return client, err
	}
	return storedTailscaleClientFn()
}

// NewTailscaleClient exposes the same fail-closed loopback override gate to
// higher-level callers that must pass a client factory into credentials.
func NewTailscaleClient() (*tailscale.Client, error) {
	return newTailscaleClient()
}

func newUserOwnedTailscaleClient() (*tailscale.Client, error) {
	client, overridden, err := redirectedTailscaleClient()
	if err != nil || overridden {
		return client, err
	}
	return storedUserOwnedTailscaleClientFn()
}

// redirectedTailscaleClient returns overridden=false without inspecting any
// credential environment variable when APIBaseURLEnv is unset. When it is set,
// the stored-credential constructors above are structurally bypassed: only the
// process-local TSLINK_API_KEY value can populate the redirected client.
func redirectedTailscaleClient() (*tailscale.Client, bool, error) {
	rawBaseURL, set := lookupAPIClientEnvFn(APIBaseURLEnv)
	if !set || strings.TrimSpace(rawBaseURL) == "" {
		return nil, false, nil
	}

	baseURL, err := parseLoopbackAPIBaseURL(rawBaseURL)
	if err != nil {
		return nil, true, err
	}
	key, _ := lookupAPIClientEnvFn(apiKeyEnv)
	if strings.TrimSpace(key) == "" {
		return nil, true, fmt.Errorf("%s requires a non-empty %s value; stored credentials are deliberately disabled for redirected API requests", APIBaseURLEnv, apiKeyEnv)
	}
	client, err := credentials.NewTailscaleClientWithAPIKey(key)
	if err != nil {
		return nil, true, err
	}
	client.BaseURL = baseURL
	warnAPIBaseURLRedirectFn(baseURL.String())
	return client, true, nil
}

func parseLoopbackAPIBaseURL(raw string) (*url.URL, error) {
	baseURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", APIBaseURLEnv, err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("invalid %s: scheme must be http or https", APIBaseURLEnv)
	}
	if baseURL.Opaque != "" || baseURL.Host == "" || baseURL.Hostname() == "" {
		return nil, fmt.Errorf("invalid %s: an absolute URL with a host is required", APIBaseURLEnv)
	}
	if baseURL.User != nil {
		return nil, fmt.Errorf("invalid %s: URL user information is forbidden", APIBaseURLEnv)
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("invalid %s: query strings and fragments are forbidden", APIBaseURLEnv)
	}

	host := baseURL.Hostname()
	if strings.EqualFold(host, "localhost") {
		return baseURL, nil
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Unmap().IsLoopback() {
		return nil, fmt.Errorf("invalid %s: host must be literal localhost or a loopback IP address", APIBaseURLEnv)
	}
	return baseURL, nil
}
