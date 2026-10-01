package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const accessIdentityFailureModeDenyWhenUnresolved = "deny_when_identity_unresolved"

// accessExplainJSONData carries an AccessExplainResult into the result
// envelope's data slot. Custom marshaling keeps the wire data flat, so the
// envelope publishes the AccessExplainResult fields directly.
type accessExplainJSONData struct {
	AccessExplain AccessExplainResult
}

func (d accessExplainJSONData) MarshalJSON() ([]byte, error) { return json.Marshal(d.AccessExplain) }

type AccessExplainResult struct {
	SchemaVersion          int                                `json:"schema_version"`
	Service                string                             `json:"service"`
	Summary                string                             `json:"summary"`
	TSLinkKnown            AccessExplainKnown                 `json:"tslink_known"`
	TSLinkLocalEnforcement AccessExplainLocalEnforcement      `json:"tslink_local_enforcement"`
	ExternalPolicyUnknown  AccessExplainExternalPolicyUnknown `json:"external_policy_unknown"`
	BackendAuthAssumption  AccessExplainBackendAuthAssumption `json:"backend_auth_assumption"`
}

type AccessExplainKnown struct {
	ServiceType                  string                            `json:"service_type"`
	Endpoint                     inspect.EndpointView              `json:"endpoint"`
	Exposure                     inspect.ExposureView              `json:"exposure"`
	Tags                         inspect.SummaryView               `json:"tags"`
	Allow                        inspect.SummaryView               `json:"allow"`
	Backend                      inspect.BackendView               `json:"backend"`
	Warnings                     []inspect.WarningView             `json:"warnings,omitempty"`
	TargetLoopbackClassification AccessExplainTargetClassification `json:"target_loopback_classification"`
}

type AccessExplainTargetClassification struct {
	AppliesTo      string `json:"applies_to"`
	Meaningful     bool   `json:"meaningful"`
	Classification string `json:"classification"`
	Host           string `json:"host,omitempty"`
	Port           string `json:"port,omitempty"`
	Summary        string `json:"summary"`
}

type AccessExplainLocalEnforcement struct {
	Kind           string                      `json:"kind"`
	Applies        bool                        `json:"applies"`
	Summary        string                      `json:"summary"`
	AllowList      inspect.SummaryView         `json:"allow_list"`
	FailureMode    string                      `json:"failure_mode,omitempty"`
	PublicExposure AccessExplainPublicExposure `json:"public_exposure"`
	Notes          []string                    `json:"notes"`
}

type AccessExplainPublicExposure struct {
	Public  bool   `json:"public"`
	Summary string `json:"summary"`
}

type AccessExplainExternalPolicyUnknown struct {
	Known         bool     `json:"known"`
	Summary       string   `json:"summary"`
	UnknownLayers []string `json:"unknown_layers"`
}

type AccessExplainBackendAuthAssumption struct {
	Proven           bool     `json:"proven"`
	Summary          string   `json:"summary"`
	OutsideTSLink    bool     `json:"outside_tslink"`
	OutOfScopeLayers []string `json:"out_of_scope_layers"`
}

type accessTarget struct {
	Host            string
	Port            string
	LoopbackOrLocal bool
}

// accessExplainResultForPath reads one registry and explains one service.
// `tslink access explain` and the MCP access_explain tool share it, so the
// two surfaces cannot disagree about what TSLink claims to know.
func accessExplainResultForPath(regPath, serviceName string) (AccessExplainResult, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return AccessExplainResult{}, err
	}
	for _, svc := range reg.Services {
		if svc.Name != serviceName {
			continue
		}
		return buildAccessExplainResult(svc), nil
	}
	return AccessExplainResult{}, output.ErrNotFound(fmt.Sprintf("service not found: %s", serviceName))
}

