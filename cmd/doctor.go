package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/lifecycle"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
	"tailscale.com/client/local"
)

const (
	doctorStatusOK           = "healthy"
	doctorStatusWarning      = "warning"
	doctorStatusError        = "error"
	doctorStatusCritical     = "critical"
	doctorExecutionCompleted = "completed"

	doctorSeverityInfo     = "info"
	doctorSeverityWarning  = "warning"
	doctorSeverityError    = "error"
	doctorSeverityCritical = "critical"

	doctorCredentialNone              = "none"
	doctorCredentialAPIToken          = "api_token"
	doctorCredentialOAuthClientSecret = "oauth_client_secret"
	doctorCredentialLegacyAuthKey     = "legacy_authkey"
	doctorCredentialMixed             = "mixed"
	doctorCredentialTier1             = "tier1"
	doctorCredentialTier2             = "tier2"
	doctorCredentialTierUnknown       = "unknown"

	doctorProbeTimeout = 250 * time.Millisecond

	// Tailscale SSH states reported by the informational tailscale_ssh check.
	doctorTailscaleSSHEnabled  = "enabled"
	doctorTailscaleSSHDisabled = "disabled"
	doctorTailscaleSSHUnknown  = "unknown"

	doctorRedactedEvidenceValue = "[redacted]"
	doctorRedactedURL           = "[redacted-url]"

	// Go's syscall.ECONN* values are synthetic on Windows and do not equal the
	// Winsock errors returned by net.Dial. Keep the native codes explicit so the
	// same classifier handles real Windows refused and reset connections.
	windowsWSAECONNRESET   syscall.Errno = 10054
	windowsWSAECONNREFUSED syscall.Errno = 10061
)

var (
	doctorConfigDirFn           = config.Dir
	doctorRegistryPathFn        = config.RegistryPath
	doctorRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	doctorAuthHandoffPathFn     = config.AuthHandoffPath
	doctorPIDPathFn             = config.PIDPath
	doctorAuthKeyPathFn         = config.AuthKeyPath
	doctorLoadGlobalConfigFn    = config.LoadGlobalConfig
	doctorGetAPIKeyFn           = credentials.GetAPIKey
	doctorGetClientSecretFn     = credentials.GetClientSecret
	doctorReadFileFn            = os.ReadFile
	doctorStatFn                = os.Stat
	doctorOpenPathFn            = func(path string) (io.Closer, error) { return os.Open(path) }
	doctorProbeTargetFn         = defaultDoctorProbeTarget
	doctorLoadAuthHandoffFn     = loadAuthHandoff
	doctorNowFn                 = func() time.Time { return time.Now().UTC() }
	// doctorCredentialInventoryFn classifies credential slots and, with
	// persist, backfills missing value-free metadata. Tests replace it to stay
	// off the filesystem.
	doctorCredentialInventoryFn = func(values credentials.SlotValues, now time.Time, persist bool) credentials.Inventory {
		return credentials.DescribeSlots(values, now, persist)
	}
	doctorProbeCredentialFn = credentials.ProbeStoredCredential
	// doctorTailscaleSSHFn reads Tailscale SSH enablement from the local
	// Tailscale client. Tests replace it so no test process talks to a real
	// tailscaled.
	doctorTailscaleSSHFn = defaultTailscaleSSHEnabled
	// doctorLocalClientFn builds the client defaultTailscaleSSHEnabled reads
	// through. Tests replace it with a stub-transport client.
	doctorLocalClientFn = newDoctorLocalClient
)

// doctorNodeOwnershipPathFn locates the ownership ledger whose unretired
// records for unregistered services withhold device deletion.
var doctorNodeOwnershipPathFn = config.NodeOwnershipPath

// doctorSkipTailscaleSSHEnv set to 1 makes doctor skip its read of the local
// tailscaled and report Tailscale SSH as unknown, naming the variable. It
// exists for environments where contacting the machine's tailscaled is not
// wanted, such as test suites that run the compiled binary on a developer's
// machine. Unset, doctor behaves as before.
const doctorSkipTailscaleSSHEnv = "TSLINK_DOCTOR_SKIP_TAILSCALE_SSH"

// errTailscaleSSHCheckSkipped is defaultTailscaleSSHEnabled's answer while
// doctorSkipTailscaleSSHEnv is set.
var errTailscaleSSHCheckSkipped = errors.New(doctorSkipTailscaleSSHEnv + "=1 skips the local tailscaled read")

// doctorRemoteProbeTimeout bounds each --probe-remote device-list read.
const doctorRemoteProbeTimeout = 10 * time.Second

// doctorTailscaleSSHTimeout bounds the local-API preferences read. The check is
// informational, so an unreachable or slow tailscaled must degrade to unknown
// quickly instead of stretching a doctor run.
const doctorTailscaleSSHTimeout = time.Second

// defaultTailscaleSSHEnabled reads RunSSH from the local tailscaled over its
// loopback local API. It deliberately does not shell out to the `tailscale`
// binary: the client library is already a dependency (internal/server uses the
// same package), so a missing CLI on PATH cannot make this check wrong.
//
// This is a localhost IPC to the daemon on this machine, not a Tailscale
// control-plane API call, and it only reads.
func defaultTailscaleSSHEnabled(ctx context.Context) (bool, error) {
	if os.Getenv(doctorSkipTailscaleSSHEnv) == "1" {
		return false, errTailscaleSSHCheckSkipped
	}
	prefs, err := doctorLocalClientFn().GetPrefs(ctx)
	if err != nil {
		return false, err
	}
	if prefs == nil {
		return false, errors.New("local Tailscale client returned no preferences")
	}
	return prefs.RunSSH, nil
}

// newDoctorLocalClient returns the default local Tailscale client: the
// platform's tailscaled socket, authenticated the way the tailscale CLI is.
func newDoctorLocalClient() *local.Client {
	return &local.Client{}
}

