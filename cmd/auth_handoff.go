package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
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

var errAuthHandoffExpired = errors.New("auth handoff expired")

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
	return newAuthHandoffRecordAt(service, authURL, daemonPID, authHandoffNowFn())
}

func newAuthHandoffRecordAt(service, authURL string, daemonPID int, now time.Time) authHandoffRecord {
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
	f, err := openAuthHandoff(path)
	if err != nil {
		return authHandoffRecord{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return authHandoffRecord{}, err
	}
	const maxHandoffBytes = 64 << 10
	if !info.Mode().IsRegular() || info.Size() > maxHandoffBytes {
		return authHandoffRecord{}, errors.New("auth handoff must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHandoffBytes+1))
	if err != nil {
		return authHandoffRecord{}, err
	}
	if len(data) > maxHandoffBytes {
		return authHandoffRecord{}, errors.New("auth handoff exceeds 64 KiB")
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
		return record, errAuthHandoffExpired
	}
	return record, nil
}

func removeAuthHandoff(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// authHandoffMatchesService reports whether an interactive-enrollment handoff
// describes the queried service. The handoff file is a daemon-wide singleton:
// when several services are enrolling, a query for a different service must not
// be handed the first service's authorization URL. A handoff with no service
// recorded is treated as unbound and may satisfy any query.
func authHandoffMatchesService(handoff authHandoffRecord, name string) bool {
	return handoff.Service == "" || handoff.Service == name
}

// validAuthHandoffForService loads the daemon's auth handoff and reports
// whether it is a current, service-matching enrollment offer: the file must
// load, name a live daemon PID, still be unexpired, and either describe the
// queried service or be unbound. Callers use the returned record for the
// enrollment URL; ok=false means fall through to the plain url_not_ready path.
func validAuthHandoffForService(pidPath, name string) (authHandoffRecord, bool) {
	handoff, loadErr := loadAuthHandoff(filepath.Join(filepath.Dir(pidPath), "auth-handoff.json"))
	if loadErr != nil {
		return authHandoffRecord{}, false
	}
	pid, _ := readPIDFn(pidPath)
	if pid <= 0 || handoff.DaemonPID != pid || !handoff.ExpiresAt.After(time.Now()) {
		return authHandoffRecord{}, false
	}
	if !authHandoffMatchesService(handoff, name) {
		return authHandoffRecord{}, false
	}
	return handoff, true
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