func runAccessExplain(serviceName string, out io.Writer, isJSON bool) error {
	regPath, err := registryPathFn()
	if err != nil {
		return err
	}
	result, err := accessExplainResultForPath(regPath, serviceName)
	if err != nil {
		return err
	}
	if isJSON {
		output.WriteJSON(out, output.NewSuccess("access explain", accessExplainJSONData{AccessExplain: result}))
		return nil
	}
	formatAccessExplain(result, out)
	return nil
}

func buildAccessExplainResult(svc registry.Service) AccessExplainResult {
	view := inspect.ServiceViewFor(svc)
	view.Backend = accessSafeBackendView(view.Backend)

	known := AccessExplainKnown{
		ServiceType:                  view.Type,
		Endpoint:                     view.Endpoint,
		Exposure:                     view.Exposure,
		Tags:                         view.Tags,
		Allow:                        view.Allow,
		Backend:                      view.Backend,
		Warnings:                     append([]inspect.WarningView(nil), view.Warnings...),
		TargetLoopbackClassification: accessTargetClassificationFor(svc),
	}
	enforcement := accessLocalEnforcementFor(svc, view)
	externalUnknown := accessExternalPolicyUnknown(view)
	backendAuth := accessBackendAuthAssumption()

	result := AccessExplainResult{
		SchemaVersion:          inspect.SchemaVersion,
		Service:                svc.Name,
		TSLinkKnown:            known,
		TSLinkLocalEnforcement: enforcement,
		ExternalPolicyUnknown:  externalUnknown,
		BackendAuthAssumption:  backendAuth,
	}
	result.Summary = accessExplainSummary(result)
	return result
}

func accessLocalEnforcementFor(svc registry.Service, view inspect.ServiceView) AccessExplainLocalEnforcement {
	publicExposure := accessPublicExposureFor(view)
	allow := view.Allow
	var enforcement AccessExplainLocalEnforcement
	switch svc.Type {
	case registry.TypeTCP:
		enforcement = AccessExplainLocalEnforcement{
			Kind:           "tcp_no_http_enforcement",
			Applies:        false,
			Summary:        "Raw TCP services do not receive TSLink HTTP identity or allow-list enforcement; TSLink provides a raw private route only.",
			AllowList:      allow,
			PublicExposure: publicExposure,
			Notes: []string{
				"TSLink cannot evaluate or enforce HTTP principals on raw TCP streams.",
				"Tailscale or Headscale policy and backend authentication still determine real access.",
			},
		}
	case registry.TypeProxy, registry.TypeFile:
		if len(svc.AllowedUsers) > 0 {
			enforcement = AccessExplainLocalEnforcement{
				Kind:           "http_allow_list",
				Applies:        true,
				Summary:        fmt.Sprintf("TSLink HTTP allow-list enforcement applies to %d redacted principal(s).", len(svc.AllowedUsers)),
				AllowList:      allow,
				FailureMode:    accessIdentityFailureModeDenyWhenUnresolved,
				PublicExposure: publicExposure,
				Notes: []string{
					"Allowed principals are intentionally redacted.",
					"Requests are denied when Tailscale identity cannot be resolved.",
				},
			}
		} else {
			enforcement = AccessExplainLocalEnforcement{
				Kind:           "no_local_allow_list",
				Applies:        false,
				Summary:        "No TSLink local user allow-list is configured for this HTTP service.",
				AllowList:      allow,
				PublicExposure: publicExposure,
				Notes: []string{
					"Tailnet policy, sharing, tag ownership, Funnel policy, and backend authentication still matter.",
				},
			}
		}
	default:
		enforcement = AccessExplainLocalEnforcement{
			Kind:           "unknown_service_type",
			Applies:        false,
			Summary:        "TSLink local enforcement cannot be classified for this unknown service type.",
			AllowList:      allow,
			PublicExposure: publicExposure,
			Notes: []string{
				"Review the registry entry with a TSLink version that understands this service type.",
			},
		}
	}
	return accessAddContextNotes(enforcement, svc, view)
}

