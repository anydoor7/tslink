package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	readPIDFn = daemon.ReadPID

	statusPIDPathFn             = config.PIDPath
	statusRegistryPathFn        = config.RegistryPath
	statusRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	statusAuthHandoffPathFn     = config.AuthHandoffPath
	runtimeLoadSnapshotFn       = tsruntime.Load
	statusLoadAuthHandoffFn     = loadAuthHandoff
	statusNowFn                 = time.Now
	statusGetClientSecretFn     = credentials.GetClientSecret
	// statusCredentialInventoryFn classifies the stored credential slots and
	// persists any missing value-free metadata (backfill). Tests replace it to
	// stay off the filesystem.
	statusCredentialInventoryFn = func(values credentials.SlotValues, now time.Time) credentials.Inventory {
		return credentials.DescribeSlots(values, now, true)
	}
	pidFileModTimeFn = func(path string) (time.Time, error) {
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, err
		}
		return info.ModTime(), nil
	}
)

const (
	statusEndpointStateExpectedUnverified = "expected_unverified"
	statusEndpointStateStale              = "stale"
	statusEndpointStateMissing            = "missing"
	statusEndpointStateUnknown            = "unknown"
	daemonStateRunning                    = "running"
	daemonStateAbsent                     = "absent"
	daemonStateUnknown                    = "unknown"
)

// StatusResult holds the status information for display.
type StatusResult struct {
	DaemonRunning           bool                    `json:"daemon_running"`
	DaemonState             string                  `json:"daemon_state"`
	DaemonPID               int                     `json:"daemon_pid"`
	OwnershipProofAvailable bool                    `json:"ownership_proof_available"`
	Authenticated           bool                    `json:"authenticated"`
	CredentialStored        bool                    `json:"credential_stored"`
	Credentials             StatusCredentials       `json:"credentials"`
	CredentialExpiryState   string                  `json:"credential_expiry_state"`
	NodeAuthorized          bool                    `json:"node_authorized"`
	AuthorizedServiceCount  int                     `json:"authorized_service_count"`
	AuthStatus              string                  `json:"auth_status"`
	AuthURL                 string                  `json:"auth_url,omitempty"`
	ExpiresAt               *time.Time              `json:"expires_at,omitempty"`
	Next                    []string                `json:"next,omitempty"`
	GlobalError             *tsruntime.ServiceError `json:"global_error,omitempty"`
	ServiceCount            int                     `json:"service_count"`
	Services                []StatusServiceState    `json:"services"`
}

// StatusCredentials is the value-free per-slot credential report. It carries
// fingerprints and timestamps, never credential material.
type StatusCredentials struct {
	APIKey       StatusCredentialSlot `json:"api_key"`
	ClientSecret StatusCredentialSlot `json:"client_secret"`
	// MetadataError is set when credential-meta.json exists but is unreadable;
	// present slots then report expiry_state=unknown.
	MetadataError string `json:"metadata_error,omitempty"`
}

