package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

const (
	// SchemaVersion is the version of the runtime.json shape, the file the
	// daemon writes for the CLI. It is independent of the public view schema
	// (inspect.SchemaVersion), so a change to the public views never makes a
	// running daemon's snapshot unreadable to a newer CLI. Readers accept a
	// snapshot of this version and ignore fields they do not know: a daemon
	// may add fields without raising it, and raises it only for a shape an
	// older reader cannot read.
	SchemaVersion = 1

	// legacySchemaVersion is what builds before SchemaVersion existed wrote in
	// schema_version, the public view schema of the time. It is version 1.
	legacySchemaVersion = "vnext.1"

	StatusExact            = "exact"
	StatusMissing          = "missing"
	StatusMalformed        = "malformed"
	StatusUnreadable       = "unreadable"
	StatusStale            = "stale"
	StatusPartial          = "partial"
	StatusPIDMismatch      = "pid_mismatch"
	StatusRegistryMismatch = "registry_mismatch"

	ServiceRuntimeRunning = "running"
	ServiceRuntimeFailed  = "failed"

	FunnelStateNotRequested      = "not_requested"
	FunnelStateRequestedUnknown  = "requested_unverified"
	FunnelStateActive            = "active"
	FunnelStateCapabilityMissing = "capability_missing"
	FunnelStateListenFailed      = "listen_failed"
	FunnelStateStartTimeout      = "startup_timeout"
)

var (
	jsonMarshalIndent = json.MarshalIndent
	readFile          = os.ReadFile
	renameFile        = os.Rename
)

// SnapshotVersion is runtime.json's schema_version. It reads the string
// that builds before the integer version wrote as version 1.
type SnapshotVersion int

func (v *SnapshotVersion) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		if legacy != legacySchemaVersion {
			return fmt.Errorf("unsupported runtime snapshot schema_version %q", legacy)
		}
		*v = 1
		return nil
	}
	var version int
	if err := json.Unmarshal(data, &version); err != nil {
		return fmt.Errorf("runtime snapshot schema_version: %w", err)
	}
	*v = SnapshotVersion(version)
	return nil
}

// PortalState is runtime evidence for the independent Tailnet portal node.
type PortalState struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
	State    string `json:"state"`
	URL      string `json:"url,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Snapshot struct {
	AccessLog           *accesslog.Health `json:"access_log,omitempty"`
	Portal              PortalState       `json:"portal"`
	Alerts              health.AlertsView `json:"alerts"`
	SchemaVersion       SnapshotVersion   `json:"schema_version"`
	DaemonPID           int               `json:"daemon_pid"`
	DaemonStartedAt     time.Time         `json:"daemon_started_at"`
	RegistryFingerprint string            `json:"registry_fingerprint"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Partial             bool              `json:"partial,omitempty"`
	GlobalError         *ServiceError     `json:"global_error,omitempty"`
	Services            []ServiceSnapshot `json:"services"`
}

type ServiceSnapshot struct {
	Health   health.State          `json:"health"`
	NodeKey  health.Expiry         `json:"node_key"`
	Warnings []inspect.WarningView `json:"warnings,omitempty"`

	Name            string               `json:"name"`
	Type            string               `json:"type"`
	NodeID          string               `json:"node_id,omitempty"`
	RuntimeState    string               `json:"runtime_state"`
	Endpoint        inspect.EndpointView `json:"endpoint"`
	Exposure        inspect.ExposureView `json:"exposure"`
	FunnelRequested bool                 `json:"funnel_requested"`
	FunnelActive    bool                 `json:"funnel_active"`
	FunnelState     string               `json:"funnel_state"`
	Error           *ServiceError        `json:"error,omitempty"`
	CertDomains     []string             `json:"cert_domains,omitempty"`
}

type ServiceState struct {
	Health       health.State
	NodeKey      health.Expiry
	Warnings     []inspect.WarningView
	Service      registry.Service
	NodeID       string
	RuntimeHost  string
	RuntimeState string
	FunnelState  string
	Error        *ServiceError
	CertDomains  []string
}

// ServiceError is stable, actionable failure data persisted for agent consumers.
type ServiceError struct {
	Code      string                     `json:"code"`
	Message   string                     `json:"message"`
	Next      []string                   `json:"next,omitempty"`
	Provision *registry.ProvisionOutcome `json:"provision,omitempty"`
}

type ExpectedRuntime struct {
	DaemonPID                  int
	DaemonStartedAtLowerBound  time.Time
	CurrentRegistryFingerprint string
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
	return newSnapshot(daemonPID, daemonStartedAt, registryFingerprint, updatedAt, states, false)
}