func accessAddContextNotes(enforcement AccessExplainLocalEnforcement, svc registry.Service, view inspect.ServiceView) AccessExplainLocalEnforcement {
	if svc.Type == registry.TypeTCP && svc.Funnel {
		enforcement.Notes = append(enforcement.Notes, "Tailscale Funnel does not carry raw TCP; a hand-edited TCP Funnel registry mark is not proof of public reachability.")
	}
	return enforcement
}

func accessPublicExposureFor(view inspect.ServiceView) AccessExplainPublicExposure {
	if view.Exposure.Public {
		return AccessExplainPublicExposure{
			Public:  true,
			Summary: "Public exposure is configured via Tailscale Funnel; TSLink makes no private tailnet-only claim for this service.",
		}
	}
	return AccessExplainPublicExposure{
		Public:  false,
		Summary: "TSLink registry does not mark this service as public, but this command does not evaluate remote tailnet policy.",
	}
}

func accessExternalPolicyUnknown(view inspect.ServiceView) AccessExplainExternalPolicyUnknown {
	unknown := AccessExplainExternalPolicyUnknown{
		Known:   false,
		Summary: "This command reads the local TSLink registry only; it does not evaluate live Tailscale or Headscale policy.",
		UnknownLayers: []string{
			"Tailscale/Headscale membership",
			"Tailscale ACL/grants",
			"device sharing",
			"tag ownership",
			"Funnel policy",
		},
	}
	return unknown
}

func accessBackendAuthAssumption() AccessExplainBackendAuthAssumption {
	return AccessExplainBackendAuthAssumption{
		Proven:        false,
		Summary:       "Backend application, database, and SSH authentication are outside TSLink and are not proven by this command.",
		OutsideTSLink: true,
		OutOfScopeLayers: []string{
			"backend application authentication",
			"database authentication",
			"SSH authentication",
		},
	}
}

func accessExplainSummary(result AccessExplainResult) string {
	if result.TSLinkLocalEnforcement.PublicExposure.Public {
		return fmt.Sprintf(
			"TSLink knows service %q as a %s service with public Funnel exposure; external policy and backend authentication are not evaluated.",
			result.Service,
			result.TSLinkKnown.ServiceType,
		)
	}
	switch result.TSLinkLocalEnforcement.Kind {
	case "http_allow_list":
		return fmt.Sprintf(
			"TSLink knows service %q as a %s service with local HTTP allow-list enforcement for %d redacted principal(s); external policy and backend authentication are not evaluated.",
			result.Service,
			result.TSLinkKnown.ServiceType,
			result.TSLinkLocalEnforcement.AllowList.Count,
		)
	case "tcp_no_http_enforcement":
		return fmt.Sprintf(
			"TSLink knows service %q as a raw TCP service; TSLink HTTP identity and allow-list enforcement do not apply, and external policy plus backend authentication are not evaluated.",
			result.Service,
		)
	case "no_local_allow_list":
		return fmt.Sprintf(
			"TSLink knows service %q as a %s service with no local TSLink user allow-list; external policy and backend authentication are not evaluated.",
			result.Service,
			result.TSLinkKnown.ServiceType,
		)
	default:
		return fmt.Sprintf(
			"TSLink knows service %q locally, but this command does not evaluate external policy or backend authentication.",
			result.Service,
		)
	}
}

func accessTargetClassificationFor(svc registry.Service) AccessExplainTargetClassification {
	switch svc.Type {
	case registry.TypeProxy:
		target, err := accessClassifyProxyTarget(svc.Target)
		return accessNetworkTargetClassification("network_target", target, err)
	case registry.TypeTCP:
		target, err := accessClassifyHostPortTarget(svc.Target, "")
		return accessNetworkTargetClassification("network_target", target, err)
	case registry.TypeFile:
		return AccessExplainTargetClassification{
			AppliesTo:      "filesystem_path",
			Meaningful:     false,
			Classification: "not_applicable",
			Summary:        "File services expose a local filesystem path; network loopback classification is not applicable.",
		}
	default:
		return AccessExplainTargetClassification{
			AppliesTo:      "unknown",
			Meaningful:     false,
			Classification: "unsupported_service_type",
			Summary:        "Target loopback classification is unavailable for this service type.",
		}
	}
}

