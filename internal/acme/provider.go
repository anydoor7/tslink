package acme

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"

	"golang.org/x/crypto/acme/autocert"
)

// Provider implements domain.CertProvider using Let's Encrypt.
type Provider struct {
	mgr      *autocert.Manager
	listener net.Listener
}

// Option configures Provider creation.
type Option func(*options)
type options struct {
	cacheDir string
	listener net.Listener // for testing
}

// WithCacheDir sets the directory for cached certificates.
func WithCacheDir(dir string) Option {
	return func(o *options) { o.cacheDir = dir }
}

// WithListener overrides the HTTP-01 challenge listener (for testing).
func WithListener(ln net.Listener) Option {
	return func(o *options) { o.listener = ln }
}

// New creates a Provider that obtains certificates via ACME HTTP-01 challenge.
func New(email string, domains []string, opts ...Option) (*Provider, error) {
	o := &options{}
	for _, fn := range opts {
		fn(o)
	}

	if email == "" {
		return nil, fmt.Errorf("acme: email is required")
	}
	if len(domains) == 0 {
		return nil, fmt.Errorf("acme: at least one domain is required")
	}

	mgr := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      email,
		HostPolicy: autocert.HostWhitelist(domains...),
	}

	if o.cacheDir != "" {
		mgr.Cache = autocert.DirCache(o.cacheDir)
	}

	ln := o.listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", ":80")
		if err != nil {
			return nil, fmt.Errorf("acme: listen :80: %w", err)
		}
	}

	return &Provider{mgr: mgr, listener: ln}, nil
}

// GetCertificate implements domain.CertProvider.
func (p *Provider) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return p.mgr.GetCertificate(hello)
}

// Start serves the HTTP-01 challenge handler. Blocks until the listener is closed.
func (p *Provider) Start() error {
	srv := &http.Server{Handler: p.mgr.HTTPHandler(nil)}
	return srv.Serve(p.listener)
}

// Close stops the challenge listener.
func (p *Provider) Close() error {
	return p.listener.Close()
}
