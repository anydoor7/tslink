package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	readPIDFn          = daemon.ReadPID
	isPIDFileMissingFn = daemon.IsPIDFileMissing

	statusPIDPathFn             = config.PIDPath
	statusRegistryPathFn        = config.RegistryPath
	statusRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	statusAuthHandoffPathFn     = config.AuthHandoffPath
	runtimeLoadSnapshotFn       = tsruntime.Load
	statusLoadAuthHandoffFn     = loadAuthHandoff
	statusNowFn                 = time.Now
	statusGetClientSecretFn     = credentials.GetClientSecret
	// statusCredentialInventoryFn classifies the stored credential slots and,
	// with persist, records any missing value-free metadata (backfill). Tests
	// replace it to stay off the filesystem.
	statusCredentialInventoryFn = func(values credentials.SlotValues, now time.Time, persist bool) credentials.Inventory {
		return credentials.DescribeSlots(values, now, persist)
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
	systemdUserManagerUnavailableMessage  = "systemd user manager unavailable"
)

// StatusResult holds the status information for display.
type StatusResult struct {
	Portal                  tsruntime.PortalState   `json:"portal"`
	Alerts                  health.AlertsView       `json:"alerts"`
	Supervision             Supervision             `json:"supervision"`
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
	Present            bool          `json:"present"`
	Fingerprint        string        `json:"fingerprint,omitempty"`
	StoredAt           *time.Time    `json:"stored_at,omitempty"`
	ExpiresAt          *time.Time    `json:"expires_at,omitempty"`
	ExpiresAtSource    string        `json:"expires_at_source,omitempty"`
	DaysLeft           *int          `json:"days_left,omitempty"`
	ExpiryState        string        `json:"expiry_state"`
	LastVerifiedAt     *time.Time    `json:"last_verified_at,omitempty"`
	LastVerifiedResult string        `json:"last_verified_result,omitempty"`
	EarlyWarning       health.Expiry `json:"early_warning"`
}

type StatusServiceState struct {
	RequestLimits   *registry.EffectiveRequestLimits `json:"request_limits,omitempty"`
	Warnings        []inspect.WarningView            `json:"warnings,omitempty"`
	Health          health.State                     `json:"health"`
	NodeKey         health.Expiry                    `json:"node_key"`
	Name            string                           `json:"name"`
	Status          string                           `json:"status"`
	OwnershipProof  bool                             `json:"ownership_proof"`
	PreserveHost    *bool                            `json:"preserve_host,omitempty"`
	FunnelRequested bool                             `json:"funnel_requested"`
	FunnelActive    bool                             `json:"funnel_active"`
	FunnelState     string                           `json:"funnel_state"`
	FunnelExpiresAt *time.Time                       `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                          `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError          `json:"error,omitempty"`
}

type StatusURLsResult struct {
	Portal                  tsruntime.PortalState       `json:"portal"`
	Alerts                  health.AlertsView           `json:"alerts"`
	Supervision             Supervision                 `json:"supervision"`
	SchemaVersion           int                         `json:"schema_version"`
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
	RequestLimits   *registry.EffectiveRequestLimits `json:"request_limits,omitempty"`
	Health          health.State                     `json:"health"`
	NodeKey         health.Expiry                    `json:"node_key"`
	Name            string                           `json:"name"`
	Type            string                           `json:"type"`
	RuntimeState    string                           `json:"runtime_state"`
	OwnershipProof  bool                             `json:"ownership_proof"`
	Endpoint        inspect.EndpointView             `json:"endpoint"`
	Exposure        inspect.ExposureView             `json:"exposure"`
	PreserveHost    bool                             `json:"preserve_host"`
	FunnelRequested bool                             `json:"funnel_requested"`
	FunnelActive    bool                             `json:"funnel_active"`
	FunnelState     string                           `json:"funnel_state"`
	FunnelExpiresAt *time.Time                       `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                          `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError          `json:"error,omitempty"`
	Allow           inspect.SummaryView              `json:"allow"`
	Tags            inspect.SummaryView              `json:"tags"`
	Backend         inspect.BackendView              `json:"backend"`
	Warnings        []inspect.WarningView            `json:"warnings,omitempty"`
}