func accessNetworkTargetClassification(appliesTo string, target accessTarget, err error) AccessExplainTargetClassification {
	if err != nil {
		return AccessExplainTargetClassification{
			AppliesTo:      appliesTo,
			Meaningful:     true,
			Classification: "unknown",
			Summary:        "Target host and port could not be parsed for loopback classification.",
		}
	}
	classification := "non_loopback"
	summary := "Target host is not loopback/local."
	if target.LoopbackOrLocal {
		classification = "loopback_or_local"
		summary = "Target host is loopback/local."
	}
	return AccessExplainTargetClassification{
		AppliesTo:      appliesTo,
		Meaningful:     true,
		Classification: classification,
		Host:           target.Host,
		Port:           target.Port,
		Summary:        summary,
	}
}

func accessClassifyProxyTarget(raw string) (accessTarget, error) {
	if raw == "" {
		return accessTarget{}, fmt.Errorf("empty proxy target")
	}
	if !strings.Contains(raw, "://") {
		return accessClassifyHostPortTarget(accessRedactSchemelessTargetDisplay(raw), "80")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return accessTarget{}, err
	}
	if parsed.Host == "" {
		return accessTarget{}, fmt.Errorf("proxy target host is empty")
	}
	defaultPort := "80"
	if strings.EqualFold(parsed.Scheme, "https") {
		defaultPort = "443"
	}
	return accessClassifyHostPortTarget(parsed.Host, defaultPort)
}

