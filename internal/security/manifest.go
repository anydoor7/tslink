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
	OptInFlag     string   `json:"opt_in_flag,omitempty"`
	DisableFlag   string   `json:"disable_flag,omitempty"`
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
			"disabled by default; --manage-acl is required for ordinary tagOwners mutations",
			"Raw HuJSON surgical AST patch preserves comments, formatting, and unknown keys byte-for-byte outside the requested edit",
			"lossless preservation is covered by internal/tailapi TestEnsureFunnelAttr_LosslessHuJSONFusedPatch",
			"uses the policy ETag returned by PolicyFile.Raw for If-Match",
		},
	}
}

// FunnelAutoProvisionPlan describes the one default-on remote mutation used to
// make an acknowledged Funnel service autonomous. Unlike ordinary ACL tag
// management, this operation has a daemon/service off switch instead of an
// opt-in flag.
func FunnelAutoProvisionPlan(target string, owners []string, enabled bool) RemoteSideEffectPlan {
	resources := []string{target}
	resources = append(resources, owners...)
	defaultState := "enabled"
	if !enabled {
		defaultState = "disabled_by_kill_switch"
	}
	return RemoteSideEffectPlan{
		SchemaVersion: 1,
		ID:            "remote.acl.funnel_auto_provision",
		Operation:     "ensure_funnel_tag_owner_and_node_attr",
		RemoteSystem:  "tailscale_policy_file",
		Mutates:       enabled,
		Default:       defaultState,
		DisableFlag:   "--no-auto-provision",
		Resources:     resources,
		Boundaries: []string{
			"default-on only for services that recorded public_ack; the shared Funnel tag is derived at node construction and need not be persisted",
			"tailnet settings httpsEnabled is read before any ACL write; disabled HTTPS returns the networking_settings remedy without a policy POST",
			"one Raw HuJSON read and at most one ETag-guarded Set per reconcile transaction",
			"tag owner identities are derived from existing service tags held by the OAuth client",
			"nodeAttrs grants are created or modified only for an exact single target; broader existing Funnel coverage is read-only",
			"lossless preservation is covered by internal/tailapi TestEnsureFunnelAttr_LosslessHuJSONFusedPatch",
			"daemon and service kill switches are covered by cmd TestServeCmd_WiresFunnelProvisioningScopeAndKillSwitch and internal/server TestEnsureFunnelPolicyBeforeRestart_DaemonKillSwitchWins",
		},
	}
}

// InviteMutationPlan describes one explicit outward-facing invite operation.
// Invite URLs and recipient PII are intentionally excluded from resources.
// The typed inputs make it impossible for callers to pass either accidentally.
func InviteMutationPlan(kind, operation, inviteID, service string, deviceID int64) RemoteSideEffectPlan {
	remoteSystem := "tailscale_user_invites"
	if kind == "device" {
		remoteSystem = "tailscale_device_invites"
	}
	resources := make([]string, 0, 3)
	if inviteID != "" {
		resources = append(resources, inviteID)
	}
	if service != "" {
		resources = append(resources, service)
	}
	if deviceID != 0 {
		resources = append(resources, fmt.Sprintf("device:%d", deviceID))
	}
	return RemoteSideEffectPlan{
		SchemaVersion: 1,
		ID:            fmt.Sprintf("remote.invite.%s.%s", kind, operation),
		Operation:     operation + "_" + kind + "_invite",
		RemoteSystem:  remoteSystem,
		Mutates:       true,
		Default:       "explicit_command",
		Resources:     append([]string(nil), resources...),
		Boundaries: []string{
			"requires a stored user-owned tskey-api- access token; OAuth client secrets are rejected before any request",
			"creation names a recipient in the command, but recipient PII is excluded from remote_side_effect_plan resources; --print-link omits email from the API body and returns the API-provided URL for self-delivery",
			"invite URLs are returned verbatim and are never constructed or written to logs",
			"device invite mutations require an exact stable nodeId recorded by the running TSLink service; hostname matching alone is not ownership proof",
		},
	}
}
