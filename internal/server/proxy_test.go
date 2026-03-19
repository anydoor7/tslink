package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type timeoutError struct {
	timeout bool
}

func (e *timeoutError) Error() string   { return "timeout" }
func (e *timeoutError) Timeout() bool   { return e.timeout }
func (e *timeoutError) Temporary() bool { return false }

func mustReverseProxy(t *testing.T, target string, localClient *LocalClient) *httputil.ReverseProxy {
	t.Helper()

	h, err := NewProxyHandler(target, localClient)
	if err != nil {
		t.Fatalf("NewProxyHandler() error = %v", err)
	}
	rp, ok := h.(*httputil.ReverseProxy)
	if !ok {
		t.Fatalf("handler type = %T, want *httputil.ReverseProxy", h)
	}
	return rp
}

func TestNewProxyHandler_ValidTarget(t *testing.T) {
	h, err := NewProxyHandler("http://localhost:8080", nil)
	if err != nil {
		t.Fatalf("NewProxyHandler() error = %v", err)
	}
	if h == nil {
		t.Fatal("handler should not be nil")
	}
}

func TestNewProxyHandler_InvalidTarget(t *testing.T) {
	_, err := NewProxyHandler("://invalid", nil)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestProxyRewrite_StripsSpoofedHeaders(t *testing.T) {
	rp := mustReverseProxy(t, "http://localhost:8080", nil)

	in := httptest.NewRequest(http.MethodGet, "http://incoming.example/path?q=1", nil)
	in.RemoteAddr = "100.64.0.1:1234"
	in.Header.Set("X-Tailscale-Node", "spoofed-node")
	in.Header.Set("X-Tailscale-User-Login", "spoofed-login")
	in.Header.Set("X-Other", "keep-me")

	out := in.Clone(context.Background())
	out.Header = in.Header.Clone()

	rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})

	if out.URL.Scheme != "http" || out.URL.Host != "localhost:8080" {
		t.Fatalf("rewritten URL = %s, want host localhost:8080 over http", out.URL.String())
	}
	if got := out.Header.Get("X-Tailscale-Node"); got != "" {
		t.Fatalf("X-Tailscale-Node = %q, want empty", got)
	}
	if got := out.Header.Get("X-Tailscale-User-Login"); got != "" {
		t.Fatalf("X-Tailscale-User-Login = %q, want empty", got)
	}
	if got := out.Header.Get("X-Other"); got != "keep-me" {
		t.Fatalf("X-Other = %q, want %q", got, "keep-me")
	}
	if got := out.Header.Get("X-Forwarded-For"); got == "" {
		t.Fatal("X-Forwarded-For should be set")
	}
}

func TestProxyRewrite_AddsIdentityHeaders(t *testing.T) {
	body, err := json.Marshal(&apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName:   "user@example.com",
			DisplayName: "Example User",
		},
		Node: &tailcfg.Node{
			ComputedName: "workstation",
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	localClient := &LocalClient{
		OmitAuth: true,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/localapi/v0/whois" {
				t.Fatalf("WhoIs path = %q, want %q", req.URL.Path, "/localapi/v0/whois")
			}
			if got := req.URL.Query().Get("addr"); got != "100.64.0.1:1234" {
				t.Fatalf("whois addr = %q, want %q", got, "100.64.0.1:1234")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}

	rp := mustReverseProxy(t, "http://localhost:8080", localClient)

	in := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
	in.RemoteAddr = "100.64.0.1:1234"
	out := in.Clone(context.Background())
	out.Header = in.Header.Clone()

	rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})

	if got := out.Header.Get("X-Tailscale-User-Login"); got != "user@example.com" {
		t.Fatalf("X-Tailscale-User-Login = %q, want %q", got, "user@example.com")
	}
	if got := out.Header.Get("X-Tailscale-User-Name"); got != "Example User" {
		t.Fatalf("X-Tailscale-User-Name = %q, want %q", got, "Example User")
	}
	if got := out.Header.Get("X-Tailscale-Node"); got != "workstation" {
		t.Fatalf("X-Tailscale-Node = %q, want %q", got, "workstation")
	}
}

func TestProxyErrorHandler_Timeout(t *testing.T) {
	rp := mustReverseProxy(t, "http://localhost:8080", nil)
	req := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
	w := httptest.NewRecorder()

	rp.ErrorHandler(w, req, &timeoutError{timeout: true})

	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

func TestProxyErrorHandler_BadGateway(t *testing.T) {
	rp := mustReverseProxy(t, "http://localhost:8080", nil)
	req := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
	w := httptest.NewRecorder()

	rp.ErrorHandler(w, req, errors.New("backend offline"))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}
}

func TestIsTimeout_NetTimeout(t *testing.T) {
	if !isTimeout(&timeoutError{timeout: true}) {
		t.Error("net.Error with Timeout()=true should be timeout")
	}
}

