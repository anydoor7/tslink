package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var (
	registryPathFn            = config.RegistryPath
	listPIDPathFn             = config.PIDPath
	listRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	listLoadRegistryFn        = registry.Load
	listTailnetDevicesFn      = tailapi.ListTSLinkDevices
)

type listOptions struct {
	Name    string
	Type    string
	Fields  []string
	Verbose bool
	Tailnet bool
}

const listStatePending = "pending"

// Origins reported by `tslink list --tailnet`. The registry is per-machine
// while the tailnet is shared, so every row must say which side it came from.
const (
	// listOriginLocalRegistry means this machine's registry.json holds a
	// service with exactly this hostname.
	listOriginLocalRegistry = "local_registry"
	// listOriginLocalNameVariant means the hostname is a <service>-N tsnet
	// collision variant of a locally registered service. That is the usual
	// shape of an orphan left behind by this machine.
	listOriginLocalNameVariant = "local_name_variant"
	// listOriginUnregistered means this machine's registry knows nothing about
	// the hostname: it is another machine's TSLink service, or an orphan.
	listOriginUnregistered = "unregistered"
)

// listTailnetCleanupAuthority is emitted on every --tailnet result, in JSON and
// in human output, because the view exposes a real limitation. Node ownership
// proof is a local file (~/.config/tslink/node-ownership.json) and
// internal/tailapi authorizes DELETE only on an exact NodeID recorded there, so
// `tslink cleanup` on this machine can never delete a device another machine
// created - including an orphan.
const listTailnetCleanupAuthority = "tslink cleanup deletes only devices whose exact NodeID is recorded in this machine's local node-ownership.json, so a device this machine's registry does not name must be cleaned up from the machine that created it"

// listTailnetCredentialMessage is the failure text when no API credential is
// stored. The recovery steps come from credentials.NextAPIKeyBootstrap so this
// command uses the same guidance as every other credential-dependent path.
const listTailnetCredentialMessage = "listing tailnet devices requires a stored Tailscale API credential (a user-owned tskey-api- access token or an OAuth client secret)"

// ListServiceSummary is the token-efficient default service representation.
// URL is null until runtime.json contains exact evidence for the current daemon
// and registry fingerprint.
type ListServiceSummary struct {
	Health          health.State            `json:"health"`
	NodeKey         health.Expiry           `json:"node_key"`
	Name            string                  `json:"name"`
	Type            string                  `json:"type"`
	URL             *string                 `json:"url"`
	URLPending      bool                    `json:"url_pending"`
	State           string                  `json:"state"`
	PreserveHost    bool                    `json:"preserve_host"`
	FunnelRequested bool                    `json:"funnel_requested"`
	FunnelActive    bool                    `json:"funnel_active"`
	FunnelState     string                  `json:"funnel_state"`
	FunnelExpiresAt *time.Time              `json:"funnel_expires_at,omitempty"`
	FunnelRemaining *string                 `json:"funnel_remaining,omitempty"`
	Error           *tsruntime.ServiceError `json:"error,omitempty"`
}

// ListResult holds the result for JSON output.
type ListResult struct {
	SchemaVersion int `json:"schema_version"`
	Services      any `json:"services"`
	Count         int `json:"count"`
}

// TailnetDeviceView is one row of `tslink list --tailnet`. It carries no node
// identity value: tailapi.TailnetDevice already drops NodeID, matching the rest
// of TSLink's output surface where NodeIDs are never emitted.
type TailnetDeviceView struct {
	Hostname           string     `json:"hostname"`
	Name               string     `json:"name,omitempty"`
	LocallyRegistered  bool       `json:"locally_registered"`
	Origin             string     `json:"origin"`
	LocalService       string     `json:"local_service,omitempty"`
	Tags               []string   `json:"tags"`
	OS                 string     `json:"os,omitempty"`
	CreatedAt          *time.Time `json:"created_at,omitempty"`
	LastSeenAt         *time.Time `json:"last_seen_at,omitempty"`
	ConnectedToControl bool       `json:"connected_to_control"`
	Authorized         bool       `json:"authorized"`
}

// TailnetListResult is the `tslink list --tailnet` payload. It is a separate
// type from ListResult on purpose: the default list reports this machine's
// registered services, this one reports tailnet devices, and collapsing them
// would make "count" mean two different things.
type TailnetListResult struct {
	SchemaVersion     int                 `json:"schema_version"`
	Devices           []TailnetDeviceView `json:"devices"`
	Count             int                 `json:"count"`
	RegisteredCount   int                 `json:"registered_count"`
	UnregisteredCount int                 `json:"unregistered_count"`
	CleanupAuthority  string              `json:"cleanup_authority"`
}