var (
	doctorCredentialTokenPattern = regexp.MustCompile(`(?i)\btskey-[A-Za-z0-9._~+/=-]+`)
	doctorURLPattern             = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>)]+`)
	doctorUserInfoPattern        = regexp.MustCompile(`[A-Za-z0-9._%+-]+:[^@\s/]+@`)
)

type doctorOptions struct {
	ProbeExternal       bool
	ProbeRemote         bool
	RegistryPath        string
	PIDPath             string
	RuntimeSnapshotPath string
	AuthHandoffPath     string
	// ReadOnly reports a credential metadata backfill without recording it.
	// The MCP doctor tool sets it: it declares readOnlyHint and must not
	// write, while tslink doctor records the backfill (see statusRead).
	ReadOnly bool
}

type DoctorResult struct {
	AccessLog       accesslog.Health `json:"access_log"`
	canonicalHosts  map[string]string
	NodeKeys        map[string]health.Expiry    `json:"node_keys"`
	Credentials     StatusCredentials           `json:"credentials"`
	Alerts          health.AlertsView           `json:"alerts"`
	Supervision     Supervision                 `json:"supervision"`
	SchemaVersion   int                         `json:"schema_version"`
	ExecutionStatus string                      `json:"execution_status"`
	Status          string                      `json:"status"`
	HealthStatus    string                      `json:"health_status"`
	HealthExitCode  int                         `json:"health_exit_code"`
	Counts          DoctorCounts                `json:"counts"`
	Paths           DoctorPaths                 `json:"paths"`
	CredentialMode  string                      `json:"credential_mode"`
	CredentialTier  string                      `json:"credential_tier"`
	Daemon          DoctorDaemon                `json:"daemon"`
	RuntimeSnapshot StatusRuntimeSnapshotResult `json:"runtime_snapshot"`
	TailscaleSSH    DoctorTailscaleSSH          `json:"tailscale_ssh"`
	Findings        []DoctorFinding             `json:"findings"`
}

// DoctorTailscaleSSH reports whether tailscaled on this node runs a Tailscale
// SSH server. It is discoverability data for the zero-code remote path
// (`tailscale ssh <host> tslink <command>`), not a TSLink capability: TSLink
// neither enables nor requires it, and this report never affects health.
type DoctorTailscaleSSH struct {
	State string `json:"state"`
	// ACLRuleRequired records the second half of the requirement, which no
	// local read can observe. `tailscale set --ssh` alone is not enough; the
	// tailnet policy file also needs an ssh rule admitting the caller.
	ACLRuleRequired bool `json:"acl_rule_required"`
}

type DoctorCounts struct {
	Services int `json:"services"`
	Findings int `json:"findings"`
	Info     int `json:"info"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
	Critical int `json:"critical"`
}

type DoctorPaths struct {
	ConfigDir       string `json:"config_dir,omitempty"`
	Registry        string `json:"registry,omitempty"`
	RuntimeSnapshot string `json:"runtime_snapshot,omitempty"`
	AuthHandoff     string `json:"auth_handoff,omitempty"`
	PID             string `json:"pid,omitempty"`
}

type DoctorDaemon struct {
	IdentityUnverified bool `json:"identity_unverified,omitempty"`
	Running            bool `json:"running"`
	PID                int  `json:"pid,omitempty"`
	// BuildVersion, Executable, and BuildSkew are reporting-only: they never
	// factor into IdentityUnverified, Running, or health_status/exit code
	// beyond the daemon_build_skew warning itself. See
	// diagnoseDaemonBuildSkew and daemon.ProcessBuildIdentity.
	BuildVersion string `json:"build_version,omitempty"`
	Executable   string `json:"executable,omitempty"`
	BuildSkew    bool   `json:"build_skew,omitempty"`
}

