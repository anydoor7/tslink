package cmd

import (
	"fmt"
	"io"
	"os"
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
	runtimeLoadSnapshotFn       = tsruntime.Load
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
	DaemonRunning bool `json:"daemon_running"`
	DaemonPID     int  `json:"daemon_pid"`
	Authenticated bool `json:"authenticated"`
	ServiceCount  int  `json:"service_count"`
}

type StatusURLsResult struct {
	SchemaVersion   string                      `json:"schema_version"`
	DaemonRunning   bool                        `json:"daemon_running"`
	DaemonPID       int                         `json:"daemon_pid"`
	Authenticated   bool                        `json:"authenticated"`
	ServiceCount    int                         `json:"service_count"`
	RuntimeSnapshot StatusRuntimeSnapshotResult `json:"runtime_snapshot"`
	Services        []StatusServiceView         `json:"services"`
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
	var r StatusResult
	if isRunningFn(pidPath) {
		r.DaemonRunning = true
		r.DaemonPID, _ = readPIDFn(pidPath)
	}
	if apiKey, _ := getAPIKeyFn(); apiKey != "" {
		r.Authenticated = true
	} else if hasClientSecretFn() {
		r.Authenticated = true
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return StatusResult{}, err
	}
	r.ServiceCount = len(reg.Services)
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
	} else {
		fmt.Fprintln(out, "→ tailnet: not authenticated (run: tslink login)")
	}
	fmt.Fprintf(out, "→ services: %d registered\n", r.ServiceCount)
}

func getStatusURLs(pidPath, regPath, snapshotPath string) (StatusURLsResult, error) {
	status, err := getStatus(pidPath, regPath)
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
		SchemaVersion:   inspect.SchemaVersion,
		DaemonRunning:   status.DaemonRunning,
		DaemonPID:       status.DaemonPID,
		Authenticated:   status.Authenticated,
		ServiceCount:    len(reg.Services),
		RuntimeSnapshot: runtimeSnapshotResult(snapshot, freshness),
		Services:        make([]StatusServiceView, 0, len(reg.Services)),
	}

	snapshotServices := map[string]tsruntime.ServiceSnapshot{}
	if freshness.Exact && snapshot != nil {
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

		if freshness.Exact {
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
		} else {
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

func endpointStateForFreshness(freshness tsruntime.Freshness) string {
	switch freshness.Status {
	case tsruntime.StatusMissing:
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
		DaemonRunning: r.DaemonRunning,
		DaemonPID:     r.DaemonPID,
		Authenticated: r.Authenticated,
		ServiceCount:  r.ServiceCount,
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
Tailscale authentication state, and number of registered services.

Output lines:
  → tslink: running (pid 12345)     Daemon is active with its process ID
  → tslink: not running             Daemon is not active
  → tailnet: authenticated          Valid API key or OAuth client secret found
  → tailnet: not authenticated      No credentials — run 'tslink login'
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
			r, err := getStatusURLs(pidPath, regPath, snapshotPath)
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

		r, err := getStatus(pidPath, regPath)
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
