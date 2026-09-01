package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	registryPathFn            = config.RegistryPath
	listPIDPathFn             = config.PIDPath
	listRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
)

type listOptions struct {
	Name    string
	Type    string
	Fields  []string
	Verbose bool
}

const listStatePending = "pending"

// ListServiceSummary is the token-efficient default service representation.
// URL is null until runtime.json contains exact evidence for the current daemon
// and registry fingerprint.
type ListServiceSummary struct {
	Name            string                  `json:"name"`
	Type            string                  `json:"type"`
	URL             *string                 `json:"url"`
	URLPending      bool                    `json:"url_pending"`
	State           string                  `json:"state"`
	FunnelRequested bool                    `json:"funnel_requested"`
	FunnelActive    bool                    `json:"funnel_active"`
	FunnelState     string                  `json:"funnel_state"`
	FunnelExpiresAt *time.Time              `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                 `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError `json:"error,omitempty"`
}

// ListResult holds the result for JSON output.
type ListResult struct {
	SchemaVersion string `json:"schema_version"`
	Services      any    `json:"services"`
	Count         int    `json:"count"`
}

func validateListOptions(opts listOptions) error {
	if opts.Name != "" {
		if err := registry.ValidateName(opts.Name); err != nil {
			return err
		}
	}
	if opts.Type != "" && opts.Type != registry.TypeProxy && opts.Type != registry.TypeFile && opts.Type != registry.TypeTCP {
		return output.ErrUsage("--type must be one of: proxy, file, tcp")
	}
	if opts.Verbose && len(opts.Fields) > 0 {
		return output.ErrUsage("--verbose conflicts with --fields")
	}
	allowed := map[string]bool{
		"name": true, "type": true, "url": true, "url_pending": true, "state": true,
		"funnel_requested": true, "funnel_active": true, "funnel_state": true,
		"funnel_expires_at": true, "funnel_remaining": true, "error": true,
	}
	for _, field := range opts.Fields {
		if !allowed[field] {
			return output.ErrUsage(fmt.Sprintf("unknown --fields value %q; supported: name,type,url,url_pending,state,funnel_requested,funnel_active,funnel_state,funnel_expires_at,funnel_remaining,error", field))
		}
	}
	return nil
}

func parseListFields(raw string) []string {
	var fields []string
	seen := map[string]bool{}
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field != "" && !seen[field] {
			seen[field] = true
			fields = append(fields, field)
		}
	}
	return fields
}

func filterStatusServices(result StatusURLsResult, opts listOptions) ([]StatusServiceView, error) {
	filtered := make([]StatusServiceView, 0, len(result.Services))
	for _, svc := range result.Services {
		if opts.Name != "" && svc.Name != opts.Name {
			continue
		}
		if opts.Type != "" && svc.Type != opts.Type {
			continue
		}
		filtered = append(filtered, svc)
	}
	if opts.Name != "" && len(filtered) == 0 {
		return nil, output.ErrNotFound(fmt.Sprintf("service not found: %s", opts.Name))
	}
	return filtered, nil
}

func listSummary(svc StatusServiceView) ListServiceSummary {
	summary := ListServiceSummary{
		Name:            svc.Name,
		Type:            svc.Type,
		URLPending:      true,
		State:           listStatePending,
		FunnelRequested: svc.FunnelRequested,
		FunnelActive:    svc.FunnelActive,
		FunnelState:     svc.FunnelState,
		FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
		FunnelRemaining: svc.FunnelRemaining,
		Error:           svc.Error,
	}
	if svc.RuntimeState == tsruntime.ServiceRuntimeFailed {
		summary.State = tsruntime.ServiceRuntimeFailed
	}
	if svc.Endpoint.State == inspect.EndpointStateExact && svc.Endpoint.Display != "" && !strings.Contains(svc.Endpoint.Display, "<tailnet>") {
		url := svc.Endpoint.Display
		summary.URL = &url
		summary.URLPending = false
		summary.State = inspect.EndpointStateExact
	}
	return summary
}

func selectListFields(summary ListServiceSummary, fields []string) map[string]any {
	selected := make(map[string]any, len(fields))
	for _, field := range fields {
		switch field {
		case "name":
			selected[field] = summary.Name
		case "type":
			selected[field] = summary.Type
		case "url":
			selected[field] = summary.URL
		case "url_pending":
			selected[field] = summary.URLPending
		case "state":
			selected[field] = summary.State
		case "funnel_requested":
			selected[field] = summary.FunnelRequested
		case "funnel_active":
			selected[field] = summary.FunnelActive
		case "funnel_state":
			selected[field] = summary.FunnelState
		case "funnel_expires_at":
			selected[field] = summary.FunnelExpiresAt
		case "funnel_remaining":
			selected[field] = summary.FunnelRemaining
		case "error":
			if summary.Error != nil {
				selected[field] = summary.Error
			}
		}
	}
	return selected
}

