package domain

import (
	"crypto/tls"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// domainRegexp validates a basic hostname: labels separated by dots,
// each label starts/ends with alphanumeric, may contain hyphens.
var domainRegexp = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

// ValidateDomain checks if the given string is a valid domain name.
func ValidateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain must not be empty")
	}
	if strings.ContainsAny(domain, " \t\n\r") {
		return fmt.Errorf("domain must not contain whitespace: %q", domain)
	}
	if strings.Contains(domain, "://") {
		return fmt.Errorf("domain must not contain a scheme: %q", domain)
	}
	if strings.Contains(domain, "/") {
		return fmt.Errorf("domain must not contain a path: %q", domain)
	}
	if !strings.Contains(domain, ".") {
		return fmt.Errorf("domain must contain at least one dot: %q", domain)
	}
	if !domainRegexp.MatchString(domain) {
		return fmt.Errorf("invalid domain name: %q", domain)
	}
	return nil
}

// CertProvider abstracts TLS certificate retrieval for custom domains.
// In production, this would use Let's Encrypt with DNS validation.
type CertProvider interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
}

// Config holds custom domain configuration for a service.
type Config struct {
	ServiceName string
	Domain      string
	// CertDir is where certificates are cached.
	CertDir string
}

// Manager handles custom domain TLS certificates.
type Manager struct {
	mu       sync.RWMutex
	configs  map[string]*Config // domain -> config
	certDir  string
	provider CertProvider
}

// NewManager creates a domain manager with the given cert cache directory.
func NewManager(certDir string) *Manager {
	return &Manager{
		configs: make(map[string]*Config),
		certDir: certDir,
	}
}

// SetProvider sets a CertProvider for TLS certificate retrieval.
func (m *Manager) SetProvider(p CertProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.provider = p
}

// AddDomain registers a custom domain for a service.
func (m *Manager) AddDomain(serviceName, domain string) error {
	if err := ValidateDomain(domain); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.configs[domain]; ok {
		return fmt.Errorf("domain %q is already registered for service %q", domain, existing.ServiceName)
	}

	m.configs[domain] = &Config{
		ServiceName: serviceName,
		Domain:      domain,
		CertDir:     m.certDir,
	}
	return nil
}

// RemoveDomain unregisters a custom domain.
func (m *Manager) RemoveDomain(domain string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.configs, domain)
}

// HasDomain checks if a domain is registered.
func (m *Manager) HasDomain(domain string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.configs[domain]
	return ok
}

// GetServiceForDomain returns the service name for a given domain.
func (m *Manager) GetServiceForDomain(domain string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.configs[domain]
	if !ok {
		return "", false
	}
	return cfg.ServiceName, true
}

// TLSConfig returns a tls.Config that can serve certificates for registered domains.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			m.mu.RLock()
			_, ok := m.configs[hello.ServerName]
			provider := m.provider
			m.mu.RUnlock()

			if !ok {
				return nil, fmt.Errorf("no certificate for domain %q", hello.ServerName)
			}

			if provider != nil {
				return provider.GetCertificate(hello)
			}

			return nil, fmt.Errorf("no certificate provider configured for domain %q", hello.ServerName)
		},
	}
}
