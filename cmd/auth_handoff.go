package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
)

const (
	authHandoffSchemaVersion   = 1
	authStatusAuthenticated    = "authenticated"
	authStatusNeedsLogin       = "needs_login"
	authStatusNotAuthenticated = "not_authenticated"

	// Tailscale currently refreshes interactive URLs that are approaching seven
	// days old. TSLink publishes a conservative six-day client retry boundary
	// because the exact control-plane expiry timestamp is not exposed by Status.
	authHandoffConservativeLifetime = 6 * 24 * time.Hour
)

var authHandoffNowFn = func() time.Time { return time.Now().UTC() }

type authHandoffRecord struct {
	SchemaVersion int       `json:"schema_version"`
	Status        string    `json:"status"`
	Service       string    `json:"service,omitempty"`
	AuthURL       string    `json:"auth_url"`
	ExpiresAt     time.Time `json:"expires_at"`
	Poll          string    `json:"poll"`
	DaemonPID     int       `json:"daemon_pid"`
}

type serveAuthResult struct {
	Status             string    `json:"status"`
	AuthURL            string    `json:"auth_url"`
	ExpiresAt          time.Time `json:"expires_at"`
	Poll               string    `json:"poll"`
	CredentialMigrated bool      `json:"credential_migrated,omitempty"`
}

func newAuthHandoffRecord(service, authURL string, daemonPID int) authHandoffRecord {
	now := authHandoffNowFn()
	return authHandoffRecord{
		SchemaVersion: authHandoffSchemaVersion,
		Status:        authStatusNeedsLogin,
		Service:       service,
		AuthURL:       authURL,
		ExpiresAt:     now.Add(authHandoffConservativeLifetime),
		Poll:          "tslink status --json",
		DaemonPID:     daemonPID,
	}
}

func (r authHandoffRecord) serveResult() serveAuthResult {
	return serveAuthResult{
		Status:    r.Status,
		AuthURL:   r.AuthURL,
		ExpiresAt: r.ExpiresAt,
		Poll:      r.Poll,
	}
}

func saveAuthHandoff(path string, record authHandoffRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth handoff: %w", err)
	}
	data = append(data, '\n')
	if err := atomicfile.WriteFile(path, data); err != nil {
		return fmt.Errorf("write auth handoff: %w", err)
	}
	return nil
}

func loadAuthHandoff(path string) (authHandoffRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return authHandoffRecord{}, err
	}
	var record authHandoffRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return authHandoffRecord{}, fmt.Errorf("decode auth handoff: %w", err)
	}
	if record.SchemaVersion != authHandoffSchemaVersion {
		return authHandoffRecord{}, fmt.Errorf("unsupported auth handoff schema_version %d", record.SchemaVersion)
	}
	if record.Status != authStatusNeedsLogin || strings.TrimSpace(record.AuthURL) == "" || record.DaemonPID <= 0 {
		return authHandoffRecord{}, errors.New("invalid auth handoff record")
	}
	if !record.ExpiresAt.After(authHandoffNowFn()) {
		return authHandoffRecord{}, errors.New("auth handoff expired")
	}
	return record, nil
}

func removeAuthHandoff(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func openBrowser(authURL string) error {
	parsed, err := url.Parse(authURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("refusing to open invalid authentication URL")
	}

	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{authURL}
	case "linux":
		name, args = "xdg-open", []string{authURL}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", authURL}
	default:
		return fmt.Errorf("browser opening is unsupported on %s", runtime.GOOS)
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func ciEnvironmentSet() bool {
	_, ok := os.LookupEnv("CI")
	return ok
}
