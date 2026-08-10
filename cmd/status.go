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
	pidFileModTimeFn            = func(path string) (time.Time, error) {
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
)

// StatusResult holds the status information for display.
type StatusResult struct {
	DaemonRunning          bool                 `json:"daemon_running"`
	DaemonPID              int                  `json:"daemon_pid"`
	Authenticated          bool                 `json:"authenticated"`
	CredentialStored       bool                 `json:"credential_stored"`
	NodeAuthorized         bool                 `json:"node_authorized"`
	AuthorizedServiceCount int                  `json:"authorized_service_count"`
	AuthStatus             string               `json:"auth_status"`
	AuthURL                string               `json:"auth_url,omitempty"`
	ExpiresAt              *time.Time           `json:"expires_at,omitempty"`
	ServiceCount           int                  `json:"service_count"`
	Services               []StatusServiceState `json:"services"`
}

type StatusServiceState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type StatusURLsResult struct {
	SchemaVersion          string                      `json:"schema_version"`
	DaemonRunning          bool                        `json:"daemon_running"`
	DaemonPID              int                         `json:"daemon_pid"`
	Authenticated          bool                        `json:"authenticated"`
	CredentialStored       bool                        `json:"credential_stored"`
	NodeAuthorized         bool                        `json:"node_authorized"`
	AuthorizedServiceCount int                         `json:"authorized_service_count"`
	AuthStatus             string                      `json:"auth_status"`
	AuthURL                string                      `json:"auth_url,omitempty"`
	ExpiresAt              *time.Time                  `json:"expires_at,omitempty"`
	ServiceCount           int                         `json:"service_count"`
	RuntimeSnapshot        StatusRuntimeSnapshotResult `json:"runtime_snapshot"`
	Services               []StatusServiceView         `json:"services"`
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
	Name     string                `json:"name"`
	Type     string                `json:"type"`
	Endpoint inspect.EndpointView  `json:"endpoint"`
	Exposure inspect.ExposureView  `json:"exposure"`
	Allow    inspect.SummaryView   `json:"allow"`
	Tags     inspect.SummaryView   `json:"tags"`
	Backend  inspect.BackendView   `json:"backend"`
	Warnings []inspect.WarningView `json:"warnings,omitempty"`
}

func getStatus(pidPath, regPath string) (StatusResult, error) {
	r := StatusResult{AuthStatus: authStatusNotAuthenticated}
	if isRunningFn(pidPath) {
		r.DaemonRunning = true
		r.DaemonPID, _ = readPIDFn(pidPath)
	}
	if apiKey, _ := getAPIKeyFn(); apiKey != "" {
		r.CredentialStored = true
		r.Authenticated = true
		r.AuthStatus = authStatusAuthenticated
	} else if hasClientSecretFn() {
		r.CredentialStored = true
		r.Authenticated = true
		r.AuthStatus = authStatusAuthenticated
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	r.ServiceCount = len(reg.Services)
	r.Services = make([]StatusServiceState, 0, len(reg.Services))
	for _, svc := range reg.Services {
		r.Services = append(r.Services, StatusServiceState{Name: svc.Name, Status: "down"})
	}
	return r, nil
}

func getPollableStatus(pidPath, regPath, snapshotPath, authHandoffPath string) (StatusResult, error) {
	r, err := getStatus(pidPath, regPath)
	if err != nil {
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
		for _, svc := range snapshot.Services {
			up[svc.Name] = struct{}{}
		}
		for i := range r.Services {
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
	return r, nil
}

func formatStatus(r StatusResult, out io.Writer) {
	if r.DaemonRunning {
		fmt.Fprintf(out, "→ tslink: running (pid %d)\n", r.DaemonPID)
	} else {
		fmt.Fprintln(out, "→ tslink: not running")
	}
	if r.Authenticated {
		fmt.Fprintln(out, "→ tailnet: authenticated")
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
		SchemaVersion:          inspect.SchemaVersion,
		DaemonRunning:          status.DaemonRunning,
		DaemonPID:              status.DaemonPID,
		Authenticated:          status.Authenticated,
		CredentialStored:       status.CredentialStored,
		NodeAuthorized:         status.NodeAuthorized,
		AuthorizedServiceCount: status.AuthorizedServiceCount,
		AuthStatus:             status.AuthStatus,
		AuthURL:                status.AuthURL,
		ExpiresAt:              status.ExpiresAt,
		ServiceCount:           len(reg.Services),
		RuntimeSnapshot:        runtimeSnapshotResult(snapshot, freshness),
		Services:               make([]StatusServiceView, 0, len(reg.Services)),
	}

	snapshotServices := map[string]tsruntime.ServiceSnapshot{}
	if snapshotReportsServices(freshness) && snapshot != nil {
		for _, svc := range snapshot.Services {
			snapshotServices[svc.Name] = svc
		}
	}

	for _, svc := range reg.Services {
		view := inspect.ServiceViewFor(svc)
		service := StatusServiceView{
			Name:     view.Name,
			Type:     view.Type,
			Endpoint: view.Endpoint,
			Exposure: view.Exposure,
			Allow:    view.Allow,
			Tags:     view.Tags,
			Backend:  view.Backend,
			Warnings: append([]inspect.WarningView(nil), view.Warnings...),
		}

		switch {
		case freshness.Exact:
			snapshotService, ok := snapshotServices[svc.Name]
			switch {
			case ok && snapshotService.Endpoint.State == inspect.EndpointStateExact:
				service.Endpoint = snapshotService.Endpoint
				service.Endpoint.State = inspect.EndpointStateExact
				service.Exposure = snapshotService.Exposure
			case ok:
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
			snapshotService, ok := snapshotServices[svc.Name]
			switch {
			case ok && snapshotService.Endpoint.State == inspect.EndpointStateExact:
				// A partial snapshot is not authoritative for omissions, but an
				// included service is positive runtime evidence and keeps sequential
				// interactive-enrollment progress visible.
				service.Endpoint = snapshotService.Endpoint
				service.Endpoint.State = inspect.EndpointStateExact
				service.Exposure = snapshotService.Exposure
			case ok:
				service.Endpoint.State = statusEndpointStateExpectedUnverified
			default:
				service.Endpoint.State = statusEndpointStateMissing
			}
			service.Warnings = appendRuntimeFreshnessWarning(service.Warnings, freshness)
		default:
			service.Endpoint.State = endpointStateForFreshness(freshness)
			service.Warnings = appendRuntimeFreshnessWarning(service.Warnings, freshness)
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
		DaemonRunning:          r.DaemonRunning,
		DaemonPID:              r.DaemonPID,
		Authenticated:          r.Authenticated,
		CredentialStored:       r.CredentialStored,
		NodeAuthorized:         r.NodeAuthorized,
		AuthorizedServiceCount: r.AuthorizedServiceCount,
		AuthStatus:             r.AuthStatus,
		AuthURL:                r.AuthURL,
		ExpiresAt:              r.ExpiresAt,
		ServiceCount:           r.ServiceCount,
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
	fmt.Fprintln(writer, "NAME\tTYPE\tENDPOINT\tSTATE\tEXPOSURE\tALLOW\tTAGS\tBACKEND\tWARNINGS")
	for _, svc := range r.Services {
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			svc.Name,
			svc.Type,
			emptyDash(svc.Endpoint.Display),
			emptyDash(svc.Endpoint.State),
			emptyDash(svc.Exposure.Kind),
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