func TestIsTimeout_DeadlineExceeded(t *testing.T) {
	if !isTimeout(context.DeadlineExceeded) {
		t.Error("DeadlineExceeded should be timeout")
	}
}

func TestIsTimeout_RegularError(t *testing.T) {
	if isTimeout(errors.New("not a timeout")) {
		t.Error("regular error should not be timeout")
	}
}

func TestIsTimeout_WrappedDeadline(t *testing.T) {
	err := errors.Join(errors.New("wrapped"), context.DeadlineExceeded)
	if !isTimeout(err) {
		t.Error("wrapped DeadlineExceeded should be timeout")
	}
}

// deadlineOnlyError contains DeadlineExceeded in its chain but does NOT
// implement net.Error, so errors.As(err, &netErr) returns false and
// isTimeout must fall through to the errors.Is branch.
type deadlineOnlyError struct{ inner error }

func (e *deadlineOnlyError) Error() string { return "op failed: " + e.inner.Error() }
func (e *deadlineOnlyError) Unwrap() []error { return []error{e.inner} }

func TestIsTimeout_DeadlineWithoutNetError(t *testing.T) {
	err := &deadlineOnlyError{inner: context.DeadlineExceeded}
	// errors.As should NOT match net.Error (deadlineOnlyError doesn't implement it)
	var netErr net.Error
	if errors.As(err, &netErr) {
		t.Skip("errors.As matches net.Error through Unwrap — branch unreachable in this Go version")
	}
	if !isTimeout(err) {
		t.Error("wrapped DeadlineExceeded (non-net.Error) should be timeout")
	}
}

func TestIsTimeout_NonTimeoutNetError(t *testing.T) {
	if isTimeout(&net.DNSError{IsTimeout: false}) {
		t.Error("non-timeout net error should not be timeout")
	}
}

func TestProxyRewrite_ProfilePicURL(t *testing.T) {
	tests := []struct {
		name       string
		picURL     string
		wantHeader bool
	}{
		{"with picture", "https://example.com/avatar.png", true},
		{"empty picture", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(&apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{
					LoginName:     "user@example.com",
					DisplayName:   "Example User",
					ProfilePicURL: tt.picURL,
				},
				Node: &tailcfg.Node{
					ComputedName: "workstation",
				},
			})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}

			localClient := &LocalClient{
				OmitAuth: true,
				Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader(string(body))),
					}, nil
				}),
			}

			rp := mustReverseProxy(t, "http://localhost:8080", localClient)

			in := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
			in.RemoteAddr = "100.64.0.1:1234"
			out := in.Clone(context.Background())
			out.Header = in.Header.Clone()

			rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})

			got := out.Header.Get("X-Tailscale-User-Picture")
			if tt.wantHeader {
				if got != tt.picURL {
					t.Fatalf("X-Tailscale-User-Picture = %q, want %q", got, tt.picURL)
				}
			} else {
				if got != "" {
					t.Fatalf("X-Tailscale-User-Picture = %q, want empty", got)
				}
			}
		})
	}
}

func TestProxyRewrite_WhoIsError(t *testing.T) {
	localClient := &LocalClient{
		OmitAuth: true,
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("whois unavailable")
		}),
	}

	rp := mustReverseProxy(t, "http://localhost:8080", localClient)

	in := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
	in.RemoteAddr = "100.64.0.1:1234"
	out := in.Clone(context.Background())
	out.Header = in.Header.Clone()

	rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})

	// When WhoIs fails, identity headers should NOT be set
	if got := out.Header.Get("X-Tailscale-User-Login"); got != "" {
		t.Fatalf("X-Tailscale-User-Login = %q, want empty on WhoIs error", got)
	}
	if got := out.Header.Get("X-Tailscale-User-Name"); got != "" {
		t.Fatalf("X-Tailscale-User-Name = %q, want empty on WhoIs error", got)
	}
	if got := out.Header.Get("X-Tailscale-Node"); got != "" {
		t.Fatalf("X-Tailscale-Node = %q, want empty on WhoIs error", got)
	}
	// URL rewrite should still work
	if out.URL.Host != "localhost:8080" {
		t.Fatalf("URL host = %q, want %q", out.URL.Host, "localhost:8080")
	}
}

func TestProxyRewrite_NilLocalClient(t *testing.T) {
	rp := mustReverseProxy(t, "http://localhost:8080", nil)

	in := httptest.NewRequest(http.MethodGet, "http://incoming.example/path", nil)
	in.RemoteAddr = "100.64.0.1:1234"
	out := in.Clone(context.Background())
	out.Header = in.Header.Clone()

	rp.Rewrite(&httputil.ProxyRequest{In: in, Out: out})

	// No identity headers without localClient
	if got := out.Header.Get("X-Tailscale-User-Login"); got != "" {
		t.Fatalf("X-Tailscale-User-Login = %q, want empty with nil localClient", got)
	}
}