// NewPartialSnapshot returns non-authoritative progress evidence produced while
// a registry synchronization is still starting services. Consumers may use
// services present in it as positive evidence, but absence is not evidence that
// any other registered service is down or missing.
func NewPartialSnapshot(daemonPID int, daemonStartedAt time.Time, registryFingerprint string, updatedAt time.Time, states []ServiceState) Snapshot {
	return newSnapshot(daemonPID, daemonStartedAt, registryFingerprint, updatedAt, states, true)
}

func newSnapshot(daemonPID int, daemonStartedAt time.Time, registryFingerprint string, updatedAt time.Time, states []ServiceState, partial bool) Snapshot {
	sorted := append([]ServiceState(nil), states...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Service.Name < sorted[j].Service.Name
	})

	services := make([]ServiceSnapshot, 0, len(sorted))
	for _, state := range sorted {
		view := inspect.ServiceViewFor(state.Service)
		endpoint := view.Endpoint
		endpoint = applyRuntimeHost(endpoint, state.Service, state.RuntimeHost)
		if state.Service.Type == registry.TypeProxy || state.Service.Type == registry.TypeFile {
			if host := registry.CanonicalProxyHost(state.CertDomains, state.RuntimeHost); host != "" {
				endpoint.Display = "https://" + host
				endpoint.Host = host
			} else if len(state.CertDomains) > 0 {
				endpoint.Display = ""
				endpoint.Host = ""
			}
		}
		endpoint.State = endpointState(endpoint)
		runtimeState := state.RuntimeState
		if runtimeState == "" {
			runtimeState = ServiceRuntimeRunning
		}
		funnelState := state.FunnelState
		if funnelState == "" {
			switch {
			case !state.Service.Funnel:
				funnelState = FunnelStateNotRequested
			case runtimeState == ServiceRuntimeRunning:
				funnelState = FunnelStateActive
			default:
				funnelState = FunnelStateRequestedUnknown
			}
		}
		certDomains := append([]string(nil), state.CertDomains...)
		var serviceError *ServiceError
		if state.Error != nil {
			copied := *state.Error
			copied.Next = append([]string(nil), state.Error.Next...)
			if state.Error.Provision != nil {
				provision := *state.Error.Provision
				copied.Provision = &provision
			}
			serviceError = &copied
		}
		services = append(services, ServiceSnapshot{
			Health:          state.Health,
			NodeKey:         state.NodeKey,
			Warnings:        append([]inspect.WarningView(nil), state.Warnings...),
			Name:            state.Service.Name,
			Type:            state.Service.Type,
			NodeID:          state.NodeID,
			RuntimeState:    runtimeState,
			Endpoint:        endpoint,
			Exposure:        view.Exposure,
			FunnelRequested: state.Service.Funnel,
			FunnelActive:    funnelState == FunnelStateActive,
			FunnelState:     funnelState,
			Error:           serviceError,
			CertDomains:     certDomains,
		})
	}

	return Snapshot{
		SchemaVersion:       SchemaVersion,
		DaemonPID:           daemonPID,
		DaemonStartedAt:     daemonStartedAt.UTC(),
		RegistryFingerprint: registryFingerprint,
		UpdatedAt:           updatedAt.UTC(),
		Partial:             partial,
		Services:            services,
	}
}