func validateListOptions(opts listOptions) error {
	if opts.Tailnet {
		for _, conflict := range []struct {
			set  bool
			flag string
		}{
			{opts.Name != "", "--name"},
			{opts.Type != "", "--type"},
			{len(opts.Fields) > 0, "--fields"},
			{opts.Verbose, "--verbose"},
		} {
			if conflict.set {
				return output.ErrUsage(fmt.Sprintf("--tailnet conflicts with %s; %s filters this machine's registered services, while --tailnet reports tailnet devices", conflict.flag, conflict.flag))
			}
		}
		return nil
	}
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
		"preserve_host": true,
		"name":          true, "type": true, "url": true, "url_pending": true, "state": true,
		"funnel_requested": true, "funnel_active": true, "funnel_state": true,
		"funnel_expires_at": true, "funnel_remaining": true, "error": true,
	}
	for _, field := range opts.Fields {
		if !allowed[field] {
			return output.ErrUsage(fmt.Sprintf("unknown --fields value %q; supported: name,type,url,url_pending,state,preserve_host,funnel_requested,funnel_active,funnel_state,funnel_expires_at,funnel_remaining,error", field))
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
		Health: svc.Health, NodeKey: svc.NodeKey,
		Name:            svc.Name,
		Type:            svc.Type,
		URLPending:      true,
		State:           listStatePending,
		PreserveHost:    svc.PreserveHost,
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
		case "preserve_host":
			selected[field] = summary.PreserveHost
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
	return commandStatus.loadListResultForPaths(regPath, pidPath, snapshotPath, opts)
}

func (s statusRead) loadListResultForPaths(regPath, pidPath, snapshotPath string, opts listOptions) (ListResult, error) {
	if err := validateListOptions(opts); err != nil {
		return ListResult{}, err
	}
	status, err := s.getStatusURLs(pidPath, regPath, snapshotPath)
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

// listTailnetCredentialError reports the missing-credential case as an auth
// failure (exit 3) that still carries the shared bootstrap guidance, the same
// way invite creation does. It follows the commandGroupUsageError pattern in
// root.go: Unwrap supplies the numeric exit, StableCode supplies the envelope
// discriminator, and NextCommands supplies the recovery list that both the JSON
// envelope's error.next and the human "Next:" lines render.
type listTailnetCredentialError struct{}

func (listTailnetCredentialError) Error() string { return listTailnetCredentialMessage }

func (e listTailnetCredentialError) Unwrap() error { return output.ErrAuth(e.Error()) }

func (listTailnetCredentialError) StableCode() string {
	return output.StableErrorCode(output.ExitAuth)
}

func (listTailnetCredentialError) NextCommands() []string {
	return append(credentials.NextAPIKeyBootstrap(), "printf %s \"$SECRET\" | tslink login --client-secret-stdin   # an OAuth client secret also authorizes this read-only device list")
}

// classifyTailnetDeviceOrigin decides which side of the machine boundary a
// tailnet hostname sits on. Exact registration wins; a <service>-N tsnet
// collision variant of a local service is reported as its own origin so it is
// never silently counted as registered here.
func classifyTailnetDeviceOrigin(hostname string, services []registry.Service) (origin, localService string) {
	for _, svc := range services {
		if svc.Name == hostname {
			return listOriginLocalRegistry, svc.Name
		}
	}
	for _, svc := range services {
		if tailapi.HostnameMatchesServiceName(hostname, svc.Name) {
			return listOriginLocalNameVariant, svc.Name
		}
	}
	return listOriginUnregistered, ""
}

func loadTailnetListResult(ctx context.Context, regPath string) (TailnetListResult, error) {
	devices, err := listTailnetDevicesFn(ctx)
	if err != nil {
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			return TailnetListResult{}, listTailnetCredentialError{}
		}
		return TailnetListResult{}, err
	}
	// An absent registry.json loads as an empty registry, which is the correct
	// reading here: this machine registers nothing, so every tailnet device is
	// unregistered from its point of view.
	reg, err := listLoadRegistryFn(regPath)
	if err != nil {
		return TailnetListResult{}, err
	}
	var services []registry.Service
	if reg != nil {
		services = reg.Services
	}

	result := TailnetListResult{
		SchemaVersion:    inspect.SchemaVersion,
		Devices:          make([]TailnetDeviceView, 0, len(devices)),
		CleanupAuthority: listTailnetCleanupAuthority,
	}
	for _, device := range devices {
		origin, localService := classifyTailnetDeviceOrigin(device.Hostname, services)
		view := TailnetDeviceView{
			Hostname:           device.Hostname,
			Name:               device.Name,
			LocallyRegistered:  origin == listOriginLocalRegistry,
			Origin:             origin,
			LocalService:       localService,
			Tags:               device.Tags,
			OS:                 device.OS,
			CreatedAt:          device.CreatedAt,
			LastSeenAt:         device.LastSeenAt,
			ConnectedToControl: device.ConnectedToControl,
			Authorized:         device.Authorized,
		}
		if view.Tags == nil {
			view.Tags = []string{}
		}
		if view.LocallyRegistered {
			result.RegisteredCount++
		} else {
			result.UnregisteredCount++
		}
		result.Devices = append(result.Devices, view)
	}
	result.Count = len(result.Devices)
	return result, nil
}

func formatTailnetTimestamp(moment *time.Time) string {
	if moment == nil {
		return "-"
	}
	return moment.UTC().Format(time.RFC3339)
}

func tailnetLocalColumn(view TailnetDeviceView) string {
	switch view.Origin {
	case listOriginLocalRegistry:
		return "yes"
	case listOriginLocalNameVariant:
		return "variant"
	default:
		return "no"
	}
}

func writeTailnetDevices(out io.Writer, result TailnetListResult) error {
	if result.Count == 0 {
		fmt.Fprintln(out, "No TSLink-owned devices found in the tailnet.")
		return nil
	}
	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "HOSTNAME\tLOCAL\tLOCAL SERVICE\tTAGS\tOS\tLAST SEEN\tCREATED")
	for _, view := range result.Devices {
		tags := strings.Join(view.Tags, ",")
		if tags == "" {
			tags = "-"
		}
		lastSeen := formatTailnetTimestamp(view.LastSeenAt)
		if view.ConnectedToControl && view.LastSeenAt == nil {
			lastSeen = "connected"
		}
		localService := view.LocalService
		if localService == "" {
			localService = "-"
		}
		osName := view.OS
		if osName == "" {
			osName = "-"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			view.Hostname, tailnetLocalColumn(view), localService, tags, osName, lastSeen, formatTailnetTimestamp(view.CreatedAt))
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d of %d TSLink-owned tailnet devices are not registered on this machine.\n", result.UnregisteredCount, result.Count)
	fmt.Fprintf(out, "%s.\n", listTailnetCleanupAuthority)
	return nil
}

func listTailnetDevices(ctx context.Context, regPath string, out io.Writer, isJSON bool) error {
	result, err := loadTailnetListResult(ctx, regPath)
	if err != nil {
		return err
	}
	if isJSON {
		output.Success("list", result)
		return nil
	}
	return writeTailnetDevices(out, result)
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
	if err := writer.Flush(); err != nil {
		return err
	}
	if opts.Verbose {
		for _, svc := range services {
			formatAppHealth(out, svc.Name, svc.Health, svc.NodeKey)
		}
	}
	return nil
}

func listServices(regPath string, out io.Writer) error {
	return listServicesWithOptions(regPath, out, listOptions{})
}

func init() {
	listCmd := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List registered services",
		Long: `List registered services with a compact default projection.

JSON defaults to name, type, exact runtime URL (or null), url_pending, state,
and Funnel intent/active/reason fields. Use --verbose for the complete
owner-only diagnostic view.

--tailnet switches to a different question. The default list reads this
machine's registry.json; --tailnet reads the shared tailnet and reports every
TSLink-tagged device in it, including services registered on other machines and
orphan nodes. It is read-only, requires a stored Tailscale API credential, and
marks each row with whether this machine's registry knows the hostname. Device
deletion still requires exact NodeID ownership proof recorded locally, so
unregistered rows can only be cleaned up from the machine that created them.

Examples:
  tslink list --json
  tslink list --name myapp --fields name,url --json
  tslink list --type proxy --verbose --json
  tslink list --tailnet --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			name, _ := cmd.Flags().GetString("name")
			serviceType, _ := cmd.Flags().GetString("type")
			fieldsRaw, _ := cmd.Flags().GetString("fields")
			verbose, _ := cmd.Flags().GetBool("verbose")
			tailnet, _ := cmd.Flags().GetBool("tailnet")
			opts := listOptions{Name: name, Type: serviceType, Fields: parseListFields(fieldsRaw), Verbose: verbose, Tailnet: tailnet}
			if err := validateListOptions(opts); err != nil {
				return err
			}
			if opts.Tailnet {
				return listTailnetDevices(cmd.Context(), regPath, cmd.OutOrStdout(), jsonOutput(cmd))
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
	listCmd.Flags().String("fields", "", "Comma-separated slim fields: name,type,url,url_pending,state,preserve_host,funnel_requested,funnel_active,funnel_state,funnel_expires_at,funnel_remaining,error")
	listCmd.Flags().Bool("verbose", false, "Return the complete owner-only diagnostic service view")
	listCmd.Flags().Bool("tailnet", false, "Read-only: list every TSLink-tagged device in the tailnet, including other machines' services and orphans, instead of this machine's registered services")
	rootCmd.AddCommand(listCmd)
}
