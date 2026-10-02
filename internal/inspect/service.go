package inspect

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"

	"github.com/anydoor7/tslink/internal/registry"
)

const (
	// SchemaVersion is the version of the public data views (list, status
	// --urls, doctor, access explain, templates, the service view). It is an
	// integer like the envelope's, registry.json's and runtime.json's own
	// schema_version; runtime.json keeps its own number.
	SchemaVersion = 1

	EndpointKindHTTPS       = "https"
	EndpointKindPublicHTTPS = "public_https"
	EndpointKindTCP         = "tcp"
	EndpointKindUnknown     = "unknown"

	EndpointStateExact    = "exact"
	EndpointStateExpected = "expected"

	ExposureTailnet      = "tailnet"
	ExposureTailnetAllow = "tailnet_allow"
	ExposurePublicFunnel = "public_funnel"
	ExposureGuestFunnel  = "guest_funnel"
	ExposureUnknown      = "unknown"
)

type EndpointView struct {
	Kind    string `json:"kind"`
	Display string `json:"display"`
	State   string `json:"state"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type ExposureView struct {
	Kind    string `json:"kind"`
	Display string `json:"display"`
	Public  bool   `json:"public"`
}

type SummaryView struct {
	Mode     string   `json:"mode,omitempty"`
	Count    int      `json:"count"`
	Entries  []string `json:"entries,omitempty"`
	Redacted bool     `json:"redacted,omitempty"`
}

type BackendView struct {
	Kind    string `json:"kind"`
	Display string `json:"display,omitempty"`
}

type WarningView struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

type ServiceView struct {
	RequestLimits *registry.EffectiveRequestLimits `json:"request_limits,omitempty"`
	SchemaVersion int                              `json:"schema_version"`
	Name          string                           `json:"name"`
	Type          string                           `json:"type"`
	Endpoint      EndpointView                     `json:"endpoint"`
	Exposure      ExposureView                     `json:"exposure"`
	Tags          SummaryView                      `json:"tags"`
	Allow         SummaryView                      `json:"allow"`
	Backend       BackendView                      `json:"backend"`
	PreserveHost  bool                             `json:"preserve_host"`
	Funnel        bool                             `json:"funnel,omitempty"`
	Warnings      []WarningView                    `json:"warnings,omitempty"`
}

func ServiceViews(services []registry.Service) []ServiceView {
	views := make([]ServiceView, 0, len(services))
	for _, svc := range services {
		views = append(views, ServiceViewFor(svc))
	}
	return views
}

func ServiceViewFor(svc registry.Service) ServiceView {
	view := ServiceView{
		RequestLimits: svc.EffectiveRequestLimits(),
		SchemaVersion: SchemaVersion,
		Name:          svc.Name,
		Type:          svc.Type,
		Endpoint:      endpointFor(svc),
		Exposure:      exposureFor(svc),
		Tags:          entriesSummary("configured", svc.Tags),
		Allow:         allowSummary(svc),
		Backend:       backendFor(svc),
		Funnel:        svc.Funnel,
		PreserveHost:  svc.PreserveHost,
	}
	view.Warnings = warningsFor(svc)
	return view
}

func endpointFor(svc registry.Service) EndpointView {
	switch svc.Type {
	case registry.TypeTCP:
		port := tcpPort(svc)
		host := fmt.Sprintf("%s.<tailnet>.ts.net", svc.Name)
		display := host
		if port > 0 {
			display = net.JoinHostPort(host, strconv.Itoa(port))
		}
		return EndpointView{
			Kind:    EndpointKindTCP,
			Display: display,
			State:   EndpointStateExpected,
			Host:    host,
			Port:    port,
		}
	case registry.TypeProxy, registry.TypeFile:
		kind := EndpointKindHTTPS
		if svc.Funnel {
			kind = EndpointKindPublicHTTPS
		}
		return EndpointView{
			Kind:  kind,
			State: EndpointStateExpected,
		}
	default:
		return EndpointView{
			Kind:  EndpointKindUnknown,
			State: EndpointStateExpected,
		}
	}
}

func exposureFor(svc registry.Service) ExposureView {
	if svc.GuestGate && svc.Funnel {
		return ExposureView{Kind: ExposureGuestFunnel, Display: "Funnel with mandatory guest authentication", Public: true}
	}
	if svc.Funnel {
		return ExposureView{
			Kind:    ExposurePublicFunnel,
			Display: "public via Tailscale Funnel",
			Public:  true,
		}
	}
	switch svc.Type {
	case registry.TypeProxy, registry.TypeFile:
		if len(svc.AllowedUsers) > 0 {
			return ExposureView{
				Kind:    ExposureTailnetAllow,
				Display: "tailnet with TSLink allow list",
				Public:  false,
			}
		}
		return ExposureView{
			Kind:    ExposureTailnet,
			Display: "tailnet",
			Public:  false,
		}
	case registry.TypeTCP:
		return ExposureView{
			Kind:    ExposureTailnet,
			Display: "tailnet",
			Public:  false,
		}
	default:
		return ExposureView{
			Kind:    ExposureUnknown,
			Display: "unknown",
			Public:  false,
		}
	}
}

func allowSummary(svc registry.Service) SummaryView {
	if len(svc.AllowedUsers) > 0 {
		return redactedEntriesSummary("restricted", svc.AllowedUsers)
	}
	if svc.Funnel {
		return SummaryView{Mode: "public", Count: 0}
	}
	if svc.Type == registry.TypeTCP {
		return SummaryView{Mode: "not_applicable", Count: 0}
	}
	return SummaryView{Mode: "all_tailnet", Count: 0}
}

func backendFor(svc registry.Service) BackendView {
	switch svc.Type {
	case registry.TypeProxy:
		return BackendView{Kind: "http_target", Display: SanitizeBackendDisplay(svc.Target)}
	case registry.TypeFile:
		if svc.File != "" {
			// A single-file share serves that one file, not its directory.
			return BackendView{Kind: "file", Display: filepath.Join(svc.Path, svc.File)}
		}
		return BackendView{Kind: "directory", Display: svc.Path}
	case registry.TypeTCP:
		return BackendView{Kind: "tcp_target", Display: SanitizeBackendDisplay(svc.Target)}
	default:
		return BackendView{Kind: "unknown"}
	}
}

func warningsFor(svc registry.Service) []WarningView {
	var warnings []WarningView
	if svc.Type == registry.TypeTCP {
		if len(svc.AllowedUsers) > 0 {
			warnings = append(warnings, warningView(
				WarningCodeTCPAllowedUsersInvalid,
				"Raw TCP services cannot enforce allowed_users; remove the allow list or convert the service to HTTP.",
			))
		}
		warnings = append(warnings, warningView(
			WarningCodeTCPHTTPACLNotApplicable,
			"Raw TCP is routed privately; TSLink HTTP identity and allow filtering do not apply.",
		))
	}
	if svc.Type != registry.TypeProxy && svc.Type != registry.TypeFile && svc.Type != registry.TypeTCP {
		warnings = append(warnings, warningView(
			WarningCodeServiceTypeUnknown,
			fmt.Sprintf("Unknown service type %q.", svc.Type),
		))
	}
	if svc.FunnelExpiryUndecided() {
		warnings = append(warnings, warningView(
			WarningCodeFunnelExpiryRequired,
			"Funnel is requested but funnel_expires_at is missing; set an RFC 3339 deadline or \"never\" in registry.json. Until then the daemon does not start this service.",
		))
	}
	return warnings
}

func warningView(code, message string) WarningView {
	meta := WarningCodeRegistry[code]
	return WarningView{
		Code:     code,
		Severity: meta.Severity,
		Message:  message,
		Source:   meta.Source,
	}
}

func entriesSummary(mode string, entries []string) SummaryView {
	copied := append([]string(nil), entries...)
	return SummaryView{
		Mode:    mode,
		Count:   len(copied),
		Entries: copied,
	}
}

func redactedEntriesSummary(mode string, entries []string) SummaryView {
	return SummaryView{
		Mode:     mode,
		Count:    len(entries),
		Redacted: true,
	}
}

func tcpPort(svc registry.Service) int {
	if svc.Port > 0 {
		return svc.Port
	}
	_, portStr, err := net.SplitHostPort(svc.Target)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}
