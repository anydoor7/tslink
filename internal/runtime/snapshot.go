package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

const (
	SchemaVersion = inspect.SchemaVersion

	StatusExact            = "exact"
	StatusMissing          = "missing"
	StatusMalformed        = "malformed"
	StatusPIDMismatch      = "pid_mismatch"
	StatusRegistryMismatch = "registry_mismatch"
)

var (
	jsonMarshalIndent = json.MarshalIndent
	readFile          = os.ReadFile
	renameFile        = os.Rename
)

type Snapshot struct {
	SchemaVersion       string            `json:"schema_version"`
	DaemonPID           int               `json:"daemon_pid"`
	DaemonStartedAt     time.Time         `json:"daemon_started_at"`
	RegistryFingerprint string            `json:"registry_fingerprint"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Services            []ServiceSnapshot `json:"services"`
}

type ServiceSnapshot struct {
	Name        string               `json:"name"`
	Type        string               `json:"type"`
	Endpoint    inspect.EndpointView `json:"endpoint"`
	Exposure    inspect.ExposureView `json:"exposure"`
	CertDomains []string             `json:"cert_domains,omitempty"`
}

type ServiceState struct {
	Service     registry.Service
	CertDomains []string
}

type SnapshotError struct {
	Status string
	Code   string
	Err    error
}

func (e *SnapshotError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *SnapshotError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *SnapshotError) StableCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

type Freshness struct {
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Exact   bool   `json:"exact"`
	Message string `json:"message,omitempty"`
}

func NewSnapshot(daemonPID int, daemonStartedAt time.Time, registryFingerprint string, updatedAt time.Time, states []ServiceState) Snapshot {
	sorted := append([]ServiceState(nil), states...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Service.Name < sorted[j].Service.Name
	})

	services := make([]ServiceSnapshot, 0, len(sorted))
	for _, state := range sorted {
		view := inspect.ServiceViewFor(state.Service)
		endpoint := view.Endpoint
		endpoint.State = inspect.EndpointStateExact
		if len(state.CertDomains) > 0 && (state.Service.Type == registry.TypeProxy || state.Service.Type == registry.TypeFile) {
			endpoint.Display = "https://" + state.CertDomains[0]
			endpoint.Host = state.CertDomains[0]
		}
		certDomains := append([]string(nil), state.CertDomains...)
		services = append(services, ServiceSnapshot{
			Name:        state.Service.Name,
			Type:        state.Service.Type,
			Endpoint:    endpoint,
			Exposure:    view.Exposure,
			CertDomains: certDomains,
		})
	}

	return Snapshot{
		SchemaVersion:       SchemaVersion,
		DaemonPID:           daemonPID,
		DaemonStartedAt:     daemonStartedAt.UTC(),
		RegistryFingerprint: registryFingerprint,
		UpdatedAt:           updatedAt.UTC(),
		Services:            services,
	}
}

func RegistryFingerprint(reg *registry.Registry) (string, error) {
	if reg == nil {
		reg = &registry.Registry{}
	}
	canonical := registry.Registry{
		Services: append([]registry.Service(nil), reg.Services...),
	}
	if canonical.Services == nil {
		canonical.Services = []registry.Service{}
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func Save(path string, snapshot Snapshot) error {
	snapshot.SchemaVersion = SchemaVersion
	if snapshot.Services == nil {
		snapshot.Services = []ServiceSnapshot{}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := jsonMarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := renameFile(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func Load(path string) (*Snapshot, error) {
	snapshot, err := loadOnce(path)
	if err == nil {
		return snapshot, nil
	}
	if !retryableReadError(err) {
		return nil, err
	}
	snapshot, retryErr := loadOnce(path)
	if retryErr == nil {
		return snapshot, nil
	}
	return nil, retryErr
}

func Remove(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func Classify(snapshot *Snapshot, loadErr error, expectedPID int, currentRegistryFingerprint string) Freshness {
	if loadErr != nil {
		var snapshotErr *SnapshotError
		if errors.As(loadErr, &snapshotErr) {
			return Freshness{
				Status:  snapshotErr.Status,
				Code:    snapshotErr.Code,
				Exact:   false,
				Message: snapshotErr.Error(),
			}
		}
		return Freshness{
			Status:  StatusMalformed,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: loadErr.Error(),
		}
	}
	if snapshot == nil {
		return Freshness{
			Status:  StatusMissing,
			Code:    inspect.WarningCodeRuntimeSnapshotMissing,
			Exact:   false,
			Message: "runtime snapshot is missing",
		}
	}
	if snapshot.DaemonPID != expectedPID {
		return Freshness{
			Status:  StatusPIDMismatch,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: fmt.Sprintf("runtime snapshot daemon PID %d does not match expected PID %d", snapshot.DaemonPID, expectedPID),
		}
	}
	if snapshot.RegistryFingerprint != currentRegistryFingerprint {
		return Freshness{
			Status:  StatusRegistryMismatch,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: "runtime snapshot registry fingerprint does not match current registry",
		}
	}
	return Freshness{
		Status: StatusExact,
		Exact:  true,
	}
}

func loadOnce(path string) (*Snapshot, error) {
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &SnapshotError{
				Status: StatusMissing,
				Code:   inspect.WarningCodeRuntimeSnapshotMissing,
				Err:    err,
			}
		}
		return nil, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, malformedError(err)
	}
	if snapshot.SchemaVersion != SchemaVersion {
		return nil, malformedError(fmt.Errorf("unsupported runtime snapshot schema_version %q", snapshot.SchemaVersion))
	}
	if snapshot.Services == nil {
		snapshot.Services = []ServiceSnapshot{}
	}
	return &snapshot, nil
}

func malformedError(err error) error {
	return &SnapshotError{
		Status: StatusMalformed,
		Code:   inspect.WarningCodeRuntimeSnapshotStale,
		Err:    err,
	}
}

func retryableReadError(err error) bool {
	var snapshotErr *SnapshotError
	if !errors.As(err, &snapshotErr) || snapshotErr.Status != StatusMalformed {
		return false
	}
	var syntaxErr *json.SyntaxError
	return errors.As(snapshotErr.Err, &syntaxErr) ||
		errors.Is(snapshotErr.Err, io.ErrUnexpectedEOF) ||
		errors.Is(snapshotErr.Err, io.EOF)
}