// StatusCredentialSlot describes one credential slot.
type StatusCredentialSlot struct {
	Present            bool       `json:"present"`
	Fingerprint        string     `json:"fingerprint,omitempty"`
	StoredAt           *time.Time `json:"stored_at,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	ExpiresAtSource    string     `json:"expires_at_source,omitempty"`
	DaysLeft           *int       `json:"days_left,omitempty"`
	ExpiryState        string     `json:"expiry_state"`
	LastVerifiedAt     *time.Time `json:"last_verified_at,omitempty"`
	LastVerifiedResult string     `json:"last_verified_result,omitempty"`
}

type StatusServiceState struct {
	Name            string                  `json:"name"`
	Status          string                  `json:"status"`
	OwnershipProof  bool                    `json:"ownership_proof"`
	FunnelRequested bool                    `json:"funnel_requested"`
	FunnelActive    bool                    `json:"funnel_active"`
	FunnelState     string                  `json:"funnel_state"`
	FunnelExpiresAt *time.Time              `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                 `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError `json:"error,omitempty"`
}

type StatusURLsResult struct {
	SchemaVersion           string                      `json:"schema_version"`
	DaemonRunning           bool                        `json:"daemon_running"`
	DaemonState             string                      `json:"daemon_state"`
	DaemonPID               int                         `json:"daemon_pid"`
	OwnershipProofAvailable bool                        `json:"ownership_proof_available"`
	Authenticated           bool                        `json:"authenticated"`
	CredentialStored        bool                        `json:"credential_stored"`
	Credentials             StatusCredentials           `json:"credentials"`
	CredentialExpiryState   string                      `json:"credential_expiry_state"`
	NodeAuthorized          bool                        `json:"node_authorized"`
	AuthorizedServiceCount  int                         `json:"authorized_service_count"`
	AuthStatus              string                      `json:"auth_status"`
	AuthURL                 string                      `json:"auth_url,omitempty"`
	ExpiresAt               *time.Time                  `json:"expires_at,omitempty"`
	Next                    []string                    `json:"next,omitempty"`
	GlobalError             *tsruntime.ServiceError     `json:"global_error,omitempty"`
	ServiceCount            int                         `json:"service_count"`
	RuntimeSnapshot         StatusRuntimeSnapshotResult `json:"runtime_snapshot"`
	Services                []StatusServiceView         `json:"services"`
}

type StatusRuntimeSnapshotResult struct {
	Status              string     `json:"status"`
	Code                string     `json:"code,omitempty"`
	Exact               bool       `json:"exact"`
	Message             string     `json:"message,omitempty"`
	UpdatedAt           *time.Time `json:"updated_at,omitempty"`
	DaemonPID           int        `json:"daemon_pid,omitempty"`
	RegistryFingerprint string     `json:"registry_fingerprint,omitempty"`
}

type StatusServiceView struct {
	Name            string                  `json:"name"`
	Type            string                  `json:"type"`
	RuntimeState    string                  `json:"runtime_state"`
	OwnershipProof  bool                    `json:"ownership_proof"`
	Endpoint        inspect.EndpointView    `json:"endpoint"`
	Exposure        inspect.ExposureView    `json:"exposure"`
	FunnelRequested bool                    `json:"funnel_requested"`
	FunnelActive    bool                    `json:"funnel_active"`
	FunnelState     string                  `json:"funnel_state"`
	FunnelExpiresAt *time.Time              `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                 `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError `json:"error,omitempty"`
	Allow           inspect.SummaryView     `json:"allow"`
	Tags            inspect.SummaryView     `json:"tags"`
	Backend         inspect.BackendView     `json:"backend"`
	Warnings        []inspect.WarningView   `json:"warnings,omitempty"`
}

func getStatus(pidPath, regPath string) (StatusResult, error) {
	r := baseStatus(pidPath)
	reg, err := registry.Load(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	r.ServiceCount = len(reg.Services)
	r.Services = make([]StatusServiceState, 0, len(reg.Services))
	ownershipProofs, ownershipProofAvailable := ownershipProofsForRegistry(regPath)
	r.OwnershipProofAvailable = ownershipProofAvailable
	for _, svc := range reg.Services {
		now := statusNowFn()
		effective := registry.EffectiveServiceAt(svc, now)
		r.Services = append(r.Services, StatusServiceState{
			Name:            svc.Name,
			Status:          "down",
			OwnershipProof:  ownershipProofs[svc.Name],
			FunnelRequested: effective.Funnel,
			FunnelState:     configuredFunnelState(effective.Funnel),
			FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
			FunnelRemaining: registry.FunnelRemainingAt(svc, now),
		})
	}
	setStatusContinuation(&r)
	return r, nil
}

func ownershipProofsForRegistry(_ string) (map[string]bool, bool) {
	proofs := make(map[string]bool)
	ledgerPath, err := config.NodeOwnershipPath()
	if err != nil {
		return proofs, false
	}
	ledger, err := tsruntime.LoadOwnership(ledgerPath)
	if err != nil {
		return proofs, false
	}
	for _, node := range ledger.Nodes {
		proofs[node.ServiceName] = true
	}
	return proofs, true
}

func baseStatus(pidPath string) StatusResult {
	r := StatusResult{DaemonState: daemonStateUnknown, AuthStatus: authStatusNotAuthenticated, Services: []StatusServiceState{}}
	if isRunningFn(pidPath) {
		r.DaemonRunning = true
		r.DaemonState = daemonStateRunning
		r.DaemonPID, _ = readPIDFn(pidPath)
	} else if isProcessAbsentFromPIDFileFn(pidPath) {
		r.DaemonState = daemonStateAbsent
	}
	values := credentials.SlotValues{}
	values.APIKey, _ = getAPIKeyFn()
	hasClientSecret := hasClientSecretFn()
	if hasClientSecret {
		values.ClientSecret, _ = statusGetClientSecretFn()
	}
	if values.APIKey != "" || hasClientSecret {
		r.CredentialStored = true
		r.Authenticated = true
		r.AuthStatus = authStatusAuthenticated
	}
	r.Credentials, r.CredentialExpiryState = statusCredentialsFromInventory(statusCredentialInventoryFn(values, statusNowFn()), hasClientSecret)
	return r
}

func statusCredentialSlot(view credentials.SlotView) StatusCredentialSlot {
	slot := StatusCredentialSlot{Present: view.Present, ExpiryState: view.ExpiryState}
	if view.Metadata != nil {
		slot.Fingerprint = view.Metadata.Fingerprint
		storedAt := view.Metadata.StoredAt.UTC()
		slot.StoredAt = &storedAt
		slot.ExpiresAt = cloneTimePointer(view.Metadata.ExpiresAt)
		slot.ExpiresAtSource = view.Metadata.ExpiresAtSource
		slot.LastVerifiedAt = cloneTimePointer(view.Metadata.LastVerifiedAt)
		slot.LastVerifiedResult = view.Metadata.LastVerifiedResult
	}
	if view.DaysLeft != nil {
		days := *view.DaysLeft
		slot.DaysLeft = &days
	}
	return slot
}

// statusCredentialsFromInventory maps the credentials inventory onto the wire
// shape. clientSecretPresent covers the case where presence is known but the
// value could not be re-read for fingerprinting: the slot stays present with an
// unknown expiry instead of silently vanishing.
func statusCredentialsFromInventory(inventory credentials.Inventory, clientSecretPresent bool) (StatusCredentials, string) {
	result := StatusCredentials{
		APIKey:       statusCredentialSlot(inventory.APIKey),
		ClientSecret: statusCredentialSlot(inventory.ClientSecret),
	}
	if inventory.MetadataError != nil {
		result.MetadataError = sanitizeDoctorEvidenceValue(inventory.MetadataError.Error())
	}
	if clientSecretPresent && !result.ClientSecret.Present {
		result.ClientSecret = StatusCredentialSlot{Present: true, ExpiryState: credentials.ExpiryStateUnknown}
	}
	state := credentials.WorstExpiryState(result.APIKey.ExpiryState, result.ClientSecret.ExpiryState)
	return result, state
}

func setStatusContinuation(r *StatusResult) {
	switch r.AuthStatus {
	case authStatusNotAuthenticated:
		r.Next = []string{"tslink serve --json"}
	case authStatusNeedsLogin:
		r.Next = []string{"tslink status --json"}
	default:
		r.Next = nil
		switch r.CredentialExpiryState {
		case credentials.ExpiryStateExpiring, credentials.ExpiryStateExpired:
			// The api-key slot is the only one that expires; renewing it is
			// the same three-step bootstrap every auth error points to.
			r.Next = credentials.NextAPIKeyBootstrap()
		}
	}
}

// formatCredentialSlotSummary renders one slot for the human status line.
func formatCredentialSlotSummary(name string, slot StatusCredentialSlot) string {
	if !slot.Present {
		return ""
	}
	switch slot.ExpiryState {
	case credentials.ExpiryStateExpired:
		if slot.DaysLeft != nil {
			return fmt.Sprintf("%s expired %dd ago (%s)", name, -*slot.DaysLeft, expirySourceLabel(slot.ExpiresAtSource))
		}
		return name + " expired"
	case credentials.ExpiryStateExpiring, credentials.ExpiryStateOK:
		if slot.ExpiresAt == nil {
			return name + " ok (does not expire)"
		}
		days := 0
		if slot.DaysLeft != nil {
			days = *slot.DaysLeft
		}
		return fmt.Sprintf("%s expires in %dd (%s)", name, days, expirySourceLabel(slot.ExpiresAtSource))
	default:
		return name + " expiry unknown"
	}
}

func formatCredentialSummary(c StatusCredentials) string {
	parts := make([]string, 0, 2)
	if summary := formatCredentialSlotSummary(credentials.SlotAPIKey, c.APIKey); summary != "" {
		parts = append(parts, summary)
	}
	if summary := formatCredentialSlotSummary(credentials.SlotClientSecret, c.ClientSecret); summary != "" {
		parts = append(parts, summary)
	}
	return strings.Join(parts, "; ")
}

func getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusResult, error) {
	r, err := getStatus(pidPath, regPath)
	if err != nil {
		snapshot, snapshotErr := runtimeLoadSnapshotFn(snapshotPath)
		if snapshotErr == nil && snapshot != nil && snapshot.GlobalError != nil {
			ownershipProofs, ownershipProofAvailable := ownershipProofsForRegistry(regPath)
			return statusFromGlobalFailure(pidPath, snapshot, ownershipProofs, ownershipProofAvailable), nil
		}
		return StatusResult{}, err
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		return StatusResult{}, err
	}

	snapshot, loadErr := runtimeLoadSnapshotFn(snapshotPath)
	if snapshot != nil {
		r.GlobalError = cloneServiceError(snapshot.GlobalError)
	}
	expected := tsruntime.ExpectedRuntime{CurrentRegistryFingerprint: fingerprint}
	if r.DaemonRunning {
		expected.DaemonPID = r.DaemonPID
		if lowerBound, err := pidFileModTimeFn(pidPath); err == nil {
			expected.DaemonStartedAtLowerBound = lowerBound
		}
	}
	freshness := tsruntime.Classify(snapshot, loadErr, expected)
	up := make(map[string]struct{})
	if snapshotReportsServices(freshness) && snapshot != nil {
		up = make(map[string]struct{}, len(snapshot.Services))
		snapshotServices := make(map[string]tsruntime.ServiceSnapshot, len(snapshot.Services))
		for _, svc := range snapshot.Services {
			snapshotServices[svc.Name] = svc
			if runtimeServiceRunning(svc) {
				up[svc.Name] = struct{}{}
			}
		}
		for i := range r.Services {
			if runtimeService, ok := snapshotServices[r.Services[i].Name]; ok {
				if runtimeService.FunnelState != "" {
					r.Services[i].FunnelRequested = runtimeService.FunnelRequested
					r.Services[i].FunnelActive = runtimeService.FunnelActive
					r.Services[i].FunnelState = runtimeService.FunnelState
				}
				r.Services[i].Error = runtimeService.Error
				if runtimeService.RuntimeState == tsruntime.ServiceRuntimeFailed {
					r.Services[i].Status = tsruntime.ServiceRuntimeFailed
				}
			}
			if _, ok := up[r.Services[i].Name]; ok {
				r.Services[i].Status = "up"
			}
		}
		if len(up) > 0 {
			r.NodeAuthorized = true
			r.AuthorizedServiceCount = len(up)
			r.Authenticated = true
			r.AuthStatus = authStatusAuthenticated
		}
	}

	if handoff, err := statusLoadAuthHandoffFn(authHandoffPath); err == nil {
		currentHandoff := !r.DaemonRunning || handoff.DaemonPID == r.DaemonPID
		if currentHandoff {
			_, handoffServiceUp := up[handoff.Service]
			if handoffServiceUp {
				// The snapshot can briefly win the race with removal of the
				// completed handoff. Do not regress an already-up service.
				setStatusContinuation(&r)
				return r, nil
			}
			r.Authenticated = false
			r.NodeAuthorized = false
			r.AuthStatus = authStatusNeedsLogin
			r.AuthURL = handoff.AuthURL
			expiresAt := handoff.ExpiresAt.UTC()
			r.ExpiresAt = &expiresAt
			for i := range r.Services {
				if r.Services[i].Name == handoff.Service {
					r.Services[i].Status = authStatusNeedsLogin
				}
			}
		}
	}
	setStatusContinuation(&r)
	return r, nil
}

func statusFromGlobalFailure(pidPath string, snapshot *tsruntime.Snapshot, ownershipProofs map[string]bool, ownershipProofAvailable bool) StatusResult {
	r := baseStatus(pidPath)
	r.OwnershipProofAvailable = ownershipProofAvailable
	r.GlobalError = cloneServiceError(snapshot.GlobalError)
	r.ServiceCount = len(snapshot.Services)
	r.Services = make([]StatusServiceState, 0, len(snapshot.Services))
	for _, service := range snapshot.Services {
		status := service.RuntimeState
		if status == "" {
			status = tsruntime.ServiceRuntimeRunning
		}
		r.Services = append(r.Services, StatusServiceState{
			Name:            service.Name,
			Status:          status,
			OwnershipProof:  ownershipProofs[service.Name],
			FunnelRequested: service.FunnelRequested,
			FunnelActive:    service.FunnelActive,
			FunnelState:     service.FunnelState,
			Error:           cloneServiceError(service.Error),
		})
		if status == tsruntime.ServiceRuntimeRunning {
			r.NodeAuthorized = true
			r.AuthorizedServiceCount++
		}
	}
	if r.NodeAuthorized {
		r.Authenticated = true
		r.AuthStatus = authStatusAuthenticated
	}
	setStatusContinuation(&r)
	return r
}

func cloneServiceError(source *tsruntime.ServiceError) *tsruntime.ServiceError {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Next = append([]string(nil), source.Next...)
	if source.Provision != nil {
		provision := *source.Provision
		cloned.Provision = &provision
	}
	return &cloned
}

func formatStatus(r StatusResult, out io.Writer) {
	if r.DaemonRunning {
		fmt.Fprintf(out, "→ tslink: running (pid %d)\n", r.DaemonPID)
	} else {
		fmt.Fprintln(out, "→ tslink: not running")
	}
	if r.Authenticated {
		fmt.Fprintln(out, "→ tailnet: authenticated")
		if summary := formatCredentialSummary(r.Credentials); summary != "" {
			fmt.Fprintf(out, "→ credentials: %s\n", summary)
		}
		switch r.CredentialExpiryState {
		case credentials.ExpiryStateExpiring, credentials.ExpiryStateExpired:
			for _, step := range r.Next {
				fmt.Fprintf(out, "→ next: %s\n", step)
			}
		}
	} else if r.AuthStatus == authStatusNeedsLogin {
		fmt.Fprintln(out, "→ tailnet: needs login")
		if r.AuthURL != "" {
			fmt.Fprintf(out, "→ login URL: %s\n", r.AuthURL)
		}
	} else {
		fmt.Fprintln(out, "→ tailnet: not authenticated (run: tslink serve)")
	}
	fmt.Fprintf(out, "→ services: %d registered\n", r.ServiceCount)
}

func getStatusURLs(pidPath, regPath, snapshotPath string) (StatusURLsResult, error) {
	return getStatusURLsWithAuth(pidPath, regPath, snapshotPath, filepath.Join(filepath.Dir(snapshotPath), "auth-handoff.json"))
}

func getStatusURLsWithAuth(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusURLsResult, error) {
	status, err := getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath)
	if err != nil {
		return StatusURLsResult{}, err
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return StatusURLsResult{}, err
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		return StatusURLsResult{}, err
	}

	snapshot, loadErr := runtimeLoadSnapshotFn(snapshotPath)
	expected := tsruntime.ExpectedRuntime{
		CurrentRegistryFingerprint: fingerprint,
	}
	if status.DaemonRunning {
		expected.DaemonPID = status.DaemonPID
		if lowerBound, err := pidFileModTimeFn(pidPath); err == nil {
			expected.DaemonStartedAtLowerBound = lowerBound
		}
	}
	freshness := tsruntime.Classify(snapshot, loadErr, expected)

	result := StatusURLsResult{
		SchemaVersion:           inspect.SchemaVersion,
		DaemonRunning:           status.DaemonRunning,
		DaemonState:             status.DaemonState,
		DaemonPID:               status.DaemonPID,
		OwnershipProofAvailable: status.OwnershipProofAvailable,
		Authenticated:           status.Authenticated,
		CredentialStored:        status.CredentialStored,
		Credentials:             status.Credentials,
		CredentialExpiryState:   status.CredentialExpiryState,
		NodeAuthorized:          status.NodeAuthorized,
		AuthorizedServiceCount:  status.AuthorizedServiceCount,
		AuthStatus:              status.AuthStatus,
		AuthURL:                 status.AuthURL,
		ExpiresAt:               status.ExpiresAt,
		Next:                    append([]string(nil), status.Next...),
		GlobalError:             cloneServiceError(status.GlobalError),
		ServiceCount:            len(reg.Services),
		RuntimeSnapshot:         runtimeSnapshotResult(snapshot, freshness),
		Services:                make([]StatusServiceView, 0, len(reg.Services)),
	}

	snapshotServices := map[string]tsruntime.ServiceSnapshot{}
	if snapshotReportsServices(freshness) && snapshot != nil {
		for _, svc := range snapshot.Services {
			snapshotServices[svc.Name] = svc
		}
	}

	now := statusNowFn()
	ownershipProofs, ownershipProofAvailable := ownershipProofsForRegistry(regPath)
	result.OwnershipProofAvailable = ownershipProofAvailable
	for _, svc := range reg.Services {
		expired := registry.FunnelExpiredAt(svc, now)
		effective := registry.EffectiveServiceAt(svc, now)
		view := inspect.ServiceViewFor(effective)
		service := StatusServiceView{
			Name:            view.Name,
			Type:            view.Type,
			RuntimeState:    "unknown",
			OwnershipProof:  ownershipProofs[svc.Name],
			Endpoint:        view.Endpoint,
			Exposure:        view.Exposure,
			FunnelRequested: effective.Funnel,
			FunnelState:     configuredFunnelState(effective.Funnel),
			FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
			FunnelRemaining: registry.FunnelRemainingAt(svc, now),
			Allow:           view.Allow,
			Tags:            view.Tags,
			Backend:         view.Backend,
			Warnings:        append([]inspect.WarningView(nil), view.Warnings...),
		}
		snapshotService, snapshotServiceOK := snapshotServices[svc.Name]
		if snapshotReportsServices(freshness) && snapshotServiceOK {
			service.RuntimeState = normalizedRuntimeState(snapshotService)
			if snapshotService.FunnelState != "" && !expired {
				service.FunnelRequested = snapshotService.FunnelRequested
				service.FunnelActive = snapshotService.FunnelActive
				service.FunnelState = snapshotService.FunnelState
			}
			service.Error = snapshotService.Error
		}

		switch {
		case freshness.Exact:
			switch {
			case snapshotServiceOK && snapshotService.RuntimeState == tsruntime.ServiceRuntimeFailed:
				service.Endpoint.State = statusEndpointStateMissing
				service.Exposure = snapshotService.Exposure
			case snapshotServiceOK && snapshotService.Endpoint.State == inspect.EndpointStateExact:
				service.Endpoint = snapshotService.Endpoint
				service.Endpoint.State = inspect.EndpointStateExact
				service.Exposure = snapshotService.Exposure
			case snapshotServiceOK:
				service.Endpoint.State = statusEndpointStateExpectedUnverified
			default:
				service.Endpoint.State = statusEndpointStateMissing
				service.Warnings = appendStatusWarning(
					service.Warnings,
					inspect.WarningCodeRuntimeSnapshotStale,
					"Runtime snapshot is exact but does not include this registered service.",
				)
			}
		case freshness.Status == tsruntime.StatusPartial:
			switch {
			case snapshotServiceOK && snapshotService.RuntimeState == tsruntime.ServiceRuntimeFailed:
				service.Endpoint.State = statusEndpointStateMissing
				service.Exposure = snapshotService.Exposure
			case snapshotServiceOK && snapshotService.Endpoint.State == inspect.EndpointStateExact:
				// A partial snapshot is not authoritative for omissions, but an
				// included service is positive runtime evidence and keeps sequential
				// interactive-enrollment progress visible.
				service.Endpoint = snapshotService.Endpoint
				service.Endpoint.State = inspect.EndpointStateExact
				service.Exposure = snapshotService.Exposure
			case snapshotServiceOK:
				service.Endpoint.State = statusEndpointStateExpectedUnverified
			default:
				service.Endpoint.State = statusEndpointStateMissing
			}
			service.Warnings = appendRuntimeFreshnessWarning(service.Warnings, freshness)
		default:
			service.Endpoint.State = endpointStateForFreshness(freshness)
			service.Warnings = appendRuntimeFreshnessWarning(service.Warnings, freshness)
		}
		if expired {
			// A pre-deadline snapshot cannot override the current wall clock.
			// Report the contractual tailnet-only state even during the bounded
			// interval before the lifecycle ticker closes/replaces the listener.
			service.FunnelRequested = false
			service.FunnelActive = false
			service.FunnelState = tsruntime.FunnelStateNotRequested
			service.Exposure = view.Exposure
			if service.Endpoint.Kind == inspect.EndpointKindPublicHTTPS {
				service.Endpoint.Kind = inspect.EndpointKindHTTPS
			}
		}
		if service.Endpoint.State != inspect.EndpointStateExact {
			// Expected endpoint templates are internal planning data. Never expose a
			// syntactically valid-looking hostname without exact runtime evidence.
			service.Endpoint.Display = ""
			service.Endpoint.Host = ""
		}

		result.Services = append(result.Services, service)
	}

	return result, nil
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func funnelExpiresLabel(expiresAt *time.Time, remaining *string) string {
	if expiresAt != nil {
		return expiresAt.UTC().Format(time.RFC3339)
	}
	if remaining != nil && *remaining == "never" {
		return "never"
	}
	return "-"
}

func configuredFunnelState(requested bool) string {
	if requested {
		return tsruntime.FunnelStateRequestedUnknown
	}
	return tsruntime.FunnelStateNotRequested
}

func runtimeServiceRunning(service tsruntime.ServiceSnapshot) bool {
	return service.RuntimeState == "" || service.RuntimeState == tsruntime.ServiceRuntimeRunning
}

func normalizedRuntimeState(service tsruntime.ServiceSnapshot) string {
	if runtimeServiceRunning(service) {
		return tsruntime.ServiceRuntimeRunning
	}
	return service.RuntimeState
}

func filterStatusURLsResult(result StatusURLsResult, name string) (StatusURLsResult, error) {
	if err := registry.ValidateName(name); err != nil {
		return StatusURLsResult{}, err
	}
	for _, svc := range result.Services {
		if svc.Name == name {
			result.Services = []StatusServiceView{svc}
			result.ServiceCount = 1
			return result, nil
		}
	}
	return StatusURLsResult{}, output.ErrNotFound(fmt.Sprintf("service not found: %s", name))
}

func runtimeSnapshotResult(snapshot *tsruntime.Snapshot, freshness tsruntime.Freshness) StatusRuntimeSnapshotResult {
	result := StatusRuntimeSnapshotResult{
		Status:  freshness.Status,
		Code:    freshness.Code,
		Exact:   freshness.Exact,
		Message: freshness.Message,
	}
	if snapshot != nil {
		updatedAt := snapshot.UpdatedAt.UTC()
		result.UpdatedAt = &updatedAt
		result.DaemonPID = snapshot.DaemonPID
		result.RegistryFingerprint = snapshot.RegistryFingerprint
	}
	return result
}

func snapshotReportsServices(freshness tsruntime.Freshness) bool {
	return freshness.Exact || freshness.Status == tsruntime.StatusPartial
}

func endpointStateForFreshness(freshness tsruntime.Freshness) string {
	switch freshness.Status {
	case tsruntime.StatusMissing:
		return statusEndpointStateMissing
	case tsruntime.StatusPartial:
		return statusEndpointStateMissing
	case tsruntime.StatusMalformed, tsruntime.StatusStale, tsruntime.StatusPIDMismatch, tsruntime.StatusRegistryMismatch:
		return statusEndpointStateStale
	case tsruntime.StatusUnreadable:
		return statusEndpointStateUnknown
	case tsruntime.StatusExact:
		return statusEndpointStateExpectedUnverified
	default:
		return statusEndpointStateUnknown
	}
}

func appendRuntimeFreshnessWarning(warnings []inspect.WarningView, freshness tsruntime.Freshness) []inspect.WarningView {
	if freshness.Code == "" {
		return warnings
	}
	return appendStatusWarning(warnings, freshness.Code, freshness.Message)
}

func appendStatusWarning(warnings []inspect.WarningView, code, message string) []inspect.WarningView {
	meta := inspect.WarningCodeRegistry[code]
	if message == "" {
		message = meta.Description
	}
	return append(warnings, inspect.WarningView{
		Code:     code,
		Severity: meta.Severity,
		Message:  message,
		Source:   meta.Source,
	})
}

func formatStatusURLs(r StatusURLsResult, out io.Writer) {
	formatStatus(StatusResult{
		DaemonRunning:           r.DaemonRunning,
		DaemonState:             r.DaemonState,
		DaemonPID:               r.DaemonPID,
		OwnershipProofAvailable: r.OwnershipProofAvailable,
		Authenticated:           r.Authenticated,
		CredentialStored:        r.CredentialStored,
		Credentials:             r.Credentials,
		CredentialExpiryState:   r.CredentialExpiryState,
		NodeAuthorized:          r.NodeAuthorized,
		AuthorizedServiceCount:  r.AuthorizedServiceCount,
		AuthStatus:              r.AuthStatus,
		AuthURL:                 r.AuthURL,
		ExpiresAt:               r.ExpiresAt,
		Next:                    r.Next,
		ServiceCount:            r.ServiceCount,
	}, out)
	fmt.Fprintf(out, "→ runtime snapshot: %s", r.RuntimeSnapshot.Status)
	if r.RuntimeSnapshot.Code != "" {
		fmt.Fprintf(out, " (%s)", r.RuntimeSnapshot.Code)
	}
	fmt.Fprintln(out)

	if len(r.Services) == 0 {
		fmt.Fprintln(out, "→ service urls: none")
		return
	}

	fmt.Fprintln(out)
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTYPE\tENDPOINT\tSTATE\tEXPOSURE\tFUNNEL EXPIRES\tFUNNEL TTL\tALLOW\tTAGS\tBACKEND\tWARNINGS")
	for _, svc := range r.Services {
		remaining := "-"
		if svc.FunnelRemaining != nil {
			remaining = *svc.FunnelRemaining
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			svc.Name,
			svc.Type,
			emptyDash(svc.Endpoint.Display),
			emptyDash(svc.Endpoint.State),
			emptyDash(svc.Exposure.Kind),
			funnelExpiresLabel(svc.FunnelExpiresAt, svc.FunnelRemaining),
			remaining,
			summaryLabel(svc.Allow),
			summaryLabel(svc.Tags),
			emptyDash(svc.Backend.Display),
			warningCodes(svc.Warnings),
		)
	}
	_ = writer.Flush()
}

func summaryLabel(summary inspect.SummaryView) string {
	if summary.Redacted {
		if summary.Mode == "" {
			return fmt.Sprintf("%d redacted", summary.Count)
		}
		return fmt.Sprintf("%s(%d redacted)", summary.Mode, summary.Count)
	}
	if len(summary.Entries) > 0 {
		prefix := summary.Mode
		if prefix == "" {
			prefix = "configured"
		}
		return prefix + ":" + strings.Join(summary.Entries, ",")
	}
	if summary.Mode != "" {
		return summary.Mode
	}
	return fmt.Sprintf("%d", summary.Count)
}

func warningCodes(warnings []inspect.WarningView) string {
	if len(warnings) == 0 {
		return "-"
	}
	codes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	return strings.Join(codes, ",")
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show TSLink status",
	Long: `Show the current status of TSLink: whether the daemon is running,
	Tailscale authentication state, and registered/running services. JSON output
	distinguishes credential_stored from node_authorized, includes any pending
	auth_url even after the daemon stops, and reports per-service state so an agent
	can poll a zero-credential launch to completion.

Output lines:
  → tslink: running (pid 12345)     Daemon is active with its process ID
  → tslink: not running             Daemon is not active
  → tailnet: authenticated          Stored credential or running user-owned node
  → credentials: api-key expires in 12d (assumed max); client-secret ok (does not expire)
                                    Per-slot expiry; JSON exposes credentials.* and
                                    credential_expiry_state (none/ok/expiring/expired/unknown;
                                    expiring means <= 14 days left). Fingerprints only, never values.
  → tailnet: needs login            Open the emitted URL, then poll status again
  → tailnet: not authenticated      Run 'tslink serve' to enroll without a credential
  → services: 3 registered          Number of services in the registry

	Examples:
	  tslink status                     Show current status
	  tslink status --urls              Show owner-only service endpoint overview`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := statusPIDPathFn()
		if err != nil {
			return err
		}
		regPath, err := statusRegistryPathFn()
		if err != nil {
			return err
		}
		showURLs, err := cmd.Flags().GetBool("urls")
		if err != nil {
			return err
		}
		if showURLs {
			name, err := cmd.Flags().GetString("name")
			if err != nil {
				return err
			}
			snapshotPath, err := statusRuntimeSnapshotPathFn()
			if err != nil {
				return err
			}
			authHandoffPath, err := statusAuthHandoffPathFn()
			if err != nil {
				return err
			}
			r, err := getStatusURLsWithAuth(pidPath, regPath, snapshotPath, authHandoffPath)
			if err != nil {
				return err
			}
			if name != "" {
				r, err = filterStatusURLsResult(r, name)
				if err != nil {
					return err
				}
			}
			if jsonOutput(cmd) {
				output.Success("status", r)
				return nil
			}
			formatStatusURLs(r, cmd.OutOrStdout())
			return nil
		}
		name, err := cmd.Flags().GetString("name")
		if err != nil {
			return err
		}
		if name != "" {
			return output.ErrUsage("--name requires --urls")
		}

		snapshotPath, err := statusRuntimeSnapshotPathFn()
		if err != nil {
			return err
		}
		authHandoffPath, err := statusAuthHandoffPathFn()
		if err != nil {
			return err
		}
		r, err := getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath)
		if err != nil {
			return err
		}
		if jsonOutput(cmd) {
			output.Success("status", r)
			return nil
		}
		formatStatus(r, cmd.OutOrStdout())
		return nil
	},
}

func init() {
	statusCmd.Flags().Bool("urls", false, "Show owner-only service endpoint overview")
	statusCmd.Flags().String("name", "", "Filter --urls output by exact service name")
	rootCmd.AddCommand(statusCmd)
}
