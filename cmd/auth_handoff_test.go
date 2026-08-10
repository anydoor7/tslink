package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAuthHandoffRoundTripUsesPrivateVersionedRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth-handoff.json")
	fixedNow := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	oldNow := authHandoffNowFn
	authHandoffNowFn = func() time.Time { return fixedNow }
	t.Cleanup(func() { authHandoffNowFn = oldNow })

	want := newAuthHandoffRecord("web", "https://login.tailscale.com/a/round-trip", 4242)
	if err := saveAuthHandoff(path, want); err != nil {
		t.Fatalf("saveAuthHandoff() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("auth handoff mode = %#o, want 0600", got)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("auth handoff mode = %v, want regular file on Windows", info.Mode())
	}

	got, err := loadAuthHandoff(path)
	if err != nil {
		t.Fatalf("loadAuthHandoff() error = %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if got.ExpiresAt.Sub(fixedNow) != authHandoffConservativeLifetime {
		t.Fatalf("expiry lifetime = %s, want %s", got.ExpiresAt.Sub(fixedNow), authHandoffConservativeLifetime)
	}
}

func TestAuthHandoffRejectsExpiredAndInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth-handoff.json")
	fixedNow := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	oldNow := authHandoffNowFn
	authHandoffNowFn = func() time.Time { return fixedNow }
	t.Cleanup(func() { authHandoffNowFn = oldNow })

	expired := newAuthHandoffRecord("web", "https://login.tailscale.com/a/expired", 4242)
	expired.ExpiresAt = fixedNow
	if err := saveAuthHandoff(path, expired); err != nil {
		t.Fatalf("save expired handoff: %v", err)
	}
	if _, err := loadAuthHandoff(path); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("load expired error = %v, want expired", err)
	}

	if err := os.WriteFile(path, []byte(`{"schema_version":1,"status":"needs_login","auth_url":"","daemon_pid":4242}`), 0o600); err != nil {
		t.Fatalf("write invalid handoff: %v", err)
	}
	if _, err := loadAuthHandoff(path); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("load invalid error = %v, want invalid record", err)
	}
}

func TestOpenBrowserRejectsNonHTTPURLBeforeLaunching(t *testing.T) {
	if err := openBrowser("file:///tmp/not-an-auth-url"); err == nil || !strings.Contains(err.Error(), "invalid authentication URL") {
		t.Fatalf("openBrowser() error = %v, want invalid authentication URL", err)
	}
}