// statusRead is how a caller reads status. The commands that report status
// record the value-free metadata they backfill for a stored credential that
// has none yet (README: they create credentials.lock only then). An MCP tool
// that declares readOnlyHint must not write anything, so it reads readOnly:
// the backfill is described but not recorded. Nothing else differs.
type statusRead struct {
	readOnly bool
}

var (
	// commandStatus is how the CLI commands read status, and how share and
	// add read it while they wait for a URL.
	commandStatus = statusRead{}
	// readOnlyStatus is how the read-only MCP tools read it.
	readOnlyStatus = statusRead{readOnly: true}
)

func getStatus(pidPath, regPath string) (StatusResult, error) {
	return commandStatus.getStatus(pidPath, regPath)
}

func (s statusRead) getStatus(pidPath, regPath string) (StatusResult, error) {
	r := s.baseStatus(pidPath)
	reg, issues, err := registry.LoadForDiagnostics(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	issueErrors := diagnosticServiceErrors(issues)
	r.Portal = readPortalView(reg, regPath, r.DaemonRunning, r.DaemonPID)
	r.Alerts = readAlertsForRegistry(regPath)
	r.ServiceCount = len(reg.Services)
	r.Services = make([]StatusServiceState, 0, len(reg.Services))
	ownershipProofs, ownershipProofAvailable := ownershipProofsForRegistry(regPath)
	r.OwnershipProofAvailable = ownershipProofAvailable
	for _, svc := range reg.Services {
		now := statusNowFn()
		effective := registry.EffectiveServiceAt(svc, now)
		r.Services = append(r.Services, StatusServiceState{
			Name:            svc.Name,
			Health:          health.Unchecked(svc.Type),
			NodeKey:         health.Expiry{State: health.Unknown, Source: "unavailable"},
			Status:          "down",
			Error:           issueErrors[svc.Name],
			OwnershipProof:  ownershipProofs[svc.Name],
			PreserveHost:    &effective.PreserveHost,
			RequestLimits:   svc.EffectiveRequestLimits(),
			FunnelRequested: effective.Funnel,
			FunnelState:     configuredFunnelState(effective.Funnel),
			FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
			FunnelRemaining: registry.FunnelRemainingAt(svc, now),
		})
		if issueErrors[svc.Name] != nil {
			r.Services[len(r.Services)-1].Status = tsruntime.ServiceRuntimeFailed
		}
	}
	setStatusContinuation(&r)
	return r, nil
}

func diagnosticServiceErrors(issues []registry.ServiceIssue) map[string]*tsruntime.ServiceError {
	result := make(map[string]*tsruntime.ServiceError, len(issues))
	for _, issue := range issues {
		code, ok := registry.ErrorCode(issue.Err)
		if !ok {
			code = registry.CodeInvalidServiceConfig
		}
		result[issue.Name] = &tsruntime.ServiceError{Code: code, Message: issue.Error(), Next: []string{"tslink registry check --json"}}
	}
	return result
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

func (s statusRead) baseStatus(pidPath string) StatusResult {
	r := StatusResult{DaemonState: daemonStateUnknown, AuthStatus: authStatusNotAuthenticated, Services: []StatusServiceState{}}
	if isRunningFn(pidPath) {
		r.DaemonRunning = true
		r.DaemonState = daemonStateRunning
		r.DaemonPID, _ = readPIDFn(pidPath)
	} else if isPIDFileMissingFn(pidPath) || isProcessAbsentFromPIDFileFn(pidPath) {
		// No PID file, or a PID file whose process is gone: no daemon runs
		// for this config directory. Only a PID file that cannot be judged
		// leaves the state unknown.
		r.DaemonState = daemonStateAbsent
	}
	r.Supervision = detectSupervisionFn(pidPath, r.DaemonRunning, r.DaemonPID)
	values := credentials.SlotValues{}
	values.APIKey, _ = getAPIKeyFn()
	hasClientSecret := hasClientSecretFn()
	if hasClientSecret {
		values.ClientSecret, _ = statusGetClientSecretFn()
	}
	// A stored credential is a string on disk until a node proves it:
	// credential_stored reports it, while authenticated and auth_status wait
	// for an authorized service node (node_authorized), on every surface.
	if values.APIKey != "" || hasClientSecret {
		r.CredentialStored = true
	}
	r.Credentials, r.CredentialExpiryState = statusCredentialsFromInventory(statusCredentialInventoryFn(values, statusNowFn(), !s.readOnly), hasClientSecret)
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
	slot.EarlyWarning = credentialEarlyWarning(slot, statusNowFn())
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

// systemdUserManagerUnavailableNext is the way out of a missing systemd user
// manager: a login session that provides one, or a manual serve. Installing
// again is not, since install fails the same way until that session exists.
func systemdUserManagerUnavailableNext() []string {
	return []string{"Establish a login session with a working systemd user manager and XDG_RUNTIME_DIR", "tslink serve", "Use add/share --no-daemon-install to register services for manual serve"}
}

func setStatusContinuation(r *StatusResult) {
	switch {
	case strings.Contains(r.Supervision.Detail, systemdUserManagerUnavailableMessage):
		r.Next = systemdUserManagerUnavailableNext()
	case r.AuthStatus == authStatusNeedsLogin:
		r.Next = []string{"tslink status --json"}
	case r.AuthStatus == authStatusNotAuthenticated && !r.CredentialStored:
		// Each step is a command that does what it says. Without a daemon,
		// tslink install starts the supervised background service, which
		// enrolls the registered services; a running daemon's authorization
		// URL appears in tslink status --json. With nothing registered there
		// is nothing to authorize: the next share or add enrolls and installs.
		// (tslink serve --json would start an unsupervised daemon that the
		// next share refuses to reuse.)
		switch {
		case r.ServiceCount == 0:
			r.Next = nil
		case !r.DaemonRunning:
			r.Next = []string{"tslink install"}
		default:
			r.Next = []string{"tslink status --json"}
		}
	default:
		// Authorized, or a stored credential whose nodes are not up yet.
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

// currentRegistryFingerprint fingerprints registry.json the way the daemon
// does (tsruntime.CurrentRegistryFingerprint), so a snapshot the daemon wrote
// for this registry compares equal. A registry the daemon's loader rejects
// cannot be the one it applied: the blank fingerprint keeps the snapshot
// from classifying as exact.
func currentRegistryFingerprint(regPath string) string {
	fingerprint, err := tsruntime.CurrentRegistryFingerprint(regPath)
	if err != nil {
		return ""
	}
	return fingerprint
}

func getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusResult, error) {
	return commandStatus.getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath)
}

func (s statusRead) getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusResult, error) {
	r, err := s.getStatus(pidPath, regPath)
	if err != nil {
		snapshot, snapshotErr := runtimeLoadSnapshotFn(snapshotPath)
		if snapshotErr == nil && snapshot != nil && snapshot.GlobalError != nil {
			ownershipProofs, ownershipProofAvailable := ownershipProofsForRegistry(regPath)
			return s.statusFromGlobalFailure(pidPath, regPath, snapshot, ownershipProofs, ownershipProofAvailable), nil
		}
		return StatusResult{}, err
	}
	reg, _, err := registry.LoadForDiagnostics(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	expiredByName := make(map[string]bool, len(reg.Services))
	currentByName := make(map[string]registry.Service, len(reg.Services))
	now := statusNowFn()
	for _, svc := range reg.Services {
		expiredByName[svc.Name] = registry.FunnelExpiredAt(svc, now)
		currentByName[svc.Name] = svc
	}
	// getStatus may have sampled the clock just before a deadline. Normalize
	// every Funnel field to this later effective time before applying runtime
	// snapshot evidence, so one response cannot mix pre/post-deadline state.
	for i := range r.Services {
		if expiredByName[r.Services[i].Name] {
			r.Services[i].FunnelRequested = false
			r.Services[i].FunnelActive = false
			r.Services[i].FunnelState = tsruntime.FunnelStateNotRequested
			remaining := "0s"
			r.Services[i].FunnelRemaining = &remaining
		}
	}
	fingerprint := currentRegistryFingerprint(regPath)

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
	// Events always come from the durable journal. A live, fresh snapshot may
	// additionally report a write failure that could not itself be persisted.
	if snapshot != nil {
		r.Alerts = alertsWithSnapshot(r.Alerts, snapshot.Alerts, r.DaemonRunning && snapshotContributesRuntimeEvidence(freshness))
	}
	up := make(map[string]struct{})
	if snapshotContributesRuntimeEvidence(freshness) && snapshot != nil {
		up = make(map[string]struct{}, len(snapshot.Services))
		snapshotServices := make(map[string]tsruntime.ServiceSnapshot, len(snapshot.Services))
		for _, svc := range snapshot.Services {
			snapshotServices[svc.Name] = svc
		}
		for i := range r.Services {
			// Another writer can remove or reorder services between our
			// registry reads. Join by name and withhold removed services.
			current, present := currentByName[r.Services[i].Name]
			if !present || r.Services[i].Error != nil {
				continue
			}
			if runtimeService, ok := snapshotServices[r.Services[i].Name]; ok {
				if runtimeService.FunnelState != "" && !expiredByName[r.Services[i].Name] {
					r.Services[i].FunnelRequested = runtimeService.FunnelRequested
					r.Services[i].FunnelActive = runtimeService.FunnelActive
					r.Services[i].FunnelState = runtimeService.FunnelState
				}
				r.Services[i].Warnings = append([]inspect.WarningView(nil), runtimeService.Warnings...)
				r.Services[i].Error = runtimeService.Error
				r.Services[i].Health = currentHealth(runtimeService.Health, current, now)
				r.Services[i].NodeKey = health.ExpiryAt(runtimeService.NodeKey.ExpiresAt, runtimeService.NodeKey.Source, now, nodeExpiryNext())
				if runtimeService.RuntimeState == tsruntime.ServiceRuntimeFailed {
					r.Services[i].Status = tsruntime.ServiceRuntimeFailed
				}
			}
			// Only services still present in the current registry can count
			// toward authorization. A registry-mismatch snapshot may still list
			// a service that was just removed; counting it would inflate
			// AuthorizedServiceCount until the next snapshot write.
			if runtimeService, ok := snapshotServices[r.Services[i].Name]; ok && runtimeServiceRunning(runtimeService) {
				up[r.Services[i].Name] = struct{}{}
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

	portalUp := r.DaemonRunning && r.Portal.Enabled && r.Portal.State == "running" && r.Portal.URL != ""
	if portalUp {
		r.NodeAuthorized, r.Authenticated = true, true
		r.AuthStatus = authStatusAuthenticated
	}
	if handoff, err := statusLoadAuthHandoffFn(authHandoffPath); err == nil {
		currentHandoff := !r.DaemonRunning || handoff.DaemonPID == r.DaemonPID
		if currentHandoff {
			_, handoffServiceUp := up[handoff.Service]
			if handoffServiceUp || (portalUp && handoff.Service == r.Portal.Hostname) {
				// The snapshot can briefly win the race with removal of the
				// completed handoff. Do not regress an already-up service.
				setStatusContinuation(&r)
				return r, nil
			}
			// A pending handoff for one service must not erase the
			// authorization evidence of the other services this daemon is
			// already serving. Keep the authorized count and only let the
			// handoff's own service fall back to needs_login; suppress the
			// global authenticated flag only when no service is up.
			if len(up) == 0 && !portalUp {
				r.Authenticated = false
				r.NodeAuthorized = false
			}
			r.AuthStatus = authStatusNeedsLogin
			r.AuthURL = handoff.AuthURL
			expiresAt := handoff.ExpiresAt.UTC()
			r.ExpiresAt = &expiresAt
			for i := range r.Services {
				if r.Services[i].Name == handoff.Service && r.Services[i].Error == nil {
					r.Services[i].Status = authStatusNeedsLogin
				}
			}
		}
	}
	setStatusContinuation(&r)
	return r, nil
}

func (s statusRead) statusFromGlobalFailure(pidPath, regPath string, snapshot *tsruntime.Snapshot, ownershipProofs map[string]bool, ownershipProofAvailable bool) StatusResult {
	r := s.baseStatus(pidPath)
	r.OwnershipProofAvailable = ownershipProofAvailable
	r.GlobalError = cloneServiceError(snapshot.GlobalError)
	// The registry cannot be read, so this snapshot cannot be verified as
	// current. Journal authority still holds on the global-failure path.
	r.Alerts = alertsWithSnapshot(readAlertsForRegistry(regPath), snapshot.Alerts, false)
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
	formatPortal(out, r.Portal)
	userManagerUnavailable := strings.Contains(r.Supervision.Detail, systemdUserManagerUnavailableMessage)
	noNodes := false
	if r.ServiceCount == 0 {
		cfg, err := config.LoadGlobalConfig()
		noNodes = err == nil && (cfg.MCP == nil || !cfg.MCP.Enabled)
	}
	if r.DaemonRunning {
		fmt.Fprintf(out, "→ tslink: running (pid %d)\n", r.DaemonPID)
	} else {
		fmt.Fprintln(out, "→ tslink: not running")
		if !noNodes && !userManagerUnavailable {
			fmt.Fprintln(out, "Next: tslink install")
		}
	}
	formatSupervision(r.Supervision, out)
	if r.Authenticated || r.CredentialStored && r.AuthStatus != authStatusNeedsLogin {
		if r.Authenticated {
			fmt.Fprintln(out, "→ tailnet: authenticated")
		} else {
			// A stored credential is not an authorized node; say which.
			fmt.Fprintln(out, "→ tailnet: no authorized node yet (credential stored)")
		}
		if summary := formatCredentialSummary(r.Credentials); summary != "" {
			fmt.Fprintf(out, "→ credentials: %s\n", summary)
		}
		switch r.CredentialExpiryState {
		case credentials.ExpiryStateExpiring, credentials.ExpiryStateExpired:
			for _, step := range r.Next {
				fmt.Fprintf(out, "→ next: %s\n", step)
			}
		}
		// Authenticated services can coexist with a pending enrollment for a
		// newly added node; keep that node's login URL visible instead of
		// hiding it behind the global authenticated state.
		if r.AuthStatus == authStatusNeedsLogin && r.AuthURL != "" {
			fmt.Fprintf(out, "→ pending login URL: %s\n", r.AuthURL)
		}
	} else if r.AuthStatus == authStatusNeedsLogin {
		fmt.Fprintln(out, "→ tailnet: needs login")
		if r.AuthURL != "" {
			fmt.Fprintf(out, "→ login URL: %s\n", r.AuthURL)
		}
	} else if noNodes {
		fmt.Fprintln(out, "→ tailnet: not authenticated; no service nodes configured; register one with tslink add or tslink share")
	} else if userManagerUnavailable {
		fmt.Fprintln(out, "→ tailnet: not authenticated; restore the login session described above, or run tslink serve manually to enroll")
	} else {
		fmt.Fprintln(out, "→ tailnet: not authenticated (run: tslink install, then tslink status to obtain the login URL)")
	}
	fmt.Fprintf(out, "→ services: %d registered\n", r.ServiceCount)
	for _, svc := range r.Services {
		formatAppHealth(out, svc.Name, svc.Health, svc.NodeKey)
	}
	formatEarlyWarnings(out, r.Credentials)
	formatAlerts(out, r.Alerts)
}

func getStatusURLs(pidPath, regPath, snapshotPath string) (StatusURLsResult, error) {
	return commandStatus.getStatusURLs(pidPath, regPath, snapshotPath)
}

func (s statusRead) getStatusURLs(pidPath, regPath, snapshotPath string) (StatusURLsResult, error) {
	return s.getStatusURLsWithAuth(pidPath, regPath, snapshotPath, filepath.Join(filepath.Dir(snapshotPath), "auth-handoff.json"))
}

func getStatusURLsWithAuth(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusURLsResult, error) {
	return commandStatus.getStatusURLsWithAuth(pidPath, regPath, snapshotPath, authHandoffPath)
}

func (s statusRead) getStatusURLsWithAuth(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusURLsResult, error) {
	status, err := s.getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath)
	if err != nil {
		return StatusURLsResult{}, err
	}
	reg, issues, err := registry.LoadForDiagnostics(regPath)
	if err != nil {
		return StatusURLsResult{}, err
	}
	issueErrors := diagnosticServiceErrors(issues)
	fingerprint := currentRegistryFingerprint(regPath)

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
		Portal:                  status.Portal,
		SchemaVersion:           inspect.SchemaVersion,
		Alerts:                  status.Alerts,
		Supervision:             status.Supervision,
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
	if snapshotContributesRuntimeEvidence(freshness) && snapshot != nil {
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
			Health:          health.Unchecked(svc.Type),
			NodeKey:         health.Expiry{State: health.Unknown, Source: "unavailable"},
			Type:            view.Type,
			RuntimeState:    "unknown",
			OwnershipProof:  ownershipProofs[svc.Name],
			Endpoint:        view.Endpoint,
			Exposure:        view.Exposure,
			PreserveHost:    effective.PreserveHost,
			FunnelRequested: effective.Funnel,
			FunnelState:     configuredFunnelState(effective.Funnel),
			FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
			FunnelRemaining: registry.FunnelRemainingAt(svc, now),
			Allow:           view.Allow,
			Tags:            view.Tags,
			Backend:         view.Backend,
			Warnings:        append([]inspect.WarningView(nil), view.Warnings...),
			RequestLimits:   svc.EffectiveRequestLimits(),
		}
		snapshotService, snapshotServiceOK := snapshotServices[svc.Name]
		if snapshotContributesRuntimeEvidence(freshness) && snapshotServiceOK {
			service.RuntimeState = normalizedRuntimeState(snapshotService)
			if snapshotService.FunnelState != "" && !expired {
				service.FunnelRequested = snapshotService.FunnelRequested
				service.FunnelActive = snapshotService.FunnelActive
				service.FunnelState = snapshotService.FunnelState
			}
			service.Error = snapshotService.Error
			service.Health = currentHealth(snapshotService.Health, svc, now)
			service.NodeKey = health.ExpiryAt(snapshotService.NodeKey.ExpiresAt, snapshotService.NodeKey.Source, now, nodeExpiryNext())
			service.Warnings = append(service.Warnings, snapshotService.Warnings...)
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
		case freshness.Status == tsruntime.StatusRegistryMismatch:
			// A registry mismatch means a newer registry exists, but the
			// snapshot entries for services still present are positive
			// per-service evidence produced by this daemon. Present them as up
			// while warning that the snapshot as a whole is stale, so one
			// pending node cannot erase another service's running evidence.
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
				service.Endpoint.State = statusEndpointStateStale
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
		if issueErr := issueErrors[svc.Name]; issueErr != nil {
			service.RuntimeState = tsruntime.ServiceRuntimeFailed
			service.Error = issueErr
			service.Endpoint.State = statusEndpointStateMissing
			service.FunnelActive = false
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

// snapshotContributesRuntimeEvidence reports whether individual services in a
// non-authoritative snapshot may still be consumed as positive per-service
// runtime evidence. A registry_mismatch snapshot was written by this same
// daemon for an earlier registry; the services it lists were running under this
// daemon and stay valid evidence for themselves even though a later registry
// change (for example a newly added node awaiting authorization) means the
// snapshot as a whole can no longer be trusted for omissions.
func snapshotContributesRuntimeEvidence(freshness tsruntime.Freshness) bool {
	return freshness.Exact || freshness.Status == tsruntime.StatusPartial || freshness.Status == tsruntime.StatusRegistryMismatch
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
		Portal:                  r.Portal,
		Alerts:                  r.Alerts,
		Supervision:             r.Supervision,
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
	fmt.Fprintln(writer, "NAME\tTYPE\tENDPOINT\tSTATE\tEXPOSURE\tFUNNEL EXPIRES\tFUNNEL TTL\tALLOW\tTAGS\tBACKEND\tWARNINGS\tREQUEST LIMITS")
	for _, svc := range r.Services {
		remaining := "-"
		if svc.FunnelRemaining != nil {
			remaining = *svc.FunnelRemaining
		}
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
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
			requestLimitsLabel(svc.RequestLimits),
		)
	}
	_ = writer.Flush()
	for _, svc := range r.Services {
		formatAppHealth(out, svc.Name, svc.Health, svc.NodeKey)
	}
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