// RegistryFingerprint is the one derivation of the registry fingerprint: the
// daemon writes it into runtime.json and the CLI compares against it. Its
// input is every service entry registry.LoadForRuntime decodes, in file
// order, the valid ones and the ones isolated as per-service issues alike.
// The daemon passes what it loaded; the CLI calls CurrentRegistryFingerprint.
// A fingerprint over only the valid entries on one side would call a snapshot
// stale for as long as any service has an issue, which says nothing about
// whether the daemon applied the latest registry.
func RegistryFingerprint(reg *registry.Registry, issues []registry.ServiceIssue) (string, error) {
	var valid []registry.Service
	if reg != nil {
		valid = reg.Services
	}
	isolated := append([]registry.ServiceIssue(nil), issues...)
	sort.SliceStable(isolated, func(i, j int) bool { return isolated[i].Index < isolated[j].Index })
	canonical := registry.Registry{Services: make([]registry.Service, 0, len(valid)+len(isolated))}
	if reg != nil {
		canonical.Portal = reg.Portal
	}
	for len(valid) > 0 || len(isolated) > 0 {
		if len(isolated) > 0 && (len(valid) == 0 || isolated[0].Index <= len(canonical.Services)) {
			canonical.Services = append(canonical.Services, isolated[0].Service)
			isolated = isolated[1:]
			continue
		}
		canonical.Services = append(canonical.Services, valid[0])
		valid = valid[1:]
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// CurrentRegistryFingerprint fingerprints registry.json at path the way the
// daemon does, for comparison with the fingerprint in runtime.json.
func CurrentRegistryFingerprint(path string) (string, error) {
	reg, issues, err := registry.LoadForRuntime(path)
	if err != nil {
		return "", err
	}
	return RegistryFingerprint(reg, issues)
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

func Classify(snapshot *Snapshot, loadErr error, expected ExpectedRuntime) Freshness {
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
			Status:  StatusUnreadable,
			Code:    inspect.WarningCodeRuntimeSnapshotUnreadable,
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
	if expected.DaemonPID <= 0 || expected.DaemonStartedAtLowerBound.IsZero() || expected.CurrentRegistryFingerprint == "" {
		return Freshness{
			Status:  StatusStale,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: "runtime snapshot exactness requires expected daemon PID, daemon freshness lower bound, and registry fingerprint",
		}
	}
	if snapshot.DaemonPID != expected.DaemonPID {
		return Freshness{
			Status:  StatusPIDMismatch,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: fmt.Sprintf("runtime snapshot daemon PID %d does not match expected PID %d", snapshot.DaemonPID, expected.DaemonPID),
		}
	}
	lowerBound := expected.DaemonStartedAtLowerBound.UTC()
	if snapshot.DaemonStartedAt.Before(lowerBound) || snapshot.UpdatedAt.Before(lowerBound) {
		return Freshness{
			Status:  StatusStale,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: "runtime snapshot predates expected daemon freshness lower bound",
		}
	}
	if snapshot.RegistryFingerprint != expected.CurrentRegistryFingerprint {
		return Freshness{
			Status:  StatusRegistryMismatch,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: "runtime snapshot registry fingerprint does not match current registry",
		}
	}
	if snapshot.Partial {
		return Freshness{
			Status:  StatusPartial,
			Code:    inspect.WarningCodeRuntimeSnapshotStale,
			Exact:   false,
			Message: "runtime snapshot is partial while registry synchronization is in progress",
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
		return nil, unreadableError(err)
	}
	var header struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, malformedError(err)
	}
	var version SnapshotVersion
	if err := version.UnmarshalJSON(header.SchemaVersion); err != nil {
		return nil, incompatibleError(err)
	}
	if version != SchemaVersion {
		return nil, incompatibleError(fmt.Errorf("runtime snapshot schema_version %d is not %d, the version this build reads", version, SchemaVersion))
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, malformedError(err)
	}
	if snapshot.Services == nil {
		snapshot.Services = []ServiceSnapshot{}
	}
	return &snapshot, nil
}

// malformedError and incompatibleError report a snapshot this build cannot
// read. That is not staleness, which is a readable snapshot for another daemon
// or registry, so both carry the unreadable code.
func malformedError(err error) error {
	return &SnapshotError{
		Status: StatusMalformed,
		Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
		Err:    err,
	}
}

func incompatibleError(err error) error {
	return &SnapshotError{
		Status: StatusUnreadable,
		Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
		Err:    err,
	}
}

func unreadableError(err error) error {
	return &SnapshotError{
		Status: StatusUnreadable,
		Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
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

func applyRuntimeHost(endpoint inspect.EndpointView, svc registry.Service, runtimeHost string) inspect.EndpointView {
	runtimeHost = normalizeRuntimeHost(runtimeHost)
	if runtimeHost == "" {
		return endpoint
	}

	endpoint.Host = runtimeHost
	switch svc.Type {
	case registry.TypeTCP:
		if endpoint.Port > 0 {
			endpoint.Display = net.JoinHostPort(runtimeHost, strconv.Itoa(endpoint.Port))
		} else {
			endpoint.Display = runtimeHost
		}
	case registry.TypeProxy, registry.TypeFile:
		endpoint.Display = "https://" + runtimeHost
	default:
		endpoint.Display = runtimeHost
	}
	return endpoint
}

func normalizeRuntimeHost(host string) string {
	return strings.TrimSuffix(strings.TrimSpace(host), ".")
}

func endpointState(endpoint inspect.EndpointView) string {
	if endpoint.Display == "" || endpoint.Host == "" {
		return inspect.EndpointStateExpected
	}
	if strings.Contains(endpoint.Display, "<tailnet>") || strings.Contains(endpoint.Host, "<tailnet>") {
		return inspect.EndpointStateExpected
	}
	return inspect.EndpointStateExact
}
