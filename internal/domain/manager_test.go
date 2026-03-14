package domain

import (
	"crypto/tls"
	"testing"
)

func TestValidateDomain_Valid(t *testing.T) {
	valid := []string{
		"app.example.com",
		"my-app.example.com",
		"sub.domain.example.co.uk",
		"a.bc",
		"test.io",
		"my-service.internal.company.com",
	}
	for _, d := range valid {
		if err := ValidateDomain(d); err != nil {
			t.Errorf("ValidateDomain(%q) = %v, want nil", d, err)
		}
	}
}

func TestValidateDomain_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		errMsg string
	}{
		{"empty", "", "must not be empty"},
		{"no dot", "localhost", "must contain at least one dot"},
		{"has scheme http", "http://app.example.com", "must not contain a scheme"},
		{"has scheme https", "https://app.example.com", "must not contain a scheme"},
		{"has spaces", "app .example.com", "must not contain whitespace"},
		{"has tab", "app\t.example.com", "must not contain whitespace"},
		{"has path", "app.example.com/path", "must not contain a path"},
		{"trailing dot only", ".", "invalid domain name"},
		{"starts with hyphen", "-app.example.com", "invalid domain name"},
		{"ends with hyphen", "app-.example.com", "invalid domain name"},
		{"double dot", "app..example.com", "invalid domain name"},
		{"single char tld", "app.x", "invalid domain name"},
		{"numeric tld", "app.123", "invalid domain name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDomain(tt.domain)
			if err == nil {
				t.Fatalf("ValidateDomain(%q) = nil, want error containing %q", tt.domain, tt.errMsg)
			}
			if got := err.Error(); !contains(got, tt.errMsg) {
				t.Errorf("ValidateDomain(%q) error = %q, want it to contain %q", tt.domain, got, tt.errMsg)
			}
		})
	}
}

func TestManager_AddDomain(t *testing.T) {
	m := NewManager(t.TempDir())

	if err := m.AddDomain("myapp", "app.example.com"); err != nil {
		t.Fatalf("AddDomain() = %v, want nil", err)
	}

	if !m.HasDomain("app.example.com") {
		t.Error("HasDomain() = false after AddDomain")
	}
}

func TestManager_RemoveDomain(t *testing.T) {
	m := NewManager(t.TempDir())
	_ = m.AddDomain("myapp", "app.example.com")

	m.RemoveDomain("app.example.com")

	if m.HasDomain("app.example.com") {
		t.Error("HasDomain() = true after RemoveDomain")
	}
}

func TestManager_RemoveDomain_NonExistent(t *testing.T) {
	m := NewManager(t.TempDir())
	// Should not panic
	m.RemoveDomain("nonexistent.example.com")
}

func TestManager_HasDomain(t *testing.T) {
	m := NewManager(t.TempDir())

	if m.HasDomain("app.example.com") {
		t.Error("HasDomain() = true for unregistered domain")
	}

	_ = m.AddDomain("myapp", "app.example.com")

	if !m.HasDomain("app.example.com") {
		t.Error("HasDomain() = false for registered domain")
	}
}

func TestManager_GetServiceForDomain(t *testing.T) {
	m := NewManager(t.TempDir())
	_ = m.AddDomain("myapp", "app.example.com")

	name, ok := m.GetServiceForDomain("app.example.com")
	if !ok {
		t.Fatal("GetServiceForDomain() ok = false, want true")
	}
	if name != "myapp" {
		t.Errorf("GetServiceForDomain() = %q, want %q", name, "myapp")
	}
}

func TestManager_GetServiceForDomain_NotFound(t *testing.T) {
	m := NewManager(t.TempDir())

	name, ok := m.GetServiceForDomain("unknown.example.com")
	if ok {
		t.Fatal("GetServiceForDomain() ok = true, want false")
	}
	if name != "" {
		t.Errorf("GetServiceForDomain() = %q, want empty string", name)
	}
}

func TestManager_AddDuplicate(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.AddDomain("svc1", "app.example.com"); err != nil {
		t.Fatalf("first AddDomain() = %v", err)
	}

	err := m.AddDomain("svc2", "app.example.com")
	if err == nil {
		t.Fatal("second AddDomain() = nil, want error for duplicate domain")
	}
	if !contains(err.Error(), "already registered") {
		t.Errorf("error = %q, want it to contain 'already registered'", err.Error())
	}
}

func TestManager_AddDomain_InvalidDomain(t *testing.T) {
	m := NewManager(t.TempDir())
	err := m.AddDomain("myapp", "not-a-domain")
	if err == nil {
		t.Fatal("AddDomain() with invalid domain = nil, want error")
	}
}

func TestManager_TLSConfig(t *testing.T) {
	m := NewManager(t.TempDir())

	tlsCfg := m.TLSConfig()
	if tlsCfg == nil {
		t.Fatal("TLSConfig() = nil")
	}
	if tlsCfg.GetCertificate == nil {
		t.Fatal("TLSConfig().GetCertificate = nil")
	}
}

func TestManager_TLSConfig_UnknownDomain(t *testing.T) {
	m := NewManager(t.TempDir())

	tlsCfg := m.TLSConfig()
	_, err := tlsCfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "unknown.example.com"})
	if err == nil {
		t.Fatal("GetCertificate() for unknown domain = nil, want error")
	}
	if !contains(err.Error(), "no certificate") {
		t.Errorf("error = %q, want it to contain 'no certificate'", err.Error())
	}
}

func TestManager_TLSConfig_NoProvider(t *testing.T) {
	m := NewManager(t.TempDir())
	_ = m.AddDomain("myapp", "app.example.com")

	tlsCfg := m.TLSConfig()
	_, err := tlsCfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "app.example.com"})
	if err == nil {
		t.Fatal("GetCertificate() without provider = nil, want error")
	}
	if !contains(err.Error(), "no certificate provider") {
		t.Errorf("error = %q, want it to contain 'no certificate provider'", err.Error())
	}
}

type stubCertProvider struct {
	cert *tls.Certificate
	err  error
}

func (s *stubCertProvider) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return s.cert, s.err
}

func TestManager_TLSConfig_WithProvider(t *testing.T) {
	m := NewManager(t.TempDir())
	_ = m.AddDomain("myapp", "app.example.com")

	expectedCert := &tls.Certificate{}
	m.SetProvider(&stubCertProvider{cert: expectedCert})

	tlsCfg := m.TLSConfig()
	cert, err := tlsCfg.GetCertificate(&tls.ClientHelloInfo{ServerName: "app.example.com"})
	if err != nil {
		t.Fatalf("GetCertificate() = %v, want nil", err)
	}
	if cert != expectedCert {
		t.Error("GetCertificate() returned unexpected certificate")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