func loadListResultForPaths(regPath, pidPath, snapshotPath string, opts listOptions) (ListResult, error) {
	if err := validateListOptions(opts); err != nil {
		return ListResult{}, err
	}
	status, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		return ListResult{}, err
	}
	services, err := filterStatusServices(status, opts)
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{SchemaVersion: inspect.SchemaVersion, Count: len(services)}
	if opts.Verbose {
		result.Services = services
		return result, nil
	}
	if len(opts.Fields) > 0 {
		selected := make([]map[string]any, 0, len(services))
		for _, svc := range services {
			selected = append(selected, selectListFields(listSummary(svc), opts.Fields))
		}
		result.Services = selected
		return result, nil
	}
	summaries := make([]ListServiceSummary, 0, len(services))
	for _, svc := range services {
		summaries = append(summaries, listSummary(svc))
	}
	result.Services = summaries
	return result, nil
}

func listServicesWithOptions(regPath string, out io.Writer, opts listOptions) error {
	pidPath, err := listPIDPathFn()
	if err != nil {
		return err
	}
	snapshotPath, err := listRuntimeSnapshotPathFn()
	if err != nil {
		return err
	}
	result, err := loadListResultForPaths(regPath, pidPath, snapshotPath, opts)
	if err != nil {
		return err
	}
	if result.Count == 0 {
		fmt.Fprintln(out, "No services registered.")
		return nil
	}

	status, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		return err
	}
	services, err := filterStatusServices(status, opts)
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTYPE\tBACKEND\tURL\tSTATE\tFUNNEL EXPIRES\tFUNNEL TTL")
	for _, svc := range services {
		summary := listSummary(svc)
		url := "-"
		if summary.URL != nil {
			url = *summary.URL
		}
		remaining := "-"
		if summary.FunnelRemaining != nil {
			remaining = *summary.FunnelRemaining
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", summary.Name, summary.Type, svc.Backend.Display, url, summary.State, funnelExpiresLabel(summary.FunnelExpiresAt, summary.FunnelRemaining), remaining)
	}
	return writer.Flush()
}

func listServices(regPath string, out io.Writer) error {
	return listServicesWithOptions(regPath, out, listOptions{})
}

func init() {
	listCmd := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List registered services",
		Long: `List registered services with token-efficient filtering.

JSON defaults to name, type, exact runtime URL (or null), url_pending, state,
and Funnel intent/active/reason fields. Use --verbose for the complete
owner-only diagnostic view.

Examples:
  tslink list --json
  tslink list --name myapp --fields name,url --json
  tslink list --type proxy --verbose --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			name, _ := cmd.Flags().GetString("name")
			serviceType, _ := cmd.Flags().GetString("type")
			fieldsRaw, _ := cmd.Flags().GetString("fields")
			verbose, _ := cmd.Flags().GetBool("verbose")
			opts := listOptions{Name: name, Type: serviceType, Fields: parseListFields(fieldsRaw), Verbose: verbose}
			if err := validateListOptions(opts); err != nil {
				return err
			}
			if jsonOutput(cmd) {
				pidPath, err := listPIDPathFn()
				if err != nil {
					return err
				}
				snapshotPath, err := listRuntimeSnapshotPathFn()
				if err != nil {
					return err
				}
				result, err := loadListResultForPaths(regPath, pidPath, snapshotPath, opts)
				if err != nil {
					return err
				}
				output.Success("list", result)
				return nil
			}
			return listServicesWithOptions(regPath, cmd.OutOrStdout(), opts)
		},
	}
	listCmd.Flags().String("name", "", "Return only the exact service name")
	listCmd.Flags().String("type", "", "Filter by service type: proxy, file, or tcp")
	listCmd.Flags().String("fields", "", "Comma-separated slim fields: name,type,url,url_pending,state,funnel_requested,funnel_active,funnel_state,funnel_expires_at,funnel_remaining,error")
	listCmd.Flags().Bool("verbose", false, "Return the complete owner-only diagnostic service view")
	rootCmd.AddCommand(listCmd)
}
