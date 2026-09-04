package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

const (
	mcpProtocolVersion = "2025-11-25"
	mcpMaxRecordBytes  = 1024 * 1024
)

var mcpSupportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

type mcpToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// nestedObjectSchema describes a payload branch whose internal shape is the
// CLI's own JSON contract (documented in docs/cli-manifest.json) rather than a
// second copy maintained here. It stays an open object on purpose: closing it
// would mean restating a large struct in two places, and the copy would be the
// one that rots.
func nestedObjectSchema(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}

var (
	mcpEndpointViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
		"state":   map[string]any{"type": "string"},
		"host":    map[string]any{"type": "string"},
		"port":    map[string]any{"type": "integer"},
	}, "kind", "display", "state")
	mcpExposureViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
		"public":  map[string]any{"type": "boolean"},
	}, "kind", "display", "public")
	mcpSummaryViewSchema = objectSchema(map[string]any{
		"mode":     map[string]any{"type": "string"},
		"count":    map[string]any{"type": "integer", "minimum": 0},
		"entries":  stringArraySchema(),
		"redacted": map[string]any{"type": "boolean"},
	}, "count")
	mcpBackendViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
	}, "kind")
	mcpWarningViewSchema = objectSchema(map[string]any{
		"code":     map[string]any{"type": "string"},
		"severity": map[string]any{"type": "string"},
		"message":  map[string]any{"type": "string"},
		"source":   map[string]any{"type": "string"},
	}, "code", "severity", "message", "source")
	mcpWarningArraySchema = map[string]any{"type": "array", "items": mcpWarningViewSchema}
	mcpServiceViewSchema  = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"name":           map[string]any{"type": "string"},
		"type":           map[string]any{"type": "string", "enum": serviceTypeValues()},
		"endpoint":       mcpEndpointViewSchema,
		"exposure":       mcpExposureViewSchema,
		"tags":           mcpSummaryViewSchema,
		"allow":          mcpSummaryViewSchema,
		"backend":        mcpBackendViewSchema,
		"funnel":         map[string]any{"type": "boolean"},
		"middleware":     nestedObjectSchema("Reserved middleware view; the middleware runtime is unavailable and rejected at admission."),
		"warnings":       mcpWarningArraySchema,
	}, "schema_version", "name", "type", "endpoint", "exposure", "tags", "allow", "backend")
	mcpRemoteSideEffectPlanSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "integer"},
		"id":             map[string]any{"type": "string"},
		"operation":      map[string]any{"type": "string"},
		"remote_system":  map[string]any{"type": "string"},
		"mutates":        map[string]any{"type": "boolean"},
		"default":        map[string]any{"type": "string"},
		"opt_in_flag":    map[string]any{"type": "string"},
		"disable_flag":   map[string]any{"type": "string"},
		"resources":      stringArraySchema(),
		"boundaries":     stringArraySchema(),
	}, "schema_version", "id", "operation", "remote_system", "mutates", "default", "resources", "boundaries")
	mcpInviteProperties = map[string]any{
		"kind":               map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                 map[string]any{"type": "string"},
		"recipient":          map[string]any{"type": "string"},
		"email":              map[string]any{"type": "string"},
		"emailed":            map[string]any{"type": "boolean"},
		"invite_url":         map[string]any{"type": "string", "description": "Bearer invitation URL. Present only when the invite was created with print_link true or when invite_list was called with show_urls true; anyone holding it can accept the invitation."},
		"role":               map[string]any{"type": "string"},
		"service":            map[string]any{"type": "string"},
		"device_id":          map[string]any{"type": "integer"},
		"multi_use":          map[string]any{"type": "boolean"},
		"allow_exit_node":    map[string]any{"type": "boolean"},
		"accepted":           map[string]any{"type": "boolean"},
		"created":            map[string]any{"type": "string"},
		"last_email_sent_at": map[string]any{"type": "string"},
	}
	mcpInviteSchema       = objectSchema(mcpInviteProperties, "kind", "id", "emailed")
	mcpInviteArraySchema  = map[string]any{"type": "array", "items": mcpInviteSchema}
	mcpServiceErrorSchema = objectSchema(map[string]any{
		"code":    map[string]any{"type": "string"},
		"message": map[string]any{"type": "string"},
		"next":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"provision": objectSchema(map[string]any{
			"attempted":     map[string]any{"type": "boolean"},
			"target":        map[string]any{"type": "string"},
			"changed":       map[string]any{"type": "boolean"},
			"reason":        map[string]any{"type": "string"},
			"write_outcome": map[string]any{"type": "string", "enum": []string{tailapi.PolicyWriteNotAttempted, tailapi.PolicyWriteUnchanged, tailapi.PolicyWriteChanged, tailapi.PolicyWriteRejected, tailapi.PolicyWriteUnknown}},
		}, "attempted", "changed", "reason"),
	}, "code", "message")
	mcpShareOutputSchema = objectSchema(map[string]any{
		"url":      map[string]any{"type": "string"},
		"name":     map[string]any{"type": "string"},
		"status":   map[string]any{"type": "string", "enum": []string{shareStatusReady, authStatusNeedsLogin}},
		"auth_url": map[string]any{"type": "string"},
	}, "status")
	mcpListOutputSchema = objectSchema(map[string]any{
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":             map[string]any{"type": "string"},
				"type":             map[string]any{"type": "string", "enum": serviceTypeValues()},
				"url":              map[string]any{"type": []string{"string", "null"}},
				"url_pending":      map[string]any{"type": "boolean"},
				"state":            map[string]any{"type": "string", "enum": listStateValues()},
				"funnel_requested": map[string]any{"type": "boolean"},
				"funnel_active":    map[string]any{"type": "boolean"},
				"funnel_state":     map[string]any{"type": "string", "enum": funnelStateValues()},
				// Present only for a service with a Funnel deadline. They were
				// missing while no MCP tool could create one; share and add now
				// can, so a strict client would have started rejecting list.
				"funnel_expires_at": map[string]any{"type": "string"},
				"funnel_remaining":  map[string]any{"type": "string"},
				"error":             mcpServiceErrorSchema,
			}, "name", "type", "url", "url_pending", "state", "funnel_requested", "funnel_active", "funnel_state"),
		},
	}, "services")
	mcpUnshareOutputSchema = objectSchema(map[string]any{
		"ok":                     map[string]any{"type": "boolean", "description": "Whether the idempotent unshare request removed the local registry entry or found the service absent (removed false); ok true does not guarantee tailnet device cleanup, so check device_cleaned and device_warning."},
		"name":                   map[string]any{"type": "string"},
		"removed":                map[string]any{"type": "boolean"},
		"device_cleaned":         map[string]any{"type": "boolean"},
		"device_cleanup_skipped": map[string]any{"type": "boolean"},
		"device_skip_reason":     map[string]any{"type": "string"},
		"device_warning":         map[string]any{"type": "string"},
	}, "ok", "name", "removed", "device_cleaned", "device_cleanup_skipped")
	mcpStatusOutputSchema = objectSchema(map[string]any{
		"authenticated":            map[string]any{"type": "boolean", "description": "Legacy alias for node_authorized; it is not a stored-credential indicator."},
		"credential_stored":        map[string]any{"type": "boolean"},
		"node_authorized":          map[string]any{"type": "boolean"},
		"authorized_service_count": map[string]any{"type": "integer", "minimum": 0},
		"daemon_running":           map[string]any{"type": "boolean"},
		"service_count":            map[string]any{"type": "integer", "minimum": 0},
		"status":                   map[string]any{"type": "string"},
		"auth_url":                 map[string]any{"type": "string"},
		"next":                     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "authenticated", "credential_stored", "node_authorized", "authorized_service_count", "daemon_running", "service_count")
	mcpAddOutputSchema = objectSchema(map[string]any{
		"name":              map[string]any{"type": "string"},
		"type":              map[string]any{"type": "string", "enum": serviceTypeValues()},
		"created":           map[string]any{"type": "boolean", "description": "True when this call created the registry entry; false when it replaced an existing entry with the same name."},
		"funnel_expires_at": map[string]any{"type": "string"},
		"funnel_rearmed":    map[string]any{"type": "boolean"},
		"url":               map[string]any{"type": []string{"string", "null"}},
		"url_pending":       map[string]any{"type": "boolean"},
		"endpoint":          mcpEndpointViewSchema,
		"exposure":          mcpExposureViewSchema,
		"warnings":          mcpWarningArraySchema,
	}, "name", "type", "created", "funnel_rearmed", "url", "url_pending", "endpoint", "exposure")
	mcpURLOutputSchema = objectSchema(map[string]any{
		"name":  map[string]any{"type": "string"},
		"url":   map[string]any{"type": "string"},
		"state": map[string]any{"type": "string"},
	}, "name", "url", "state")
	mcpTagsListOutputSchema = objectSchema(map[string]any{
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name": map[string]any{"type": "string"},
				"tags": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}},
			}, "name", "tags"),
		},
	}, "services")
	mcpTagsSetOutputSchema = objectSchema(map[string]any{
		"service": map[string]any{"type": "string"},
		"tags":    stringArraySchema(),
	}, "service", "tags")
	mcpAccessExplainOutputSchema = objectSchema(map[string]any{
		"schema_version":           map[string]any{"type": "string"},
		"service":                  map[string]any{"type": "string"},
		"summary":                  map[string]any{"type": "string"},
		"tslink_known":             nestedObjectSchema("What the local registry records: service type, endpoint, exposure, tags, redacted allow-list, backend, and target loopback classification."),
		"tslink_local_enforcement": nestedObjectSchema("Which TSLink HTTP enforcement applies, its failure mode, and whether public Funnel exposure is configured."),
		"external_policy_unknown":  nestedObjectSchema("Layers this command does not evaluate: tailnet membership, ACL/grants, sharing, tag ownership, Funnel policy."),
		"backend_auth_assumption":  nestedObjectSchema("Backend application, database and SSH authentication are outside TSLink and are not proven here."),
	}, "schema_version", "service", "summary", "tslink_known", "tslink_local_enforcement", "external_policy_unknown", "backend_auth_assumption")
	mcpDoctorOutputSchema = objectSchema(map[string]any{
		"schema_version":   map[string]any{"type": "string"},
		"execution_status": map[string]any{"type": "string"},
		"status":           map[string]any{"type": "string"},
		"health_status":    map[string]any{"type": "string"},
		"health_exit_code": map[string]any{"type": "integer", "description": "Exit code `tslink doctor` would return for this health status. It is reported, not applied: the tool result itself is successful."},
		"counts":           nestedObjectSchema("Service count plus finding counts by severity."),
		"paths":            nestedObjectSchema("Resolved config, registry, runtime snapshot, auth handoff and PID paths."),
		"credential_mode":  map[string]any{"type": "string"},
		"credential_tier":  map[string]any{"type": "string"},
		"daemon":           nestedObjectSchema("Whether the local TSLink daemon is running, and its PID when it is."),
		"runtime_snapshot": nestedObjectSchema("Freshness classification of runtime.json against the current daemon and registry fingerprint."),
		"tailscale_ssh":    nestedObjectSchema("Tailscale SSH enablement for this node, read from the local Tailscale client: state is enabled, disabled, or unknown, and acl_rule_required records that a tailnet ACL ssh rule is also needed. Informational only; it never changes health_status or health_exit_code."),
		"findings": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"code":     map[string]any{"type": "string"},
				"severity": map[string]any{"type": "string"},
				"area":     map[string]any{"type": "string"},
				"message":  map[string]any{"type": "string"},
				"service":  map[string]any{"type": "string"},
				"evidence": nestedObjectSchema("Redacted string evidence for this finding; credential material and URLs are replaced before it is emitted."),
			}, "code", "severity", "area", "message"),
		},
	}, "schema_version", "execution_status", "status", "health_status", "health_exit_code", "counts", "paths", "credential_mode", "credential_tier", "daemon", "runtime_snapshot", "tailscale_ssh", "findings")
	mcpInviteCreateOutputSchema = objectSchema(mergeSchemaProperties(mcpInviteProperties, map[string]any{
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}), "kind", "id", "emailed", "remote_side_effect_plan")
	mcpInviteListOutputSchema = objectSchema(map[string]any{
		"complete":       map[string]any{"type": "boolean", "description": "False when at least one TSLink-owned device could not be checked; device_targets carries the per-service reason."},
		"user_invites":   mcpInviteArraySchema,
		"device_invites": mcpInviteArraySchema,
		"device_targets": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"service":      map[string]any{"type": "string"},
				"checked":      map[string]any{"type": "boolean"},
				"invite_count": map[string]any{"type": "integer", "minimum": 0},
				"error": objectSchema(map[string]any{
					"code":    map[string]any{"type": "string"},
					"message": map[string]any{"type": "string"},
					"next":    stringArraySchema(),
				}, "code", "message"),
			}, "service", "checked", "invite_count"),
		},
		"count": map[string]any{"type": "integer", "minimum": 0},
	}, "complete", "user_invites", "device_invites", "device_targets", "count")
	mcpInviteRevokeOutputSchema = objectSchema(map[string]any{
		"kind":                    map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                      map[string]any{"type": "string"},
		"service":                 map[string]any{"type": "string"},
		"revoked":                 map[string]any{"type": "boolean"},
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}, "kind", "id", "revoked", "remote_side_effect_plan")
	mcpInviteResendOutputSchema = objectSchema(map[string]any{
		"kind":                    map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                      map[string]any{"type": "string"},
		"recipient":               map[string]any{"type": "string"},
		"email":                   map[string]any{"type": "string"},
		"emailed":                 map[string]any{"type": "boolean"},
		"service":                 map[string]any{"type": "string"},
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}, "kind", "id", "email", "emailed", "remote_side_effect_plan")
	mcpTemplateListOutputSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"templates": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":          map[string]any{"type": "string"},
				"summary":       map[string]any{"type": "string"},
				"service_count": map[string]any{"type": "integer", "minimum": 0},
			}, "name", "summary", "service_count"),
		},
		"count": map[string]any{"type": "integer", "minimum": 0},
	}, "schema_version", "templates", "count")
	mcpTemplateApplyOutputSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"name":           map[string]any{"type": "string"},
		"summary":        map[string]any{"type": "string"},
		"dry_run":        map[string]any{"type": "boolean"},
		"applied":        map[string]any{"type": "boolean", "description": "True only when the registry was written; template_plan always reports false."},
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":    map[string]any{"type": "string"},
				"action":  map[string]any{"type": "string", "enum": []string{templateActionCreate, templateActionCreated, templateActionSkipExisting}},
				"message": map[string]any{"type": "string"},
				"service": mcpServiceViewSchema,
			}, "name", "action", "service"),
		},
		"created": map[string]any{"type": "integer", "minimum": 0},
		"skipped": map[string]any{"type": "integer", "minimum": 0},
	}, "schema_version", "name", "summary", "dry_run", "applied", "services", "created", "skipped")
)