type DoctorFinding struct {
	Code     string            `json:"code"`
	Severity string            `json:"severity"`
	Area     string            `json:"area"`
	Message  string            `json:"message"`
	Service  string            `json:"service,omitempty"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

type doctorTarget struct {
	Host         string
	Port         string
	ProbeAddress string
	External     bool
}

type doctorCredentialState struct {
	CredentialFree bool
}

// doctorJSONData carries a DoctorResult into the result envelope's data slot.
// Custom marshaling keeps the wire data flat, so the envelope publishes the
// DoctorResult fields directly instead of nesting them under a wrapper key.
type doctorJSONData struct {
	Doctor DoctorResult
}

func (d doctorJSONData) MarshalJSON() ([]byte, error) { return json.Marshal(d.Doctor) }

func runDoctor(out io.Writer, opts doctorOptions, isJSON bool) error {
	result := buildDoctorResult(opts)
	exit := doctorExit(result)
	if isJSON {
		// The diagnosis completed, so ok stays true, but code is the process
		// exit code, as on every other command: 64 for warnings, 65 for
		// errors (health_exit_code in data carries the same number).
		envelope := output.NewSuccess("doctor", doctorJSONData{Doctor: result})
		envelope.Code = output.ExitCode(exit)
		output.WriteJSON(out, envelope)
	} else {
		formatDoctor(result, out)
	}
	return exit
}

func buildDoctorResult(opts doctorOptions) DoctorResult {
	result := DoctorResult{
		SchemaVersion:   inspect.SchemaVersion,
		ExecutionStatus: doctorExecutionCompleted,
		Status:          doctorStatusOK,
		HealthStatus:    doctorStatusOK,
		HealthExitCode:  output.ExitSuccess,
		CredentialMode:  doctorCredentialNone,
		CredentialTier:  doctorCredentialTierUnknown,
		Findings:        []DoctorFinding{},
		RuntimeSnapshot: StatusRuntimeSnapshotResult{
			Status: "unknown",
		},
	}

	pathsOK := discoverDoctorPaths(&result, opts)
	if pathsOK {
		result.AccessLog = accessHealthForRegistry(result.Paths.Registry, result.Paths.PID, result.Paths.RuntimeSnapshot)
	}
	credentialState := diagnoseCredentials(&result, opts)

	var cfg config.GlobalConfig
	cfgOK := false
	if pathsOK {
		loaded, err := doctorLoadGlobalConfigFn()
		if err != nil {
			result.addFinding(inspect.WarningCodeConfigLoadFailed, "", "config", "Global config could not be loaded.", evidenceError(err))
		} else {
			cfg = loaded
			cfgOK = true
			if err := registry.ValidateControlURL(cfg.ControlURL); err != nil {
				result.addFinding(inspect.WarningCodeControlURLInvalid, "", "config", "Global control_url is invalid.", evidenceControlURLInvalid())
			}
		}
	}

	var reg *registry.Registry
	var registryIssues []registry.ServiceIssue
	var fingerprint string
	if result.Paths.Registry != "" {
		loaded, issues, err := registry.LoadForDiagnostics(result.Paths.Registry)
		if err != nil {
			result.addFinding(inspect.WarningCodeRegistryLoadFailed, "", "registry", "Registry could not be loaded.", evidenceError(err))
		} else {
			reg = loaded
			registryIssues = issues
			result.Counts.Services = len(reg.Services)
			result.NodeKeys = map[string]health.Expiry{}
			result.Alerts = readAlertsForRegistry(result.Paths.Registry)
			for _, svc := range reg.Services {
				result.NodeKeys[svc.Name] = health.Expiry{State: health.Unknown, Source: "unavailable"}
			}
			if fp, err := tsruntime.CurrentRegistryFingerprint(result.Paths.Registry); err != nil {
				result.addFinding(inspect.WarningCodeRegistryLoadFailed, "", "registry", "Registry fingerprint could not be computed.", evidenceError(err))
			} else {
				fingerprint = fp
			}
		}
	}

	serviceCount := 0
	if reg != nil {
		serviceCount = len(reg.Services)
	}
	diagnoseDaemon(&result, serviceCount)
	result.Supervision = detectSupervisionFn(result.Paths.PID, result.Daemon.Running, result.Daemon.PID)
	if result.Supervision.RuntimeState == "circuit_open" || result.Supervision.RuntimeState == "failed" {
		result.addFinding(inspect.WarningCodeDaemonRestartUnavailable, "", "daemon", "Built-in supervisor stopped crash recovery: "+result.Supervision.FailureReason+". Inspect logs, then run 'tslink install'.", nil)
	}
	if serviceCount > 0 && !result.Daemon.IdentityUnverified && (!result.Supervision.Autostart || result.Supervision.Manager == "manual" || result.Supervision.Manager == "none") {
		result.addFinding(inspect.WarningCodeDaemonUnsupervised, "", "daemon", "Registered services have no verified supervisor/autostart; run 'tslink install'. "+result.Supervision.Detail, nil)
	}
	if serviceCount > 0 && result.Supervision.Manager == "windows-startup" && result.Supervision.Autostart && !result.Supervision.RestartOnExit {
		result.addFinding(inspect.WarningCodeDaemonRestartUnavailable, "", "daemon", "Windows Startup starts TSLink at sign-in but does not restart it after a crash. Stop the daemon and run 'tslink install' to migrate to Task Scheduler when available.", nil)
	}
	invalidServices := make(map[string]bool, len(registryIssues))
	for _, issue := range registryIssues {
		invalidServices[issue.Name] = true
	}
	var validNames []string
	if reg != nil && len(registryIssues) > 0 {
		for _, svc := range reg.Services {
			if !invalidServices[svc.Name] {
				validNames = append(validNames, svc.Name)
			}
		}
	}
	for _, issue := range registryIssues {
		if registry.ValidateService(issue.Service) != nil {
			continue // Existing service diagnostics provide the specific repair.
		}
		evidence := evidenceError(issue.Err)
		evidence["valid_services"] = strings.Join(validNames, ", ")
		result.addFinding(doctorRegistryValidationCode(issue.Service, issue.Err), issue.Name, "registry", issue.Error(), evidence)
	}
	// Resolve verified receiving-node names before HTTP business probes.
	pendingEnrollment := diagnosePendingEnrollment(&result)

	completedEnrollment := false
	if reg != nil && (serviceCount > 0 || result.Daemon.Running) && result.Paths.RuntimeSnapshot != "" && fingerprint != "" {
		suppressExpectedMissing := credentialState.CredentialFree && (pendingEnrollment || !result.Daemon.Running)
		completedEnrollment = diagnoseRuntimeSnapshot(&result, fingerprint, suppressExpectedMissing)
	}
	hasFunnel := false
	if reg != nil {
		for _, svc := range reg.Services {
			if svc.Funnel {
				hasFunnel = true
			}
			if !invalidServices[svc.Name] || registry.ValidateService(svc) != nil {
				diagnoseService(&result, svc, opts)
			}
		}
	}
	if reg != nil {
		diagnoseDeviceCleanupBlocked(&result)
	}
	if cfgOK && cfg.ControlURL != "" && hasFunnel {
		result.addFinding(
			inspect.WarningCodeFunnelGlobalControlURLUnknownCompat,
			"",
			"exposure",
			"Global custom control_url is configured while Funnel services exist; TSLink cannot prove Funnel compatibility locally.",
			nil,
		)
	}

	diagnoseCredentialTier1(&result, credentialState, pendingEnrollment, completedEnrollment)
	if result.Alerts.MonitorError != "" {
		result.addFinding(inspect.WarningCodeHealthMonitorSaturated, "", "health_monitor", "Health monitor slots are stuck; some checks were not attempted. Monitoring recovers when reads finish.", nil)
	}
	if result.AccessLog.Drops > 0 || len(result.AccessLog.MissingHistory) > 0 {
		result.addFinding(inspect.WarningCodeAccessLogDrops, "", "access_log", "Access records were dropped; history is incomplete.", nil)
	}
	if result.AccessLog.Error != "" && result.AccessLog.Error != "access_log_not_started" {
		result.addFinding(inspect.WarningCodeAccessLogUnavailable, "", "access_log", "Access logging is unavailable.", nil)
	}
	diagnoseTailscaleSSH(&result)

	result.finalize()
	return result
}

// diagnoseDeviceCleanupBlocked reports, from local files only, what keeps the
// lifecycle reconciler from deleting tailnet devices: an unreadable ownership
// ledger stops all of it, and an unretired record for a service absent from
// the registry stops that service's.
func diagnoseDeviceCleanupBlocked(result *DoctorResult) {
	ownershipPath, err := doctorNodeOwnershipPathFn()
	if err != nil {
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Node ownership ledger path could not be discovered.", evidenceError(err))
		return
	}
	if _, err := tsruntime.LoadOwnership(ownershipPath); err != nil {
		result.addFinding(inspect.WarningCodeDeviceCleanupBlocked, "", "ownership", "The node ownership ledger cannot be read, so TSLink deletes no tailnet device until it is repaired or moved aside.", evidenceError(err))
		return
	}
	names, err := lifecycle.UnretiredOrphanServices(result.Paths.Registry, ownershipPath)
	if err != nil {
		result.addFinding(inspect.WarningCodeDeviceCleanupBlocked, "", "ownership", "Device cleanup could not be checked against registry.json.", evidenceError(err))
		return
	}
	for _, name := range names {
		result.addFinding(inspect.WarningCodeDeviceCleanupBlocked, name, "ownership", "", nil)
	}
}

// diagnoseTailscaleSSH reports Tailscale SSH enablement on this node. All three
// outcomes are info severity, so the check can never move doctor's status or
// exit code; an unreachable local daemon is reported, not treated as a fault.
func diagnoseTailscaleSSH(result *DoctorResult) {
	result.TailscaleSSH = DoctorTailscaleSSH{State: doctorTailscaleSSHUnknown, ACLRuleRequired: true}

	ctx, cancel := context.WithTimeout(context.Background(), doctorTailscaleSSHTimeout)
	enabled, err := doctorTailscaleSSHFn(ctx)
	cancel()
	if errors.Is(err, errTailscaleSSHCheckSkipped) {
		result.addFinding(inspect.WarningCodeTailscaleSSHUnknown, "", "tailscale_ssh",
			"The Tailscale SSH check was skipped, so Tailscale SSH enablement on this node is unknown.",
			map[string]string{"skipped": doctorSkipTailscaleSSHEnv + "=1"})
		return
	}
	if err != nil {
		result.addFinding(inspect.WarningCodeTailscaleSSHUnknown, "", "tailscale_ssh", "", evidenceError(err))
		return
	}
	if enabled {
		result.TailscaleSSH.State = doctorTailscaleSSHEnabled
		result.addFinding(inspect.WarningCodeTailscaleSSHEnabled, "", "tailscale_ssh", "", map[string]string{
			"remote_command": "tailscale ssh <this-host> tslink list --json",
			"also_required":  "tailnet ACL ssh rule admitting the caller",
		})
		return
	}
	result.TailscaleSSH.State = doctorTailscaleSSHDisabled
	result.addFinding(inspect.WarningCodeTailscaleSSHDisabled, "", "tailscale_ssh", "", map[string]string{
		"enable":        "tailscale set --ssh",
		"also_required": "tailnet ACL ssh rule admitting the caller",
	})
}

func discoverDoctorPaths(result *DoctorResult, opts doctorOptions) bool {
	ok := true
	if dir, err := doctorConfigDirFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Config directory path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.ConfigDir = dir
	}
	if opts.RegistryPath != "" {
		result.Paths.Registry = opts.RegistryPath
	} else if path, err := doctorRegistryPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Registry path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.Registry = path
	}
	if opts.RuntimeSnapshotPath != "" {
		result.Paths.RuntimeSnapshot = opts.RuntimeSnapshotPath
	} else if path, err := doctorRuntimeSnapshotPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Runtime snapshot path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.RuntimeSnapshot = path
	}
	if opts.AuthHandoffPath != "" {
		result.Paths.AuthHandoff = opts.AuthHandoffPath
	} else if path, err := doctorAuthHandoffPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Auth handoff path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.AuthHandoff = path
	}
	if opts.PIDPath != "" {
		result.Paths.PID = opts.PIDPath
	} else if path, err := doctorPIDPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "PID path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.PID = path
	}
	return ok
}

func diagnoseCredentials(result *DoctorResult, opts doctorOptions) doctorCredentialState {
	apiToken, apiErr := doctorGetAPIKeyFn()
	clientSecret, clientSecretErr := doctorGetClientSecretFn()
	legacyAuthKey, legacyErr := doctorLegacyAuthKeyConfigured()

	if apiErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "API token credential could not be inspected.", evidenceError(apiErr))
	}
	if clientSecretErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "OAuth client secret credential could not be inspected.", evidenceError(clientSecretErr))
	}
	if legacyErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "Legacy auth key credential could not be inspected.", evidenceError(legacyErr))
	}

	hasAPI := strings.TrimSpace(apiToken) != ""
	hasOAuth := strings.TrimSpace(clientSecret) != ""
	count := 0
	for _, ok := range []bool{hasAPI, hasOAuth, legacyAuthKey} {
		if ok {
			count++
		}
	}
	switch {
	case count == 0:
		result.CredentialMode = doctorCredentialNone
		result.CredentialTier = doctorCredentialTier1
	case count > 1:
		result.CredentialMode = doctorCredentialMixed
		result.CredentialTier = doctorCredentialTier2
	case hasAPI:
		result.CredentialMode = doctorCredentialAPIToken
		result.CredentialTier = doctorCredentialTier2
	case hasOAuth:
		result.CredentialMode = doctorCredentialOAuthClientSecret
		result.CredentialTier = doctorCredentialTier2
	case legacyAuthKey:
		result.CredentialMode = doctorCredentialLegacyAuthKey
		result.CredentialTier = doctorCredentialTier2
	}

	if legacyAuthKey {
		result.addFinding(inspect.WarningCodeCredentialLegacyAuthKey, "", "credentials", "Legacy reusable auth key is configured; prefer API token or OAuth client secret login.", nil)
	}
	if !hasAPI && (hasOAuth || legacyAuthKey) {
		result.addFinding(inspect.WarningCodeCredentialNoAPIClient, "", "credentials", "No API token is configured; remote Tailscale API permissions cannot be proven locally.", nil)
	}
	// Dual-slot posture. Both slots together is the recommended state: OAuth
	// keeps daemon auth durable, the user-owned token keeps invites possible.
	switch {
	case hasAPI && hasOAuth:
		result.addFinding(inspect.WarningCodeCredentialMixedRecommended, "", "credentials", "", nil)
	case hasAPI && !legacyAuthKey:
		result.addFinding(inspect.WarningCodeCredentialAPITokenOnly, "", "credentials", "", map[string]string{"add": "tslink login --client-secret-stdin"})
	case hasOAuth && !legacyAuthKey:
		result.addFinding(inspect.WarningCodeCredentialOAuthClientOnly, "", "credentials", "", map[string]string{"add": "tslink login --api-key-stdin"})
	}
	diagnoseCredentialExpiry(result, credentials.SlotValues{APIKey: apiToken, ClientSecret: clientSecret}, opts)

	credentialFree := count == 0 && apiErr == nil && clientSecretErr == nil && legacyErr == nil
	if count == 0 && !credentialFree {
		result.CredentialTier = doctorCredentialTierUnknown
	}
	return doctorCredentialState{CredentialFree: credentialFree}
}

// diagnoseCredentialExpiry turns the value-free inventory into findings and,
// with --probe-remote, verifies each present slot against the Tailscale API.
func diagnoseCredentialExpiry(result *DoctorResult, values credentials.SlotValues, opts doctorOptions) {
	if strings.TrimSpace(values.APIKey) == "" && strings.TrimSpace(values.ClientSecret) == "" {
		return
	}
	now := doctorNowFn()
	inventory := doctorCredentialInventoryFn(values, now, !opts.ReadOnly)
	result.Credentials, _ = statusCredentialsFromInventory(inventory, false)
	result.Credentials.APIKey.EarlyWarning = credentialEarlyWarning(result.Credentials.APIKey, now)
	result.Credentials.ClientSecret.EarlyWarning = credentialEarlyWarning(result.Credentials.ClientSecret, now)
	if inventory.MetadataError != nil {
		result.addFinding(inspect.WarningCodeCredentialExpiryUnknown, "", "credentials", "", evidenceError(inventory.MetadataError))
	}
	if inventory.BackfillError != nil {
		result.addFinding(inspect.WarningCodeCredentialExpiryUnknown, "", "credentials", "Credential metadata could not be persisted; expiry will be re-evaluated from scratch on every run.", evidenceError(inventory.BackfillError))
	}
	if len(inventory.Backfilled) > 0 {
		result.addFinding(inspect.WarningCodeCredentialMetaBackfilled, "", "credentials", "", map[string]string{"slots": strings.Join(inventory.Backfilled, ",")})
	}

	var unverified []string
	for _, view := range []credentials.SlotView{inventory.APIKey, inventory.ClientSecret} {
		if !view.Present {
			continue
		}
		if view.Metadata != nil && view.Metadata.LastVerifiedAt == nil {
			unverified = append(unverified, view.Slot)
		}
		if view.Slot != credentials.SlotAPIKey {
			continue
		}
		evidence := map[string]string{"slot": view.Slot}
		if view.Metadata != nil {
			if view.Metadata.ExpiresAt != nil {
				evidence["expires_at"] = view.Metadata.ExpiresAt.UTC().Format(time.RFC3339)
			}
			if view.Metadata.ExpiresAtSource != "" {
				evidence["expires_at_source"] = view.Metadata.ExpiresAtSource
			}
		}
		if view.DaysLeft != nil {
			evidence["days_left"] = strconv.Itoa(*view.DaysLeft)
		}
		if view.Metadata != nil {
			e := health.ExpiryAt(view.Metadata.ExpiresAt, view.Metadata.ExpiresAtSource, now, nil)
			if e.Warning == "critical_3d" {
				result.addFinding(inspect.WarningCodeCredentialCritical, "", "credentials", "", evidence)
			}
		}
		switch view.ExpiryState {
		case credentials.ExpiryStateExpiring:
			result.addFinding(inspect.WarningCodeCredentialAPITokenExpiring, "", "credentials", "", evidence)
		case credentials.ExpiryStateExpired:
			result.addFinding(inspect.WarningCodeCredentialAPITokenExpired, "", "credentials", "", evidence)
		case credentials.ExpiryStateUnknown:
			if inventory.MetadataError == nil && inventory.BackfillError == nil {
				result.addFinding(inspect.WarningCodeCredentialExpiryUnknown, "", "credentials", "", evidence)
			}
		}
	}

	if !opts.ProbeRemote {
		if len(unverified) > 0 {
			result.addFinding(inspect.WarningCodeCredentialRemoteUnverified, "", "credentials", "", map[string]string{"slots": strings.Join(unverified, ",")})
		}
		return
	}
	for _, view := range []credentials.SlotView{inventory.APIKey, inventory.ClientSecret} {
		if !view.Present {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), doctorRemoteProbeTimeout)
		outcome, err := doctorProbeCredentialFn(ctx, view.Slot, now)
		cancel()
		if err != nil {
			result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "Credential could not be read for the remote probe.", evidenceError(err))
			continue
		}
		if !outcome.Present {
			continue
		}
		evidence := map[string]string{"slot": view.Slot, "result": outcome.Result}
		if outcome.Cause != nil {
			evidence["error"] = sanitizeDoctorEvidenceValue(outcome.Cause.Error())
		}
		switch outcome.Result {
		case credentials.VerifyResultUnauthorized:
			result.addFinding(inspect.WarningCodeCredentialAPITokenRejected, "", "credentials", "", evidence)
		case credentials.VerifyResultForbidden:
			result.addFinding(inspect.WarningCodeCredentialRemoteForbidden, "", "credentials", "", evidence)
		case credentials.VerifyResultUnreachable:
			result.addFinding(inspect.WarningCodeCredentialRemoteUnreachable, "", "credentials", "", evidence)
		}
	}
}

func doctorLegacyAuthKeyConfigured() (bool, error) {
	path, err := doctorAuthKeyPathFn()
	if err != nil {
		return false, err
	}
	data, err := doctorReadFileFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(data)) != "", nil
}

func diagnoseDaemon(result *DoctorResult, serviceCount int) {
	if result.Paths.PID == "" {
		return
	}
	if !isRunningFn(result.Paths.PID) {
		if serviceCount == 0 {
			return
		}
		// A live PID that provably belongs to another program is a stale PID
		// file, not an uncertain daemon: the daemon really is stopped, and
		// downgrading that to a warning would hide both daemon_not_running and
		// daemon_unsupervised behind an unverifiable-identity note. Only
		// evidence that is merely inconclusive (a sidecar from another build, a
		// timestamp outside tolerance) still earns the conservative treatment.
		scopeErr := checkSupervisorProcessScope()
		if _, err := os.Stat(result.Paths.PID); (!os.IsNotExist(err) && !daemon.IsProcessAbsentFromPIDFile(result.Paths.PID) && !daemon.IsForeignProcessFromPIDFile(result.Paths.PID)) || scopeErr != nil {
			result.Daemon.IdentityUnverified = true
			message := "Daemon identity could not be verified; the process may still be serving (including a different TSLink build). Inspect the PID file, running binary and supervisor with 'tslink status --json' and 'tslink logs' before any install/restart. Backend probes remain enabled."
			if scopeErr != nil {
				message += " " + scopeErr.Error()
			}
			result.addFinding(inspect.WarningCodeDaemonIdentityUnverified, "", "daemon", message, nil)
			return
		}
		result.addFinding(inspect.WarningCodeDaemonNotRunning, "", "daemon", "TSLink daemon is not running; run 'tslink install' to restore service.", nil)
		return
	}
	result.Daemon.Running = true
	pid, err := readPIDFn(result.Paths.PID)
	if err != nil {
		result.addFinding(inspect.WarningCodeDaemonPIDUnreadable, "", "daemon", "Daemon appears to be running, but the PID file could not be read.", evidenceError(err))
		return
	}
	result.Daemon.PID = pid
	diagnoseDaemonBuildSkew(result, result.Paths.PID)
}

// diagnoseDaemonBuildSkew compares the running daemon's self-reported build
// (written into its identity sidecar by WritePIDWithBuildIdentity) against
// this CLI invocation's own build (selfBuildIdentity). It is purely a
// reporting layer: it never changes Running, PID, or IdentityUnverified, and
// a version-skew binary is by design still recognized as running (see
// verifyProcessProduct, which compares Go module identity, not the exact
// build). The comparison is skipped when neither side could determine its
// own build identity — see selfBuildIdentity's "" contract — because there
// is nothing evidenced to compare in that case, only two unrelated unknowns.
func diagnoseDaemonBuildSkew(result *DoctorResult, pidPath string) {
	daemonIdentity := daemon.ReadProcessBuildIdentity(pidPath)
	selfIdentity := selfBuildIdentity()
	result.Daemon.BuildVersion = daemonIdentity.BuildVersion
	result.Daemon.Executable = daemonIdentity.Executable
	// A single equality check is also the "both sides could not determine
	// their own build identity, so there is nothing evidenced to compare"
	// suppression this finding must honor: selfBuildIdentity's contract
	// makes "" mean specifically "unmeasured," so daemonIdentity.BuildVersion
	// == selfIdentity == "" is exactly that case, not a coincidental match.
	// An asymmetric "" on only one side is a genuine difference and still
	// falls through to the warning below, rendered as "unknown" on that side;
	// daemonBuildSkewMessage then picks the remediation that is safe for the
	// side that turned out to be unmeasurable.
	if daemonIdentity.BuildVersion == selfIdentity {
		return
	}
	result.Daemon.BuildSkew = true
	result.addFinding(
		inspect.WarningCodeDaemonBuildSkew,
		"",
		"daemon",
		daemonBuildSkewMessage(daemonIdentity.BuildVersion, selfIdentity),
		map[string]string{
			"daemon_build":      daemonIdentity.BuildVersion,
			"cli_build":         selfIdentity,
			"daemon_executable": daemonIdentity.Executable,
		},
	)
}

// daemonBuildSkewMessage renders the skew warning with a remediation that is
// safe to act on in the direction the skew was actually observed, so that
// following either branch literally leaves the installation no worse off.
//
// When this CLI carries a build stamp, reinstalling from it moves the daemon
// onto that known build, which is the fix regardless of whether the daemon's
// own side was measurable. When this CLI carries no stamp at all — the ""
// case of selfBuildIdentity's contract, which is how a development or
// non-release build reports itself — the same advice would replace a daemon
// whose build is identified with one that is not, so the remediation asks for
// a release build of the CLI instead of sending this one to 'tslink install'.
func daemonBuildSkewMessage(daemonBuild, cliBuild string) string {
	if cliBuild == "" {
		return fmt.Sprintf(
			"Daemon build (%s) differs from this CLI's build (%s): this CLI carries no build stamp, which is how a development or non-release build reports itself. Re-run 'tslink doctor' from a release build of tslink to compare the two; installing from this binary would replace the daemon's identified build with an unidentified one.",
			doctorDisplayBuildIdentity(daemonBuild),
			doctorDisplayBuildIdentity(cliBuild),
		)
	}
	return fmt.Sprintf(
		"Daemon build (%s) differs from this CLI's build (%s); run 'tslink install' to restart the daemon with the current binary.",
		doctorDisplayBuildIdentity(daemonBuild),
		doctorDisplayBuildIdentity(cliBuild),
	)
}

// doctorDisplayBuildIdentity renders an empty build identity as an explicit
// "unknown" phrase instead of an empty string, so the finding message never
// reads as if one side's build were literally blank.
func doctorDisplayBuildIdentity(identity string) string {
	if identity == "" {
		return "unknown"
	}
	return identity
}

func diagnosePendingEnrollment(result *DoctorResult) bool {
	if result.Paths.AuthHandoff == "" {
		return false
	}
	handoff, err := doctorLoadAuthHandoffFn(result.Paths.AuthHandoff)
	if err != nil {
		return false
	}
	return !result.Daemon.Running || handoff.DaemonPID == result.Daemon.PID
}

func diagnoseRuntimeSnapshot(result *DoctorResult, fingerprint string, suppressMissing bool) bool {
	snapshot, loadErr := runtimeLoadSnapshotFn(result.Paths.RuntimeSnapshot)
	expected := tsruntime.ExpectedRuntime{
		CurrentRegistryFingerprint: fingerprint,
	}
	if result.Daemon.Running {
		expected.DaemonPID = result.Daemon.PID
		if lowerBound, err := pidFileModTimeFn(result.Paths.PID); err == nil {
			expected.DaemonStartedAtLowerBound = lowerBound
		}
	}
	freshness := tsruntime.Classify(snapshot, loadErr, expected)
	result.RuntimeSnapshot = runtimeSnapshotResult(snapshot, freshness)
	if snapshot != nil {
		result.Alerts = alertsWithSnapshot(result.Alerts, snapshot.Alerts, result.Daemon.Running && snapshotContributesRuntimeEvidence(freshness))
	}
	// completedEnrollment mirrors status's per-service evidence rule: only a
	// snapshot entry that is actually running counts as positive enrollment
	// evidence. Counting failed or stale entries here would let doctor report
	// authorized runtime state while status shows nothing up.
	completedEnrollment := false
	if snapshotContributesRuntimeEvidence(freshness) && snapshot != nil {
		for _, service := range snapshot.Services {
			for _, warning := range service.Warnings {
				if flag := registry.RequestLimitFlag(warning.Code); flag != "" {
					result.addFinding(warning.Code, service.Name, "http.request_limits", warning.Message, map[string]string{"suggested_flag": flag})
				}
			}
		}
		result.canonicalHosts = map[string]string{}
		for _, svc := range snapshot.Services {
			if runtimeServiceRunning(svc) && svc.Endpoint.State == inspect.EndpointStateExact {
				result.canonicalHosts[svc.Name] = registry.CanonicalProxyHost(svc.CertDomains, svc.Endpoint.Host)
			}
		}

		if result.NodeKeys == nil {
			result.NodeKeys = map[string]health.Expiry{}
		}
		for _, svc := range snapshot.Services {
			e := health.ExpiryAt(svc.NodeKey.ExpiresAt, svc.NodeKey.Source, doctorNowFn(), nodeExpiryNext())
			result.NodeKeys[svc.Name] = e
			code := inspect.WarningCodeNodeKeyUnknown
			switch e.Warning {
			case "warning_14d":
				code = inspect.WarningCodeNodeKeyExpiring
			case "critical_3d":
				code = inspect.WarningCodeNodeKeyCritical
			case "expired":
				code = inspect.WarningCodeNodeKeyExpired
			default:
				if e.DaysLeft != nil {
					continue
				}
			}
			evidence := map[string]string{"source": e.Source}
			if e.DaysLeft != nil {
				evidence["days_left"] = strconv.Itoa(*e.DaysLeft)
			}
			result.addFinding(code, svc.Name, "node_key", "", evidence)
		}
	}
	if snapshotContributesRuntimeEvidence(freshness) && snapshot != nil {
		for i := range snapshot.Services {
			state := snapshot.Services[i].RuntimeState
			if state == "" || state == tsruntime.ServiceRuntimeRunning {
				completedEnrollment = true
				break
			}
		}
	}
	if freshness.Code != "" {
		if suppressMissing && freshness.Code == inspect.WarningCodeRuntimeSnapshotMissing {
			return completedEnrollment
		}
		message := freshness.Message
		if message == "" {
			message = inspect.WarningCodeRegistry[freshness.Code].Description
		}
		result.addFinding(freshness.Code, "", "runtime_snapshot", message, nil)
	}
	return completedEnrollment
}

func diagnoseCredentialTier1(result *DoctorResult, state doctorCredentialState, pendingEnrollment, completedEnrollment bool) {
	if !state.CredentialFree {
		return
	}

	switch {
	case completedEnrollment:
		// Authorized runtime state is positive evidence that enrollment
		// completed. A lingering handoff file must not keep doctor reporting a
		// pending enrollment after the node is already serving.
		result.addFinding(inspect.WarningCodeCredentialTier1, "", "credentials", "Tier 1 is active without a stored credential; interactive enrollment has produced authorized runtime state.", nil)
	case pendingEnrollment:
		result.addFinding(inspect.WarningCodeCredentialTier1, "", "credentials", "Tier 1 is active without a stored credential; interactive enrollment is pending. Open the authorization URL reported by 'tslink status'.", nil)
	case result.Daemon.Running:
		result.addFinding(inspect.WarningCodeCredentialTier1, "", "credentials", "Tier 1 is active without a stored credential; the daemon is running and waiting for interactive enrollment evidence.", nil)
	default:
		result.addFinding(inspect.WarningCodeCredentialNone, "", "credentials", "Tier 1 uses interactive enrollment and no stored credential is required. Run 'tslink serve' to enroll, or run 'tslink login' only for optional Tier 2 durable installs.", nil)
	}
}

func diagnoseService(result *DoctorResult, svc registry.Service, opts doctorOptions) {
	view := inspect.ServiceViewFor(svc)
	for _, warning := range view.Warnings {
		result.addFinding(warning.Code, svc.Name, warning.Source, warning.Message, nil)
	}

	if err := registry.ValidateService(svc); err != nil {
		code := doctorRegistryValidationCode(svc, err)
		message := "Service failed registry validation."
		if code == inspect.WarningCodeControlURLInvalid {
			message = "Service control_url is invalid."
		} else if code == inspect.WarningCodeTCPAllowedUsersInvalid {
			message = "Raw TCP service cannot enforce allowed_users."
		} else if stableCode, ok := registry.ErrorCode(err); ok && stableCode != "" {
			if meta := inspect.WarningCodeRegistry[stableCode]; meta.Description != "" {
				message = meta.Description
			}
		}
		result.addFinding(code, svc.Name, "registry", message, nil)
	}

	if len(svc.AllowedUsers) > 0 {
		result.addFinding(inspect.WarningCodeIdentityResolutionUnknown, svc.Name, "identity", "Local doctor cannot prove real Tailscale identity resolution or remote ACL policy for this allow list.", map[string]string{
			"allow_count": strconv.Itoa(len(svc.AllowedUsers)),
		})
	}

	switch svc.Type {
	case registry.TypeProxy:
		diagnoseNetworkTarget(result, svc, opts, inspect.WarningCodeProxyNonLoopbackTarget)
	case registry.TypeTCP:
		diagnoseNetworkTarget(result, svc, opts, inspect.WarningCodeTCPNonLoopbackTarget)
	case registry.TypeFile:
		diagnoseFileTarget(result, svc)
	}
}

func doctorRegistryValidationCode(svc registry.Service, err error) string {
	if code, ok := registry.ErrorCode(err); ok && code != "" {
		return code
	}
	if svc.Type == registry.TypeTCP && len(svc.AllowedUsers) > 0 {
		return inspect.WarningCodeTCPAllowedUsersInvalid
	}
	if svc.ControlURL != "" {
		if controlErr := registry.ValidateControlURL(svc.ControlURL); controlErr != nil {
			return inspect.WarningCodeControlURLInvalid
		}
	}
	return inspect.WarningCodeRegistryServiceInvalid
}

func diagnoseNetworkTarget(result *DoctorResult, svc registry.Service, opts doctorOptions, nonLoopbackCode string) {
	target, err := classifyServiceTarget(svc)
	if err != nil {
		result.addFinding(inspect.WarningCodeTargetInvalid, svc.Name, "target", "Service target could not be parsed for probing.", evidenceError(err))
		return
	}
	if target.External {
		result.addFinding(nonLoopbackCode, svc.Name, "target", "Service target is not loopback/local; review whether TSLink should proxy to this host.", map[string]string{
			"host": target.Host,
		})
		if !opts.ProbeExternal {
			result.addFinding(inspect.WarningCodeTargetProbeSkippedExternal, svc.Name, "target_probe", "External target probe skipped; rerun with --probe-external to probe this target.", map[string]string{
				"host": target.Host,
			})
			return
		}
	}

	if !result.Daemon.Running && !result.Daemon.IdentityUnverified {
		result.addFinding(inspect.WarningCodeTargetProbeSkippedDaemon, svc.Name, "target_probe", "Backend probe deferred until TSLink is running; run 'tslink install' and then 'tslink doctor'.", nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), doctorProbeTimeout)
	defer cancel()
	if err := doctorProbeTargetFn(ctx, target.ProbeAddress, doctorProbeTimeout); err != nil {
		code := classifyProbeError(err)
		result.addFinding(code, svc.Name, "target_probe", inspect.WarningCodeRegistry[code].Description, nil)
		return
	}
	if svc.Type == registry.TypeProxy {
		if code := doctorHTTPProbeFn(health.WithCanonicalHost(context.Background(), result.canonicalHosts[svc.Name]), svc); code != "" {
			result.addFinding(inspect.WarningCodeAppProbeFailed, svc.Name, "app_probe", "", map[string]string{"error_code": code})
		}
	}

}

func classifyServiceTarget(svc registry.Service) (doctorTarget, error) {
	switch svc.Type {
	case registry.TypeProxy:
		return classifyProxyTarget(svc.Target)
	case registry.TypeTCP:
		return classifyHostPortTarget(svc.Target, "")
	default:
		return doctorTarget{}, fmt.Errorf("unsupported target type %q", svc.Type)
	}
}

func classifyProxyTarget(raw string) (doctorTarget, error) {
	if raw == "" {
		return doctorTarget{}, fmt.Errorf("empty proxy target")
	}
	if !strings.Contains(raw, "://") {
		return classifyHostPortTarget(raw, "80")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return doctorTarget{}, err
	}
	if parsed.Host == "" {
		return doctorTarget{}, fmt.Errorf("proxy target host is empty")
	}
	defaultPort := "80"
	if strings.EqualFold(parsed.Scheme, "https") {
		defaultPort = "443"
	}
	return classifyHostPortTarget(parsed.Host, defaultPort)
}

func classifyHostPortTarget(authority, defaultPort string) (doctorTarget, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if defaultPort == "" {
			return doctorTarget{}, err
		}
		if authorityHasExplicitPort(authority) {
			return doctorTarget{}, err
		}
		host = strings.Trim(authority, "[]")
		port = defaultPort
	}
	if port == "" {
		return doctorTarget{}, fmt.Errorf("missing target port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort <= 0 || parsedPort > 65535 {
		return doctorTarget{}, fmt.Errorf("invalid target port %q", port)
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	external := !isLocalTargetHost(host)
	dialHost := host
	if dialHost == "" || isUnspecifiedIP(dialHost) {
		dialHost = "127.0.0.1"
	}
	return doctorTarget{
		Host:         host,
		Port:         port,
		ProbeAddress: net.JoinHostPort(dialHost, port),
		External:     external,
	}, nil
}

func authorityHasExplicitPort(authority string) bool {
	if strings.HasPrefix(authority, "[") {
		return strings.Contains(authority, "]:")
	}
	return strings.Count(authority, ":") == 1
}

func isLocalTargetHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func isUnspecifiedIP(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsUnspecified()
}

func defaultDoctorProbeTarget(ctx context.Context, address string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

func classifyProbeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return inspect.WarningCodeTargetProbeTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return inspect.WarningCodeTargetProbeTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, windowsWSAECONNREFUSED) || errors.Is(err, windowsWSAECONNRESET) {
		return inspect.WarningCodeTargetProbeRefused
	}
	return inspect.WarningCodeTargetProbeFailed
}

func diagnoseFileTarget(result *DoctorResult, svc registry.Service) {
	path := svc.Path
	if path == "" {
		result.addFinding(inspect.WarningCodeFilePathMissing, svc.Name, "file", "File service path is empty.", nil)
		return
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	info, err := doctorStatFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.addFinding(inspect.WarningCodeFilePathMissing, svc.Name, "file", "File service path does not exist.", nil)
			return
		}
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path cannot be inspected.", evidenceError(err))
		return
	}
	if !info.IsDir() {
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path is not a directory.", nil)
		return
	}
	handle, err := doctorOpenPathFn(path)
	if err != nil {
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path is not readable.", evidenceError(err))
		return
	}
	_ = handle.Close()
}

func (r *DoctorResult) addFinding(code, service, area, message string, evidence map[string]string) {
	meta, ok := inspect.WarningCodeRegistry[code]
	if !ok {
		meta = inspect.WarningCodeMeta{Severity: doctorSeverityError, Source: area, Description: message}
	}
	if area == "" {
		area = meta.Source
	}
	if message == "" {
		message = meta.Description
	}
	finding := DoctorFinding{
		Code:     code,
		Severity: meta.Severity,
		Area:     area,
		Message:  message,
		Service:  service,
		Evidence: evidence,
	}
	for _, existing := range r.Findings {
		if existing.Code == finding.Code && existing.Service == finding.Service {
			return
		}
	}
	r.Findings = append(r.Findings, finding)
}

func (r *DoctorResult) finalize() {
	r.Counts.Findings = len(r.Findings)
	for _, finding := range r.Findings {
		switch finding.Severity {
		case doctorSeverityInfo:
			r.Counts.Info++
		case doctorSeverityWarning:
			r.Counts.Warnings++
		case doctorSeverityError:
			r.Counts.Errors++
		case doctorSeverityCritical:
			r.Counts.Critical++
		}
	}
	switch {
	case r.Counts.Critical > 0:
		r.Status = doctorStatusCritical
		r.HealthStatus = doctorStatusCritical
		r.HealthExitCode = output.ExitCritical
	case r.Counts.Errors > 0:
		r.Status = doctorStatusError
		r.HealthStatus = doctorStatusError
		r.HealthExitCode = output.ExitCritical
	case r.Counts.Warnings > 0:
		r.Status = doctorStatusWarning
		r.HealthStatus = doctorStatusWarning
		r.HealthExitCode = output.ExitWarning
	default:
		r.Status = doctorStatusOK
		r.HealthStatus = doctorStatusOK
		r.HealthExitCode = output.ExitSuccess
	}
}

func doctorExit(result DoctorResult) error {
	if result.Counts.Errors > 0 || result.Counts.Critical > 0 {
		return output.SilentExit(output.ExitCritical)
	}
	if result.Counts.Warnings > 0 {
		return output.SilentExit(output.ExitWarning)
	}
	return nil
}

func formatDoctor(result DoctorResult, out io.Writer) {
	formatAccessHealth(result.AccessLog, out)
	formatEarlyWarnings(out, result.Credentials)
	formatAlerts(out, result.Alerts)
	for name, key := range result.NodeKeys {
		if key.DaysLeft == nil {
			fmt.Fprintf(out, "Node %s key expiry: unknown\n", name)
		} else {
			fmt.Fprintf(out, "Node %s key expiry: %dd left %s\n", name, *key.DaysLeft, key.Warning)
		}
		for _, step := range key.Next {
			fmt.Fprintf(out, "Next: %s\n", step)
		}
	}
	fmt.Fprintf(
		out,
		"TSLink doctor: %s (%d errors, %d warnings, %d info)\n",
		result.Status,
		result.Counts.Errors+result.Counts.Critical,
		result.Counts.Warnings,
		result.Counts.Info,
	)
	fmt.Fprintf(out, "Config: %s\n", emptyDash(result.Paths.ConfigDir))
	fmt.Fprintf(out, "Registry: %s (%d services)\n", emptyDash(result.Paths.Registry), result.Counts.Services)
	fmt.Fprintf(out, "Credential tier: %s\n", formatDoctorCredentialTier(result))
	formatSupervision(result.Supervision, out)
	if result.Daemon.Running {
		fmt.Fprintf(out, "Daemon: running (pid %d)\n", result.Daemon.PID)
	} else {
		fmt.Fprintln(out, "Daemon: not running")
	}
	fmt.Fprintf(out, "Runtime snapshot: %s", emptyDash(result.RuntimeSnapshot.Status))
	if result.RuntimeSnapshot.Code != "" {
		fmt.Fprintf(out, " (%s)", result.RuntimeSnapshot.Code)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "Tailscale SSH (this node): %s\n", emptyDash(result.TailscaleSSH.State))
	if len(result.Findings) == 0 {
		fmt.Fprintln(out, "\nNo warnings or errors found.")
		return
	}

	fmt.Fprintln(out, "\nFindings:")
	for _, finding := range result.Findings {
		service := ""
		if finding.Service != "" {
			service = " service=" + finding.Service
		}
		fmt.Fprintf(out, "- %s [%s] %s%s: %s", strings.ToUpper(finding.Severity), finding.Code, finding.Area, service, finding.Message)
		if len(finding.Evidence) > 0 {
			fmt.Fprintf(out, " (%s)", formatEvidence(finding.Evidence))
		}
		fmt.Fprintln(out)
	}
}

func formatDoctorCredentialTier(result DoctorResult) string {
	switch result.CredentialTier {
	case doctorCredentialTier1:
		return "Tier 1 (interactive enrollment; no stored administrative credential)"
	case doctorCredentialTier2:
		return fmt.Sprintf("Tier 2 (stored credential mode: %s)", result.CredentialMode)
	default:
		return fmt.Sprintf("unknown (credential mode: %s)", result.CredentialMode)
	}
}

func formatEvidence(evidence map[string]string) string {
	keys := make([]string, 0, len(evidence))
	for key := range evidence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+evidence[key])
	}
	return strings.Join(parts, ", ")
}

func evidenceError(err error) map[string]string {
	if err == nil {
		return nil
	}
	return map[string]string{"error": sanitizeDoctorEvidenceValue(err.Error())}
}

func evidenceControlURLInvalid() map[string]string {
	return map[string]string{"error": "control_url failed validation"}
}

func sanitizeDoctorEvidenceValue(value string) string {
	value = doctorURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			if strings.ContainsAny(raw, "?@") || doctorCredentialTokenPattern.MatchString(raw) {
				return doctorRedactedURL
			}
			return raw
		}
		if parsed.User != nil || parsed.RawQuery != "" || doctorCredentialTokenPattern.MatchString(raw) {
			return doctorRedactedURL
		}
		return raw
	})
	value = doctorCredentialTokenPattern.ReplaceAllString(value, doctorRedactedEvidenceValue)
	return doctorUserInfoPattern.ReplaceAllString(value, doctorRedactedEvidenceValue+"@")
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose TSLink safety and runtime evidence",
	Long: `Diagnose local TSLink configuration, credentials, daemon liveness,
runtime snapshot freshness, service guardrails, and reachable local targets.

Doctor is read-only for the registry, Tailscale policy, and service state.
The only file it may write is the value-free credential bookkeeping
(~/.config/tslink/credential-meta.json): it backfills missing expiry metadata
for credentials stored before tracking existed, and --probe-remote records the
verification result. Credential findings: credential_mixed_recommended (both
slots, recommended), credential_api_token_only (daemon auth depends on an
expiring token), credential_oauth_client_only (invites need a user-owned token),
credential_api_token_expiring (<= 14 days left), credential_api_token_expired,
credential_expiry_unknown, credential_remote_unverified, and with
--probe-remote credential_api_token_rejected (HTTP 401),
credential_remote_forbidden (HTTP 403), credential_remote_unreachable.

Doctor also reports Tailscale SSH enablement for this node
(tailscale_ssh_enabled / tailscale_ssh_disabled / tailscale_ssh_unknown), read
from the local Tailscale client. Tailscale SSH is a tailscaled feature that
TSLink neither installs nor requires; it is reported because
'tailscale ssh <this-host> tslink <command>' is the zero-code way to drive this
install from another machine, and it needs both 'tailscale set --ssh' here and
a tailnet ACL ssh rule admitting the caller. All three outcomes are
informational and never change doctor's status or exit code. Set
TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1 to skip the local read; the state is then
unknown and the tailscale_ssh_unknown finding says the check was skipped.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		probeExternal, err := cmd.Flags().GetBool("probe-external")
		if err != nil {
			return err
		}
		probeRemote, err := cmd.Flags().GetBool("probe-remote")
		if err != nil {
			return err
		}
		return runDoctor(cmd.OutOrStdout(), doctorOptions{ProbeExternal: probeExternal, ProbeRemote: probeRemote}, jsonOutput(cmd))
	},
}

func init() {
	doctorCmd.Flags().Bool("probe-external", false, "Probe non-loopback service targets")
	doctorCmd.Flags().Bool("probe-remote", false, "Verify each stored credential against the Tailscale API with one device-list read and record last_verified in credential-meta.json")
	rootCmd.AddCommand(doctorCmd)
}
