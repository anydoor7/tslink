package security

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed capabilities.v1.json
var capabilityManifestJSON []byte

type CapabilityManifest struct {
	SchemaVersion int          `json:"schema_version"`
	Product       string       `json:"product"`
	Capabilities  []Capability `json:"capabilities"`
}

type Capability struct {
	ID                   string `json:"id"`
	ServiceType          string `json:"service_type"`
	TailnetTransport     string `json:"tailnet_transport"`
	TSLinkTLSTermination string `json:"tslink_tls_termination"`
	BackendHop           string `json:"backend_hop"`
	WhoIsPropagation     string `json:"whois_propagation"`
	WhoIsCache           string `json:"whois_cache"`
	AllowAuthorization   string `json:"allow_authorization"`
	PublicExposure       string `json:"public_exposure"`
	LogSink              string `json:"log_sink"`
	LogSchema            string `json:"log_schema"`
	HostIsolation        string `json:"host_isolation"`
	ComplianceStatus     string `json:"compliance_status"`
}

type RemoteSideEffectPlan struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	Operation     string   `json:"operation"`
	RemoteSystem  string   `json:"remote_system"`
	Mutates       bool     `json:"mutates"`
	Default       string   `json:"default"`
	OptInFlag     string   `json:"opt_in_flag"`
	Resources     []string `json:"resources"`
	Boundaries    []string `json:"boundaries"`
}

func CapabilityManifestJSON() []byte {
	return append([]byte(nil), capabilityManifestJSON...)
}

func LoadCapabilityManifest() (CapabilityManifest, error) {
	var manifest CapabilityManifest
	if err := json.Unmarshal(capabilityManifestJSON, &manifest); err != nil {
		return CapabilityManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return CapabilityManifest{}, err
	}
	return manifest, nil
}

func (m CapabilityManifest) Validate() error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported capability manifest schema_version %d", m.SchemaVersion)
	}
	if m.Product != "tslink" {
		return fmt.Errorf("unexpected product %q", m.Product)
	}
	if len(m.Capabilities) == 0 {
		return fmt.Errorf("capability manifest has no capabilities")
	}
	seen := make(map[string]struct{}, len(m.Capabilities))
	for _, cap := range m.Capabilities {
		if cap.ID == "" {
			return fmt.Errorf("capability with empty id")
		}
		if _, ok := seen[cap.ID]; ok {
			return fmt.Errorf("duplicate capability id %q", cap.ID)
		}
		seen[cap.ID] = struct{}{}
		if cap.ServiceType == "" || cap.TailnetTransport == "" || cap.TSLinkTLSTermination == "" ||
			cap.BackendHop == "" || cap.WhoIsPropagation == "" || cap.WhoIsCache == "" ||
			cap.AllowAuthorization == "" || cap.PublicExposure == "" || cap.LogSink == "" ||
			cap.LogSchema == "" || cap.HostIsolation == "" || cap.ComplianceStatus == "" {
			return fmt.Errorf("capability %q has empty security fields", cap.ID)
		}
	}
	return nil
}

func ACLMutationPlan(operation string, resources []string, enabled bool) RemoteSideEffectPlan {
	defaultState := "disabled"
	if enabled {
		defaultState = "explicitly_enabled"
	}
	return RemoteSideEffectPlan{
		SchemaVersion: 1,
		ID:            "remote.acl.mutation",
		Operation:     operation,
		RemoteSystem:  "tailscale_policy_file",
		Mutates:       enabled,
		Default:       defaultState,
		OptInFlag:     "--manage-acl",
		Resources:     append([]string(nil), resources...),
		Boundaries: []string{
			"typed whole-policy rewrite path",
			"uses policy ETag returned by the Tailscale API client",
			"disabled by default because lossless HuJSON preservation is not locally proven",
		},
	}
}