func accessClassifyHostPortTarget(authority, defaultPort string) (accessTarget, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if defaultPort == "" {
			return accessTarget{}, err
		}
		if accessAuthorityHasExplicitPort(authority) {
			return accessTarget{}, err
		}
		host = strings.Trim(authority, "[]")
		port = defaultPort
	}
	if port == "" {
		return accessTarget{}, fmt.Errorf("missing target port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort <= 0 || parsedPort > 65535 {
		return accessTarget{}, fmt.Errorf("invalid target port %q", port)
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	return accessTarget{
		Host:            host,
		Port:            port,
		LoopbackOrLocal: accessIsLoopbackOrLocalHost(host),
	}, nil
}

func accessAuthorityHasExplicitPort(authority string) bool {
	if strings.HasPrefix(authority, "[") {
		return strings.Contains(authority, "]:")
	}
	return strings.Count(authority, ":") == 1
}

func accessIsLoopbackOrLocalHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func accessSafeBackendView(view inspect.BackendView) inspect.BackendView {
	view.Display = inspect.SanitizeBackendDisplay(view.Display)
	return view
}

func accessRedactSchemelessTargetDisplay(raw string) string {
	display := raw
	if cut := strings.IndexAny(display, "?#"); cut >= 0 {
		display = display[:cut]
	}

	prefix := ""
	rest := display
	if schemeIndex := strings.Index(rest, "://"); schemeIndex >= 0 {
		prefix = rest[:schemeIndex+len("://")]
		rest = rest[schemeIndex+len("://"):]
	}
	return prefix + accessRedactSchemelessUserinfo(rest)
}

func accessRedactSchemelessUserinfo(raw string) string {
	if raw == "" || strings.HasPrefix(raw, "/") {
		return raw
	}
	authorityEnd := strings.Index(raw, "/")
	if authorityEnd < 0 {
		authorityEnd = len(raw)
	}
	authority := raw[:authorityEnd]
	if userinfoEnd := strings.LastIndex(authority, "@"); userinfoEnd >= 0 {
		return raw[userinfoEnd+1:]
	}
	return raw
}

func formatAccessExplain(result AccessExplainResult, out io.Writer) {
	known := result.TSLinkKnown
	enforcement := result.TSLinkLocalEnforcement

	fmt.Fprintf(out, "TSLink access explain: %s\n", result.Service)
	fmt.Fprintf(out, "Summary: %s\n\n", result.Summary)

	fmt.Fprintln(out, "TSLink knows:")
	fmt.Fprintf(out, "  Type: %s\n", known.ServiceType)
	fmt.Fprintf(out, "  Endpoint: %s (%s)\n", emptyDash(known.Endpoint.Display), emptyDash(known.Endpoint.Kind))
	fmt.Fprintf(out, "  Exposure: %s, public=%t\n", emptyDash(known.Exposure.Kind), known.Exposure.Public)
	fmt.Fprintf(out, "  Tags: %s\n", summaryLabel(known.Tags))
	fmt.Fprintf(out, "  Allow: %s\n", summaryLabel(known.Allow))
	fmt.Fprintf(out, "  Backend: %s (%s)\n", emptyDash(known.Backend.Display), emptyDash(known.Backend.Kind))
	fmt.Fprintf(out, "  Target classification: %s - %s\n", known.TargetLoopbackClassification.Classification, known.TargetLoopbackClassification.Summary)
	if len(known.Warnings) > 0 {
		fmt.Fprintln(out, "  Warnings:")
		for _, warning := range known.Warnings {
			fmt.Fprintf(out, "    %s: %s\n", warning.Code, warning.Message)
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "TSLink local enforcement:")
	fmt.Fprintf(out, "  %s\n", enforcement.Summary)
	fmt.Fprintf(out, "  Allow-list summary: %s\n", summaryLabel(enforcement.AllowList))
	if enforcement.FailureMode != "" {
		fmt.Fprintf(out, "  Failure mode: %s\n", enforcement.FailureMode)
	}
	fmt.Fprintf(out, "  Public exposure: %t - %s\n", enforcement.PublicExposure.Public, enforcement.PublicExposure.Summary)
	for _, note := range enforcement.Notes {
		fmt.Fprintf(out, "  Note: %s\n", note)
	}

	fmt.Fprintln(out, "\nExternal policy unknown:")
	fmt.Fprintf(out, "  known: %t\n", result.ExternalPolicyUnknown.Known)
	fmt.Fprintf(out, "  %s\n", result.ExternalPolicyUnknown.Summary)
	fmt.Fprintf(out, "  Unknown layers: %s\n", strings.Join(result.ExternalPolicyUnknown.UnknownLayers, ", "))

	fmt.Fprintln(out, "\nBackend auth assumption:")
	fmt.Fprintf(out, "  proven: %t\n", result.BackendAuthAssumption.Proven)
	fmt.Fprintf(out, "  %s\n", result.BackendAuthAssumption.Summary)
	fmt.Fprintf(out, "  Out-of-scope layers: %s\n", strings.Join(result.BackendAuthAssumption.OutOfScopeLayers, ", "))
}

var accessCmd = &cobra.Command{
	Use:   "access",
	Short: "Explain local access knowledge for registered services",
	Long: `Explain what TSLink can and cannot know about registered service access.

These commands read the local TSLink registry. They do not evaluate live
Tailscale or Headscale membership, ACL/grants, sharing, tag ownership, Funnel
policy, or backend application authentication.`,
	Args: cobra.NoArgs,
	RunE: runCommandGroup,
}

var accessExplainCmd = &cobra.Command{
	Use:   "explain <service>",
	Short: "Explain local access knowledge for one service",
	Long: `Explain what TSLink knows locally about one registered service and what
remains outside this command.

The explanation is registry-local. It does not prove live Tailscale or
Headscale policy, sharing, tag ownership, Funnel reachability, or backend
application/database/SSH authentication.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAccessExplain(args[0], cmd.OutOrStdout(), jsonOutput(cmd))
	},
}

func init() {
	accessCmd.AddCommand(accessExplainCmd)
	rootCmd.AddCommand(accessCmd)
}