// mergeSchemaProperties copies base and overlays extra, so a schema built from
// an embedded struct's properties cannot mutate the map the embedded schema
// itself uses.
func mergeSchemaProperties(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for name, schema := range base {
		merged[name] = schema
	}
	for name, schema := range extra {
		merged[name] = schema
	}
	return merged
}

var mcpToolDefinitions = []mcpToolDefinition{
	{
		Name:        "share",
		Description: "Setting funnel true on this tool publishes the target to the entire public internet, so ask the user before doing that; with funnel false (the default) it exposes a local directory, one file, or an HTTP port only on the user's private Tailscale network. Without allow, every member of the user's tailnet can read the share; pass allow to restrict it to named principals. Use this after creating a local page or report that the user wants to open on another tailnet device. If status is needs_login, open auth_url in a browser and retry after authorization.",
		InputSchema: objectSchema(map[string]any{
			"target":     map[string]any{"type": "string", "minLength": 1, "description": "Existing file or directory path, bare port from 1 to 65535, or host:port HTTP target."},
			"name":       map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Optional requested DNS-label service name. A matching target is reused only if it already has this name; unrelated name collisions receive a numeric suffix."},
			"ephemeral":  map[string]any{"type": "boolean", "default": true, "description": "Keep true for temporary shares; set false only when the user wants durable tailnet node state."},
			"allow":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Principals allowed to reach the share over HTTP: email addresses, or tag:<name> ACL tags. Omitting it leaves the share readable by every member of the user's tailnet. Rejected together with funnel."},
			"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": `^tag:`}, "description": "ACL tags applied to the tailnet node, each prefixed tag:. Defaults to the configured default tag."},
			"funnel":     map[string]any{"type": "boolean", "default": false, "description": "Publish to the public internet through Tailscale Funnel. Requires public_ack true, an HTTP port target, and no allow entries."},
			"public_ack": map[string]any{"type": "boolean", "default": false, "description": "Explicit acknowledgement that funnel exposes the target publicly. funnel true without it is rejected."},
			"funnel_ttl": map[string]any{"type": "string", "enum": []string{"1h", "8h", "24h", "72h", "7d", "never"}, "description": "Public Funnel lifetime; defaults to 24h. Only valid with funnel true."},
		}, "target"),
		OutputSchema: mcpShareOutputSchema,
	},
	{
		Name:        "add",
		Description: "Setting funnel true on this tool publishes the service to the entire public internet, so ask the user before doing that; otherwise it only writes a registry entry for a proxy, file, or TCP service reachable on the user's private Tailscale network. Without allow, every member of the user's tailnet can reach an HTTP service. Use this instead of share when the user wants a named, configured service rather than a one-shot share; it does not start the daemon, so url is usually pending.",
		InputSchema: objectSchema(map[string]any{
			"name":              map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Registry service name (DNS label). An existing entry with this name is replaced."},
			"type":              map[string]any{"type": "string", "enum": serviceTypeValues(), "description": "proxy forwards HTTP to target; file serves the directory dir; tcp forwards a raw stream to target."},
			"target":            map[string]any{"type": "string", "description": "host:port or URL for proxy, host:port for tcp. Rejected for file."},
			"dir":               map[string]any{"type": "string", "description": "Absolute directory path for file. Rejected for proxy and tcp."},
			"allow":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Principals allowed to reach the service over HTTP: email addresses, or tag:<name> ACL tags. Omitting it leaves an HTTP service readable by every member of the user's tailnet. Unsupported for tcp and rejected together with funnel."},
			"tags":              map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": `^tag:`}, "description": "ACL tags applied to the tailnet node, each prefixed tag:. Defaults to the configured default tag."},
			"ephemeral":         map[string]any{"type": "boolean", "default": false, "description": "Register an ephemeral tailnet node that disappears on disconnect."},
			"funnel":            map[string]any{"type": "boolean", "default": false, "description": "Publish to the public internet through Tailscale Funnel. Requires type proxy, public_ack true, no allow entries, and no control_url."},
			"public_ack":        map[string]any{"type": "boolean", "default": false, "description": "Explicit acknowledgement that funnel exposes the service publicly. funnel true without it is rejected."},
			"funnel_ttl":        map[string]any{"type": "string", "enum": []string{"1h", "8h", "24h", "72h", "7d", "never"}, "description": "Public Funnel lifetime; defaults to 24h on a new entry. Omitting it preserves an existing entry's deadline. Only valid with funnel true."},
			"no_auto_provision": map[string]any{"type": "boolean", "default": false, "description": "Disable automatic Funnel policy provisioning. Only valid with funnel true."},
			"control_url":       map[string]any{"type": "string", "description": "Per-service custom control server URL, for example a Headscale deployment. Rejected together with funnel."},
		}, "name", "type"),
		OutputSchema: mcpAddOutputSchema,
	},
	{
		Name:         "list",
		Description:  "List locally registered TSLink services with exact runtime URLs and explicit Funnel requested/active/reason state. Use this to discover current shares or check whether public exposure actually activated.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpListOutputSchema,
	},
	{
		Name:        "unshare",
		Description: "Remove one named service from the local TSLink registry. Use this when the user asks to stop sharing a specific service; it does not expose credentials or open a network listener.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name to remove."},
		}, "name"),
		OutputSchema: mcpUnshareOutputSchema,
	},
	{
		Name:         "status",
		Description:  "Report local TSLink daemon, stored-credential, node-authorization, and service-count state. Use this before retrying a share or when diagnosing why a URL is not ready; a pending needs_login handoff includes auth_url even if the daemon stopped.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpStatusOutputSchema,
	},
	{
		Name:        "url",
		Description: "Return one registered service's exact runtime URL. Use this after add, or after a needs_login share was authorized; it reads local runtime evidence only and never guesses a hostname. It fails with url_not_ready until the daemon has published an exact URL, so pass wait to poll.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
			"wait": map[string]any{"type": "string", "description": "Go duration such as 30s to poll for an exact URL. Omitted or 0s returns immediately."},
		}, "name"),
		OutputSchema: mcpURLOutputSchema,
	},
	{
		Name:         "tags_list",
		Description:  "List every registered service with the ACL tags recorded for it locally. Use this before tags_set to see what a service currently carries; it reads the local registry and does not contact the tailnet.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpTagsListOutputSchema,
	},
	{
		Name:        "tags_set",
		Description: "Replace one service's ACL tags with a single tag in the local registry. This discards the service's other tags and can change who the tailnet policy lets reach it once the daemon restarts; it makes no remote ACL change.",
		InputSchema: objectSchema(map[string]any{
			"service": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
			"tag":     map[string]any{"type": "string", "pattern": `^tag:`, "description": "Single ACL tag, prefixed tag:. It replaces every tag currently on the service."},
		}, "service", "tag"),
		OutputSchema: mcpTagsSetOutputSchema,
	},
	{
		Name:        "access_explain",
		Description: "Explain what TSLink knows locally about one service's access: its exposure, whether an allow-list is enforced, and which layers this answer does not cover. Use this to answer \"who can reach this?\" honestly; it reads the local registry and proves nothing about live tailnet policy or backend authentication.",
		InputSchema: objectSchema(map[string]any{
			"service": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
		}, "service"),
		OutputSchema: mcpAccessExplainOutputSchema,
	},
	{
		Name:        "doctor",
		Description: "Run local TSLink diagnostics and return findings by severity. Use this when a share or URL is not working and status was not enough; it inspects local config, registry, credentials metadata and runtime state, writes nothing, and reports health_exit_code instead of failing.",
		InputSchema: objectSchema(map[string]any{
			"probe_external": map[string]any{"type": "boolean", "default": false, "description": "Also attempt a short TCP connection to non-loopback service targets. Leave false unless the user asked to test reachability of a remote backend."},
		}),
		OutputSchema: mcpDoctorOutputSchema,
	},
	{
		Name:        "invite_user",
		Description: "Sends a real Tailscale invitation to a real email address, so confirm the address and role with the user before calling this. The invitation joins the recipient to the user's tailnet. Set print_link true to receive a bearer invite URL instead of having Tailscale send the email.",
		InputSchema: objectSchema(map[string]any{
			"email":      map[string]any{"type": "string", "description": "Recipient email address."},
			"role":       map[string]any{"type": "string", "enum": tailapi.InviteRoles(), "description": "Role assigned on acceptance. Defaults to member."},
			"print_link": map[string]any{"type": "boolean", "default": false, "description": "Do not send email; return the API-provided invite URL for the user to deliver. Anyone holding that URL can accept."},
		}, "email"),
		OutputSchema: mcpInviteCreateOutputSchema,
	},
	{
		Name:        "invite_device",
		Description: "Sends a real device-sharing invitation to a real email address outside the tailnet, so confirm the service and address with the user before calling this. It shares one TSLink-owned service device with that person. Set print_link true to receive a bearer invite URL instead of having Tailscale send the email.",
		InputSchema: objectSchema(map[string]any{
			"service":         map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name whose device is shared."},
			"email":           map[string]any{"type": "string", "description": "Recipient email address."},
			"print_link":      map[string]any{"type": "boolean", "default": false, "description": "Do not send email; return the API-provided invite URL for the user to deliver. Anyone holding that URL can accept."},
			"multi_use":       map[string]any{"type": "boolean", "default": false, "description": "Allow the invitation to be accepted more than once."},
			"allow_exit_node": map[string]any{"type": "boolean", "default": false, "description": "Allow the recipient to route traffic through the shared device as an exit node."},
		}, "service", "email"),
		OutputSchema: mcpInviteCreateOutputSchema,
	},
	{
		Name:        "invite_list",
		Description: "List open tailnet user invitations and TSLink-owned device invitations. Use this before invite_revoke or invite_resend to find an invite id; it reads the Tailscale API and sends nothing. complete false means at least one device could not be checked.",
		InputSchema: objectSchema(map[string]any{
			"show_urls": map[string]any{"type": "boolean", "default": false, "description": "Include bearer invite URLs. They are redacted by default because anyone holding one can accept the invitation."},
		}),
		OutputSchema: mcpInviteListOutputSchema,
	},
	{
		Name:        "invite_revoke",
		Description: "Cancels a real outstanding invitation on the user's tailnet, so confirm with the user before calling this. A revoked invitation can no longer be accepted and cannot be un-revoked; issue a new one instead. Use invite_list to find the id.",
		InputSchema: objectSchema(map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}, "description": "Invite namespace the id belongs to."},
			"invite_id": map[string]any{"type": "string", "description": "Invite id from invite_list."},
		}, "kind", "invite_id"),
		OutputSchema: mcpInviteRevokeOutputSchema,
	},
	{
		Name:        "invite_resend",
		Description: "Sends another real invitation email to the original recipient, so confirm with the user before calling this. It only works for an invitation Tailscale emailed; a print_link invitation has no recipient to email. Use invite_list to find the id.",
		InputSchema: objectSchema(map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}, "description": "Invite namespace the id belongs to."},
			"invite_id": map[string]any{"type": "string", "description": "Invite id from invite_list."},
		}, "kind", "invite_id"),
		OutputSchema: mcpInviteResendOutputSchema,
	},
	{
		Name:         "template_list",
		Description:  "List the built-in service templates. Use this to offer the user a ready-made private stack; templates only write registry entries and never install or start third-party applications.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpTemplateListOutputSchema,
	},
	{
		Name:        "template_plan",
		Description: "Preview which services a built-in template would create, without writing the registry. Use this before template_apply so the user can see what will be created and what already exists.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "description": "Template name from template_list."},
		}, "name"),
		OutputSchema: mcpTemplateApplyOutputSchema,
	},
	{
		Name:        "template_apply",
		Description: "Write a built-in template's missing services into the local registry. Existing entries with the same names are left untouched, and nothing is started; call template_plan first so the user has seen the plan.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "description": "Template name from template_list."},
		}, "name"),
		OutputSchema: mcpTemplateApplyOutputSchema,
	},
}

