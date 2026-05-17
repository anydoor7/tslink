package inspect

import (
	"fmt"
	"net"
	"strconv"

	"github.com/monody0007/tslink/internal/registry"
)

const (
	SchemaVersion = "vnext.1"

	EndpointKindHTTPS       = "https"
	EndpointKindPublicHTTPS = "public_https"
	EndpointKindTCP         = "tcp"
	EndpointKindUnknown     = "unknown"

	EndpointStateExact    = "exact"
	EndpointStateExpected = "expected"

	ExposureTailnet      = "tailnet"
	ExposureTailnetAllow = "tailnet_allow"
	ExposurePublicFunnel = "public_funnel"
	ExposureCustomDomain = "custom_domain"
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

type MiddlewareView struct {
	HTTPAuth         bool    `json:"http_auth,omitempty"`
	RateLimit        float64 `json:"rate_limit,omitempty"`
	IPAllowListCount int     `json:"ip_allow_list_count,omitempty"`
	CORSOriginsCount int     `json:"cors_origins_count,omitempty"`
}

type WarningView struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

type ServiceView struct {
	SchemaVersion string          `json:"schema_version"`
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	Endpoint      EndpointView    `json:"endpoint"`
	Exposure      ExposureView    `json:"exposure"`
	Tags          SummaryView     `json:"tags"`
	Allow         SummaryView     `json:"allow"`
	Backend       BackendView     `json:"backend"`
	Funnel        bool            `json:"funnel,omitempty"`
	Middleware    *MiddlewareView `json:"middleware,omitempty"`
	Warnings      []WarningView   `json:"warnings,omitempty"`
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
		SchemaVersion: SchemaVersion,
		Name:          svc.Name,
		Type:          svc.Type,
		Endpoint:      endpointFor(svc),
		Exposure:      exposureFor(svc),
		Tags:          entriesSummary("configured", svc.Tags),
		Allow:         allowSummary(svc),
		Backend:       backendFor(svc),
		Funnel:        svc.Funnel,
		Middleware:    middlewareFor(svc.Middleware),
	}
	view.Warnings = warningsFor(svc, view.Middleware)
	return view
}

func endpointFor(svc registry.Service) EndpointView {
	host := tailnetHost(svc.Name)

	switch svc.Type {
	case registry.TypeTCP:
		port := tcpPort(svc)
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
		if svc.Domain != "" {
			return EndpointView{
				Kind:    kind,
				Display: "https://" + svc.Domain,
				State:   EndpointStateExpected,
				Host:    svc.Domain,
			}
		}
		return EndpointView{
			Kind:    kind,
			Display: "https://" + host,
			State:   EndpointStateExpected,
			Host:    host,
		}
	default:
		return EndpointView{
			Kind:    EndpointKindUnknown,
			Display: host,
			State:   EndpointStateExpected,
			Host:    host,
		}
	}
}

func exposureFor(svc registry.Service) ExposureView {
	if svc.Funnel {
		return ExposureView{
			Kind:    ExposurePublicFunnel,
			Display: "public via Tailscale Funnel",
			Public:  true,
		}
	}
	if svc.Domain != "" {
		return ExposureView{
			Kind:    ExposureCustomDomain,
			Display: "custom domain",
			Public:  false,
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
		return BackendView{Kind: "http_target", Display: svc.Target}
	case registry.TypeFile:
		return BackendView{Kind: "directory", Display: svc.Path}
	case registry.TypeTCP:
		return BackendView{Kind: "tcp_target", Display: svc.Target}
	default:
		return BackendView{Kind: "unknown"}
	}
}

func middlewareFor(mw *registry.MiddlewareConfig) *MiddlewareView {
	if mw == nil {
		return nil
	}
	view := &MiddlewareView{
		HTTPAuth:         mw.BasicAuth != "",
		RateLimit:        mw.RateLimit,
		IPAllowListCount: len(mw.IPAllowList),
		CORSOriginsCount: len(mw.CORSOrigins),
	}
	if !view.HTTPAuth && view.RateLimit == 0 && view.IPAllowListCount == 0 && view.CORSOriginsCount == 0 {
		return nil
	}
	return view
}

func warningsFor(svc registry.Service, mw *MiddlewareView) []WarningView {
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
	if mw != nil && mw.HTTPAuth {
		warnings = append(warnings, warningView(
			WarningCodeHTTPAuthConfigured,
			"HTTP authentication is configured; credentials are redacted.",
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

func tailnetHost(name string) string {
	return fmt.Sprintf("%s.<tailnet>.ts.net", name)
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
