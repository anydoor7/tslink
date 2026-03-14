package acme

import (
	"crypto/tls"
	"net"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/acme/autocert"
)

func testListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return ln
}

func TestNew_EmptyEmail(t *testing.T) {
	_, err := New("", []string{"example.com"}, WithListener(testListener(t)))
	if err == nil {
		t.Fatal("New() error = nil, want error for empty email")
	}
	if err.Error() != "acme: email is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_EmptyDomains(t *testing.T) {
	_, err := New("user@example.com", nil, WithListener(testListener(t)))
	if err == nil {
		t.Fatal("New() error = nil, want error for empty domains")
	}
	if err.Error() != "acme: at least one domain is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNew_EmptyDomainSlice(t *testing.T) {
	_, err := New("user@example.com", []string{}, WithListener(testListener(t)))
	if err == nil {
		t.Fatal("New() error = nil, want error for empty domains slice")
	}
}

func TestNew_Success(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p == nil {
		t.Fatal("New() returned nil provider")
	}
	if p.mgr == nil {
		t.Fatal("provider.mgr is nil")
	}
	if p.listener == nil {
		t.Fatal("provider.listener is nil")
	}
	p.Close()
}

func TestNew_WithCacheDir(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "certs")
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithCacheDir(cacheDir), WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Close()

	// Verify cache is set to DirCache
	if p.mgr.Cache == nil {
		t.Fatal("expected mgr.Cache to be set")
	}
	dc, ok := p.mgr.Cache.(autocert.DirCache)
	if !ok {
		t.Fatalf("expected DirCache, got %T", p.mgr.Cache)
	}
	if string(dc) != cacheDir {
		t.Fatalf("DirCache = %q, want %q", string(dc), cacheDir)
	}
}

func TestNew_WithoutCacheDir(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Close()

	if p.mgr.Cache != nil {
		t.Fatal("expected mgr.Cache to be nil when no cache dir is set")
	}
}

func TestNew_MultipleDomains(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"a.example.com", "b.example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Close()

	if p.mgr.Email != "user@example.com" {
		t.Fatalf("mgr.Email = %q, want %q", p.mgr.Email, "user@example.com")
	}
}

func TestNew_DefaultListenerError(t *testing.T) {
	// Occupy port 80 first to force the default listener to fail.
	// On most CI/test environments, binding to :80 requires root,
	// so New() without WithListener should fail.
	_, err := New("user@example.com", []string{"example.com"})
	if err == nil {
		// If it succeeded (running as root), close and skip.
		t.Skip("binding to :80 succeeded (running as root?); skipping error-path test")
	}
	if err.Error() == "acme: email is required" || err.Error() == "acme: at least one domain is required" {
		t.Fatalf("unexpected validation error: %v", err)
	}
	// Should be a listen error.
}

func TestGetCertificate(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer p.Close()

	// Calling GetCertificate will fail because there is no real ACME server,
	// but it exercises the code path.
	_, err = p.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	if err == nil {
		t.Fatal("GetCertificate() error = nil, expected error without real ACME")
	}
}

func TestStartAndClose(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Start()
	}()

	// Close the listener, which should cause Start to return.
	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	startErr := <-errCh
	if startErr == nil {
		t.Fatal("Start() error = nil, expected error from closed listener")
	}
}

func TestClose(t *testing.T) {
	ln := testListener(t)
	p, err := New("user@example.com", []string{"example.com"}, WithListener(ln))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// First close should succeed.
	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Second close should return an error (already closed).
	if err := p.Close(); err == nil {
		t.Fatal("second Close() error = nil, expected error for already-closed listener")
	}
}

func TestWithListener_Option(t *testing.T) {
	ln := testListener(t)
	o := &options{}
	WithListener(ln)(o)
	if o.listener != ln {
		t.Fatal("WithListener did not set listener")
	}
	ln.Close()
}

func TestWithCacheDir_Option(t *testing.T) {
	o := &options{}
	WithCacheDir("/tmp/certs")(o)
	if o.cacheDir != "/tmp/certs" {
		t.Fatalf("WithCacheDir set cacheDir = %q, want %q", o.cacheDir, "/tmp/certs")
	}
}