// mcpActions is the seam between the JSON-RPC layer and the command
// implementations. Every field forwards to the same internal function the
// matching CLI command calls, so the tool surface cannot acquire behaviour the
// CLI does not have — including its refusals, which stay in the domain layer.
type mcpActions struct {
	share         func(context.Context, shareRequest) (ShareResult, error)
	add           func(context.Context, AddParams, bool) (any, error)
	list          func() (any, error)
	unshare       func(string) (any, error)
	status        func() (any, error)
	url           func(context.Context, string, time.Duration) (any, error)
	tagsList      func() (any, error)
	tagsSet       func(string, string) (any, error)
	accessExplain func(string) (any, error)
	doctor        func(bool) (any, error)
	inviteUser    func(context.Context, string, string, bool) (any, error)
	inviteDevice  func(context.Context, mcpInviteDeviceArguments) (any, error)
	inviteList    func(context.Context, bool) (any, error)
	inviteRevoke  func(context.Context, string, string) (any, error)
	inviteResend  func(context.Context, string, string) (any, error)
	templateList  func() (any, error)
	templatePlan  func(string) (any, error)
	templateApply func(string) (any, error)
}

// mcpAddArguments is the wire shape of the add tool's arguments.
type mcpAddArguments struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Target          string   `json:"target,omitempty"`
	Dir             string   `json:"dir,omitempty"`
	Allow           []string `json:"allow,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Ephemeral       bool     `json:"ephemeral,omitempty"`
	Funnel          bool     `json:"funnel,omitempty"`
	PublicAck       bool     `json:"public_ack,omitempty"`
	FunnelTTL       *string  `json:"funnel_ttl,omitempty"`
	NoAutoProvision bool     `json:"no_auto_provision,omitempty"`
	ControlURL      string   `json:"control_url,omitempty"`
}

// mcpInviteDeviceArguments is the wire shape of the invite_device arguments.
type mcpInviteDeviceArguments struct {
	Service       string `json:"service"`
	Email         string `json:"email"`
	PrintLink     bool   `json:"print_link,omitempty"`
	MultiUse      bool   `json:"multi_use,omitempty"`
	AllowExitNode bool   `json:"allow_exit_node,omitempty"`
}

// addParamsFromMCPArguments maps the tool's type/target/dir triple onto the
// AddParams shape `tslink add` builds from --proxy/--dir/--tcp. It is pure
// argument translation: every admission rule stays in buildService and
// registry.ValidateService, which both surfaces then run identically. The
// second return value is buildService's PreserveFunnelExpiry input: an absent
// funnel_ttl preserves an existing entry's deadline, exactly as an unset
// --funnel-ttl does.
func addParamsFromMCPArguments(args mcpAddArguments) (AddParams, bool, error) {
	params := AddParams{
		Name:            args.Name,
		Ephemeral:       args.Ephemeral,
		Tags:            strings.Join(args.Tags, ","),
		Allow:           strings.Join(args.Allow, ","),
		Funnel:          args.Funnel,
		Public:          args.PublicAck,
		NoAutoProvision: args.NoAutoProvision,
		ControlURL:      args.ControlURL,
	}
	if args.FunnelTTL != nil {
		params.FunnelTTL = *args.FunnelTTL
		params.FunnelTTLSet = true
	}
	switch args.Type {
	case registry.TypeProxy:
		if args.Target == "" {
			return AddParams{}, false, output.ErrUsage("target is required for proxy type")
		}
		if args.Dir != "" {
			return AddParams{}, false, output.ErrUsage("dir is not supported for proxy type")
		}
		params.Proxy = args.Target
	case registry.TypeFile:
		if args.Dir == "" {
			return AddParams{}, false, output.ErrUsage("dir is required for file type")
		}
		if args.Target != "" {
			return AddParams{}, false, output.ErrUsage("target is not supported for file type")
		}
		params.Dir = args.Dir
	case registry.TypeTCP:
		if args.Target == "" {
			return AddParams{}, false, output.ErrUsage("target is required for tcp type")
		}
		if args.Dir != "" {
			return AddParams{}, false, output.ErrUsage("dir is not supported for tcp type")
		}
		params.TCP = args.Target
	default:
		return AddParams{}, false, registry.ServiceTypeAmbiguousError()
	}
	return params, args.FunnelTTL == nil, nil
}

// parseMCPWait reads the url tool's optional Go duration. It mirrors
// `tslink url --wait`, where a non-positive duration means "do not poll".
func parseMCPWait(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	wait, err := time.ParseDuration(raw)
	if err != nil {
		return 0, output.ErrUsage(fmt.Sprintf("invalid wait duration %q: %v", raw, err))
	}
	return wait, nil
}

type mcpServiceSummary = ListServiceSummary

type mcpStatusSummary struct {
	Authenticated          bool     `json:"authenticated"`
	CredentialStored       bool     `json:"credential_stored"`
	NodeAuthorized         bool     `json:"node_authorized"`
	AuthorizedServiceCount int      `json:"authorized_service_count"`
	DaemonRunning          bool     `json:"daemon_running"`
	ServiceCount           int      `json:"service_count"`
	Status                 string   `json:"status,omitempty"`
	AuthURL                string   `json:"auth_url,omitempty"`
	Next                   []string `json:"next,omitempty"`
}

type mcpUnshareSummary struct {
	OK                   bool   `json:"ok"`
	Name                 string `json:"name"`
	Removed              bool   `json:"removed"`
	DeviceCleaned        bool   `json:"device_cleaned"`
	DeviceCleanupSkipped bool   `json:"device_cleanup_skipped"`
	DeviceSkipReason     string `json:"device_skip_reason,omitempty"`
	DeviceWarning        string `json:"device_warning,omitempty"`
}

func defaultMCPActions(paths sharePaths, errOut io.Writer) mcpActions {
	return mcpActions{
		share: func(ctx context.Context, req shareRequest) (ShareResult, error) {
			return executeShare(ctx, paths, req, defaultURLWait, errOut)
		},
		add: func(ctx context.Context, params AddParams, preserveFunnelExpiry bool) (any, error) {
			svc, err := buildService(params)
			if err != nil {
				return nil, err
			}
			svc, err = resolveAddService(svc, params)
			if err != nil {
				return nil, err
			}
			result, _, err := executeAdd(ctx, svc, paths.Registry, paths.PID, paths.Snapshot, preserveFunnelExpiry, 0)
			if err != nil {
				return nil, err
			}
			return result, nil
		},
		list: func() (any, error) {
			result, err := loadListResultForPaths(paths.Registry, paths.PID, paths.Snapshot, listOptions{})
			if err != nil {
				return nil, err
			}
			summaries, ok := result.Services.([]ListServiceSummary)
			if !ok {
				return nil, fmt.Errorf("unexpected list result type %T", result.Services)
			}
			services := make([]mcpServiceSummary, 0, len(summaries))
			for _, service := range summaries {
				services = append(services, mcpServiceSummary(service))
			}
			return map[string]any{"services": services}, nil
		},
		unshare: func(name string) (any, error) {
			if err := registry.ValidateName(name); err != nil {
				return nil, err
			}
			removed, err := removeServiceResult(paths.Registry, paths.Ownership, name)
			if err != nil {
				return nil, err
			}
			return mcpUnshareSummary{
				OK:                   true,
				Name:                 removed.Name,
				Removed:              removed.Removed,
				DeviceCleaned:        removed.DeviceCleaned,
				DeviceCleanupSkipped: removed.DeviceCleanupSkipped,
				DeviceSkipReason:     removed.DeviceSkipReason,
				DeviceWarning:        removed.DeviceWarning,
			}, nil
		},
		status: func() (any, error) {
			status, err := sharePollableStatusFn(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
			if err != nil {
				return nil, err
			}
			result := mcpStatusSummary{
				Authenticated:          status.NodeAuthorized,
				CredentialStored:       status.CredentialStored,
				NodeAuthorized:         status.NodeAuthorized,
				AuthorizedServiceCount: status.AuthorizedServiceCount,
				DaemonRunning:          status.DaemonRunning,
				ServiceCount:           status.ServiceCount,
				Next:                   append([]string(nil), status.Next...),
			}
			if status.AuthStatus == authStatusNeedsLogin && status.AuthURL != "" {
				result.Status = authStatusNeedsLogin
				result.AuthURL = status.AuthURL
			}
			return result, nil
		},
		url: func(ctx context.Context, name string, wait time.Duration) (any, error) {
			return resolveServiceURL(ctx, paths.PID, paths.Registry, paths.Snapshot, name, wait)
		},
		tagsList: func() (any, error) {
			return tagsListResultForPath(paths.Registry)
		},
		tagsSet: func(service, tag string) (any, error) {
			result, err := tagsSetForPath(paths.Registry, service, tag)
			if err != nil {
				return nil, tagsServiceError(err)
			}
			return result, nil
		},
		accessExplain: func(service string) (any, error) {
			return accessExplainResultForPath(paths.Registry, service)
		},
		doctor: func(probeExternal bool) (any, error) {
			return buildDoctorResult(doctorOptions{
				ProbeExternal:       probeExternal,
				RegistryPath:        paths.Registry,
				PIDPath:             paths.PID,
				RuntimeSnapshotPath: paths.Snapshot,
				AuthHandoffPath:     paths.AuthHandoff,
			}), nil
		},
		inviteUser: func(ctx context.Context, email, role string, printLink bool) (any, error) {
			invite, err := inviteUserCreate(ctx, email, role, printLink)
			if err != nil {
				return nil, err
			}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteDevice: func(ctx context.Context, args mcpInviteDeviceArguments) (any, error) {
			invite, err := inviteDeviceCreate(ctx, paths.Registry, paths.PID, paths.Snapshot, args.Service, args.Email, args.PrintLink, args.MultiUse, args.AllowExitNode)
			if err != nil {
				return nil, err
			}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteList: func(ctx context.Context, showURLs bool) (any, error) {
			return inviteListCollect(ctx, paths.Registry, paths.PID, paths.Snapshot, showURLs)
		},
		inviteRevoke: func(ctx context.Context, kind, id string) (any, error) {
			return inviteRevokeExecute(ctx, staticInvitePaths(paths.Registry, paths.PID, paths.Snapshot), kind, id)
		},
		inviteResend: func(ctx context.Context, kind, id string) (any, error) {
			return inviteResendExecute(ctx, staticInvitePaths(paths.Registry, paths.PID, paths.Snapshot), kind, id)
		},
		templateList: func() (any, error) {
			return listTemplatesResult(), nil
		},
		templatePlan: func(name string) (any, error) {
			return applyTemplate(name, paths.Registry, true)
		},
		templateApply: func(name string) (any, error) {
			return applyTemplate(name, paths.Registry, false)
		},
	}
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content           []mcpContent   `json:"content"`
	StructuredContent map[string]any `json:"structuredContent,omitempty"`
	IsError           bool           `json:"isError,omitempty"`
}

type mcpServer struct {
	in             io.Reader
	out            io.Writer
	actions        mcpActions
	initializeSeen bool
	initialized    bool
}

func newMCPServer(in io.Reader, out io.Writer, actions mcpActions) *mcpServer {
	return &mcpServer{in: in, out: out, actions: actions}
}

func validMCPRequestID(id json.RawMessage) bool {
	if len(id) == 0 || bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return false
	}
	var value any
	if err := json.Unmarshal(id, &value); err != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

func (s *mcpServer) write(response mcpResponse) error {
	return json.NewEncoder(s.out).Encode(response)
}

func (s *mcpServer) writeError(id json.RawMessage, code int, message string) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return s.write(mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message}})
}

func (s *mcpServer) serve(ctx context.Context) error {
	reader := bufio.NewReaderSize(s.in, 64*1024)
	record := make([]byte, 0, 64*1024)
	discardingOversize := false
	for {
		fragment, continued, readErr := reader.ReadLine()
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			_ = s.writeError(nil, -32600, "Failed to read JSON-RPC message")
			return readErr
		}
		if !discardingOversize {
			if len(fragment) > mcpMaxRecordBytes-len(record) {
				discardingOversize = true
				record = record[:0]
			} else {
				record = append(record, fragment...)
			}
		}
		if continued {
			continue
		}
		if discardingOversize {
			if err := s.writeError(nil, -32600, "JSON-RPC message exceeds maximum size"); err != nil {
				return err
			}
			discardingOversize = false
			continue
		}

		line := bytes.TrimSpace(record)
		record = record[:0]
		if len(line) == 0 {
			continue
		}
		if err := s.handleLine(ctx, line); err != nil {
			return err
		}
	}
}

func (s *mcpServer) handleLine(ctx context.Context, line []byte) error {
	if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && trimmed[0] == '[' {
		return s.writeError(nil, -32600, "Batch requests are not supported")
	}
	var request mcpRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return s.writeError(nil, -32700, "Parse error")
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		return s.writeError(request.ID, -32600, "Invalid Request")
	}
	if len(request.ID) == 0 {
		s.handleNotification(request)
		return nil
	}
	if !validMCPRequestID(request.ID) {
		return s.writeError(nil, -32600, "Invalid Request")
	}
	return s.handleRequest(ctx, request)
}

func (s *mcpServer) handleNotification(request mcpRequest) {
	if request.Method == "notifications/initialized" && s.initializeSeen {
		s.initialized = true
	}
}

func (s *mcpServer) handleRequest(ctx context.Context, request mcpRequest) error {
	switch request.Method {
	case "initialize":
		return s.initialize(request)
	case "ping":
		return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{}})
	}
	if !s.initialized {
		return s.writeError(request.ID, -32002, "Server not initialized")
	}
	switch request.Method {
	case "tools/list":
		return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: map[string]any{"tools": mcpToolDefinitions}})
	case "tools/call":
		return s.callTool(ctx, request)
	default:
		return s.writeError(request.ID, -32601, "Method not found")
	}
}

func (s *mcpServer) initialize(request mcpRequest) error {
	if s.initializeSeen {
		return s.writeError(request.ID, -32600, "Server already initialized")
	}
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    any    `json:"capabilities,omitempty"`
		ClientInfo      any    `json:"clientInfo,omitempty"`
		Meta            any    `json:"_meta,omitempty"`
	}
	if err := decodeMCPParams(request.Params, &params); err != nil || params.ProtocolVersion == "" {
		return s.writeError(request.ID, -32602, "Invalid initialize parameters")
	}
	version := mcpProtocolVersion
	if mcpSupportedProtocolVersions[params.ProtocolVersion] {
		version = params.ProtocolVersion
	}
	serverVersion := Version
	if serverVersion == "" {
		serverVersion = "dev"
	}
	s.initializeSeen = true
	result := map[string]any{
		"protocolVersion": version,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{
			"name":    "tslink",
			"version": serverVersion,
		},
		"instructions": "Use share to expose a local page to the private tailnet. A needs_login tool result is successful: open auth_url and retry after authorization. Confirm with the user before any tool whose description says it publishes publicly or sends a real invitation: share/add with funnel true, and invite_user, invite_device, invite_revoke, invite_resend.",
	}
	return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func decodeMCPParams(raw json.RawMessage, target any) error {
	return decodeMCPObject(raw, target, false)
}

func decodeMCPArguments(raw json.RawMessage, target any) error {
	return decodeMCPObject(raw, target, true)
}

func decodeMCPObject(raw json.RawMessage, target any, strict bool) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func (s *mcpServer) callTool(ctx context.Context, request mcpRequest) error {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeMCPParams(request.Params, &call); err != nil || call.Name == "" {
		return s.writeError(request.ID, -32602, "Invalid tools/call parameters")
	}
	var data any
	var err error
	switch call.Name {
	case "share":
		var args struct {
			Target    string   `json:"target"`
			Name      string   `json:"name,omitempty"`
			Ephemeral *bool    `json:"ephemeral,omitempty"`
			Allow     []string `json:"allow,omitempty"`
			Tags      []string `json:"tags,omitempty"`
			Funnel    bool     `json:"funnel,omitempty"`
			PublicAck bool     `json:"public_ack,omitempty"`
			FunnelTTL *string  `json:"funnel_ttl,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Target == "" {
			return s.writeError(request.ID, -32602, "Invalid share arguments")
		}
		req := shareRequest{
			Target:    args.Target,
			Name:      args.Name,
			Ephemeral: true,
			Allow:     args.Allow,
			Tags:      args.Tags,
			Funnel:    args.Funnel,
			PublicAck: args.PublicAck,
		}
		if args.Ephemeral != nil {
			req.Ephemeral = *args.Ephemeral
		}
		if args.FunnelTTL != nil {
			req.FunnelTTL = *args.FunnelTTL
			req.FunnelTTLSet = true
		}
		data, err = s.actions.share(ctx, req)
	case "add":
		var args mcpAddArguments
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" || args.Type == "" {
			return s.writeError(request.ID, -32602, "Invalid add arguments")
		}
		params, preserveFunnelExpiry, paramsErr := addParamsFromMCPArguments(args)
		if paramsErr != nil {
			data, err = nil, paramsErr
		} else {
			data, err = s.actions.add(ctx, params, preserveFunnelExpiry)
		}
	case "list":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid list arguments")
		}
		data, err = s.actions.list()
	case "unshare":
		var args struct {
			Name string `json:"name"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" {
			return s.writeError(request.ID, -32602, "Invalid unshare arguments")
		}
		data, err = s.actions.unshare(args.Name)
	case "status":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid status arguments")
		}
		data, err = s.actions.status()
	case "url":
		var args struct {
			Name string `json:"name"`
			Wait string `json:"wait,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" {
			return s.writeError(request.ID, -32602, "Invalid url arguments")
		}
		wait, waitErr := parseMCPWait(args.Wait)
		if waitErr != nil {
			data, err = nil, waitErr
		} else {
			data, err = s.actions.url(ctx, args.Name, wait)
		}
	case "tags_list":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid tags_list arguments")
		}
		data, err = s.actions.tagsList()
	case "tags_set":
		var args struct {
			Service string `json:"service"`
			Tag     string `json:"tag"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Service == "" || args.Tag == "" {
			return s.writeError(request.ID, -32602, "Invalid tags_set arguments")
		}
		data, err = s.actions.tagsSet(args.Service, args.Tag)
	case "access_explain":
		var args struct {
			Service string `json:"service"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Service == "" {
			return s.writeError(request.ID, -32602, "Invalid access_explain arguments")
		}
		data, err = s.actions.accessExplain(args.Service)
	case "doctor":
		var args struct {
			ProbeExternal bool `json:"probe_external,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid doctor arguments")
		}
		data, err = s.actions.doctor(args.ProbeExternal)
	case "invite_user":
		var args struct {
			Email     string `json:"email"`
			Role      string `json:"role,omitempty"`
			PrintLink bool   `json:"print_link,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Email == "" {
			return s.writeError(request.ID, -32602, "Invalid invite_user arguments")
		}
		data, err = s.actions.inviteUser(ctx, args.Email, args.Role, args.PrintLink)
	case "invite_device":
		var args mcpInviteDeviceArguments
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Service == "" || args.Email == "" {
			return s.writeError(request.ID, -32602, "Invalid invite_device arguments")
		}
		data, err = s.actions.inviteDevice(ctx, args)
	case "invite_list":
		var args struct {
			ShowURLs bool `json:"show_urls,omitempty"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid invite_list arguments")
		}
		data, err = s.actions.inviteList(ctx, args.ShowURLs)
	case "invite_revoke":
		var args struct {
			Kind     string `json:"kind"`
			InviteID string `json:"invite_id"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Kind == "" || args.InviteID == "" {
			return s.writeError(request.ID, -32602, "Invalid invite_revoke arguments")
		}
		data, err = s.actions.inviteRevoke(ctx, args.Kind, args.InviteID)
	case "invite_resend":
		var args struct {
			Kind     string `json:"kind"`
			InviteID string `json:"invite_id"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Kind == "" || args.InviteID == "" {
			return s.writeError(request.ID, -32602, "Invalid invite_resend arguments")
		}
		data, err = s.actions.inviteResend(ctx, args.Kind, args.InviteID)
	case "template_list":
		var args struct{}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil {
			return s.writeError(request.ID, -32602, "Invalid template_list arguments")
		}
		data, err = s.actions.templateList()
	case "template_plan":
		var args struct {
			Name string `json:"name"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" {
			return s.writeError(request.ID, -32602, "Invalid template_plan arguments")
		}
		data, err = s.actions.templatePlan(args.Name)
	case "template_apply":
		var args struct {
			Name string `json:"name"`
		}
		if err := decodeMCPArguments(call.Arguments, &args); err != nil || args.Name == "" {
			return s.writeError(request.ID, -32602, "Invalid template_apply arguments")
		}
		data, err = s.actions.templateApply(args.Name)
	default:
		return s.writeError(request.ID, -32602, "Unknown tool: "+call.Name)
	}
	result := makeMCPToolResult(data, err)
	return s.write(mcpResponse{JSONRPC: "2.0", ID: request.ID, Result: result})
}

