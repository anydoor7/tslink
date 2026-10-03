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
	"sync"
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

// Serialize read/modify/write operations from all publishers in this daemon.
var authHandoffFileMu sync.Mutex

const maxHandoffBytes = 64 << 10

// Entries are unique by Service and ordered by publication. Replacing an
// offer moves that node to the end; legacy consumers get the oldest offer.
type authHandoffDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	Entries       []authHandoffRecord `json:"entries"`
}

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
	authHandoffFileMu.Lock()
	defer authHandoffFileMu.Unlock()
	entries, err := loadAuthHandoffs(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	kept := make([]authHandoffRecord, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.DaemonPID == record.DaemonPID && entry.Service != record.Service {
			kept = append(kept, entry)
		}
	}
	return writeAuthHandoffs(path, append(kept, record))
}

// Caller holds authHandoffFileMu when modifying an existing document.
func writeAuthHandoffs(path string, entries []authHandoffRecord) error {
	data, err := json.MarshalIndent(authHandoffDocument{SchemaVersion: 2, Entries: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth handoff: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxHandoffBytes {
		return errors.New("auth handoff exceeds 64 KiB")
	}
	if err := atomicfile.WriteFile(path, data); err != nil {
		return fmt.Errorf("write auth handoff: %w", err)
	}
	return nil
}

func loadAuthHandoff(path string) (authHandoffRecord, error) {
	entries, err := loadAuthHandoffs(path)
	if err != nil {
		return authHandoffRecord{}, err
	}
	for _, entry := range entries {
		if entry.ExpiresAt.After(authHandoffNowFn()) {
			return entry, nil
		}
	}
	return entries[0], errAuthHandoffExpired
}

// Read expired records too: a terminal event must still retire its exact offer.
// Readers select unexpired entries using their own captured clock.
func loadAuthHandoffs(path string) ([]authHandoffRecord, error) {
	var entries []authHandoffRecord
	err := atomicfile.ReadSettled(path, func() error {
		return atomicfile.RetryFileOperation(func() error {
			var err error
			entries, err = loadAuthHandoffsOnce(path)
			return err
		})
	})
	return entries, err
}

func loadAuthHandoffsOnce(path string) ([]authHandoffRecord, error) {
	f, err := openAuthHandoff(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxHandoffBytes {
		return nil, errors.New("auth handoff must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxHandoffBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxHandoffBytes {
		return nil, errors.New("auth handoff exceeds 64 KiB")
	}
	var document authHandoffDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode auth handoff: %w", err)
	}
	switch document.SchemaVersion {
	case authHandoffSchemaVersion:
		var record authHandoffRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, fmt.Errorf("decode legacy auth handoff: %w", err)
		}
		document.Entries = []authHandoffRecord{record}
	case 2:
	default:
		return nil, fmt.Errorf("unsupported auth handoff schema_version %d", document.SchemaVersion)
	}
	if len(document.Entries) == 0 {
		return nil, errors.New("empty auth handoff document")
	}
	seen := make(map[string]bool, len(document.Entries))
	for _, record := range document.Entries {
		if record.SchemaVersion != authHandoffSchemaVersion || record.Status != authStatusNeedsLogin || strings.TrimSpace(record.AuthURL) == "" || record.DaemonPID <= 0 || seen[record.Service] {
			return nil, errors.New("invalid auth handoff record")
		}
		seen[record.Service] = true
	}
	return document.Entries, nil
}

func removeAuthHandoff(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// authHandoffMatchesService reports whether an interactive-enrollment handoff
// describes the queried service. A query for a different service must not
// be handed another service's authorization URL. A handoff with no service
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
	entries, loadErr := loadAuthHandoffs(filepath.Join(filepath.Dir(pidPath), "auth-handoff.json"))
	if loadErr != nil {
		return authHandoffRecord{}, false
	}
	pid, _ := readPIDFn(pidPath)
	for _, handoff := range entries {
		if pid > 0 && handoff.DaemonPID == pid && handoff.ExpiresAt.After(authHandoffNowFn()) && authHandoffMatchesService(handoff, name) {
			return handoff, true
		}
	}
	return authHandoffRecord{}, false
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