func makeMCPToolResult(data any, callErr error) mcpToolResult {
	if callErr != nil {
		return makeMCPToolErrorResult(callErr)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return makeMCPToolErrorResult(err)
	}
	structured := map[string]any{}
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return makeMCPToolErrorResult(err)
	}
	return mcpToolResult{
		Content:           []mcpContent{{Type: "text", Text: string(encoded)}},
		StructuredContent: structured,
	}
}

func makeMCPToolErrorResult(err error) mcpToolResult {
	failure := output.NewFailureForError("", err)
	return mcpToolResult{
		Content: []mcpContent{{Type: "text", Text: failure.Error.Message}},
		StructuredContent: map[string]any{
			"ok":    false,
			"code":  failure.Code,
			"error": failure.Error,
		},
		IsError: true,
	}
}

func init() {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the TSLink MCP server over stdio",
		Long: `Run a local Model Context Protocol server using newline-delimited JSON-RPC
over stdin/stdout. The server exposes the per-service surface of the CLI:
share, add, list, unshare, status, url, tags_list, tags_set, access_explain,
doctor, invite_user, invite_device, invite_list, invite_revoke, invite_resend,
template_list, template_plan, and template_apply. Run "tslink mcp" and send a
tools/list request to see the current set.

Daemon lifecycle, installation, login/logout, log reading and configuration are
deliberately not exposed; use the CLI for those.

The MCP process itself opens no network listener. Invoking share may start the
separate TSLink daemon and its requested tsnet service. share and add with
funnel true publish to the public internet, and the invite_* tools send or
cancel real invitations through the Tailscale API, so a client should confirm
those with its user first. Protocol frames are written only to stdout;
diagnostics and logs are written only to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput(cmd) {
				fmt.Fprintln(cmd.ErrOrStderr(), "Error: --json is not valid with tslink mcp; stdout is reserved for JSON-RPC frames")
				return output.SilentExit(output.ExitUsage)
			}
			if err := shareEnsureDirFn(); err != nil {
				return err
			}
			paths, err := resolveSharePaths()
			if err != nil {
				return err
			}
			server := newMCPServer(cmd.InOrStdin(), cmd.OutOrStdout(), defaultMCPActions(paths, cmd.ErrOrStderr()))
			if err := server.serve(cmd.Context()); err != nil {
				return fmt.Errorf("mcp stdio: %w", err)
			}
			return nil
		},
	}
	rootCmd.AddCommand(mcpCmd)
}
