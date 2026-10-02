package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/filelock"
)

const (
	TypeProxy = "proxy"
	TypeFile  = "file"
	TypeTCP   = "tcp"

	LegacyRegistrySchemaVersion  = 1
	CurrentRegistrySchemaVersion = 2

	maxTagLength = 63

	// FunnelTag is the shared plumbing identity derived for every Funnel node at
	// construction time. It is intentionally stable so the tailnet policy needs
	// a single tag-owner rule and a single nodeAttrs grant for all Funnel nodes;
	// registry persistence is neither required nor relied upon.
	FunnelTag = "tag:tslink-funnel"

	DefaultFunnelTTL = 24 * time.Hour

	// FunnelNeverExpires is the stored funnel_expires_at of a Funnel the user
	// chose to keep public with no deadline. It is written explicitly so an
	// absent deadline can only mean that nobody decided.
	FunnelNeverExpires = "never"

	// TagGrammar describes the strict Tailscale ACL tag syntax accepted by TSLink.
	TagGrammar = "tag:<lowercase-hyphen-name> using lowercase letters, numbers, and hyphens"

	CodeFunnelAllowConflict        = "funnel_allow_conflict"
	CodeFunnelPublicAckRequired    = "funnel_public_ack_required"
	CodeFunnelExpiryRequired       = "funnel_expiry_required"
	CodeFunnelControlURLConflict   = "funnel_control_url_conflict"
	CodeFunnelTypeConflict         = "funnel_type_conflict"
	CodeFunnelCapabilityMissing    = "funnel_capability_missing"
	CodeFunnelListenFailed         = "funnel_listen_failed"
	CodeServiceStartTimeout        = "service_start_timeout"
	CodeServiceTypeAmbiguous       = "service_type_ambiguous"
	CodeInvalidServiceName         = "invalid_service_name"
	CodeInvalidTag                 = "invalid_tag"
	CodeAllowUnsupportedTCP        = "allow_unsupported_for_tcp"
	CodePathMustBeAbsolute         = "path_must_be_absolute"
	CodePathNotFound               = "path_not_found"
	CodePathNotDirectory           = "path_not_directory"
	CodePathNotAccessible          = "path_not_accessible"
	CodeLinkLocalTargetRefused     = "link_local_target_refused"
	CodePathExposesConfigDir       = "path_exposes_config_dir"
	CodeUnknownConfigKey           = "unknown_config_key"
	CodeInvalidServiceConfig       = "invalid_service_config"
	CodeRegistryReloadInvalid      = "registry_reload_invalid"
	CodeURLNotReady                = "url_not_ready"
	CodeEnrollmentRequired         = "enrollment_required"
	CodeLaunchctlDomainUnavailable = "launchctl_domain_unavailable"
	CodeInviteAPIKeyRequired       = "invite_api_key_required"
	CodeInviteRoleInvalid          = "invite_role_invalid"
	CodeInviteNotFound             = "invite_not_found"
	CodeInviteDeviceAmbiguous      = "invite_device_ambiguous"
	CodeInviteOwnershipUnproven    = "invite_device_ownership_unproven"
	CodeInviteAPIForbidden         = "invite_api_forbidden"
	CodeInviteResendEmailMissing   = "invite_resend_email_missing"
	CodeInviteRecipientInvalid     = "invite_recipient_invalid"
	CodeInviteIDInvalid            = "invite_id_invalid"
	CodeInviteKindInvalid          = "invite_kind_invalid"
	CodeInviteRateLimited          = "invite_rate_limited"
	CodeInviteStateConflict        = "invite_state_conflict"
	CodeInviteRequestInvalid       = "invite_request_invalid"
	CodeInviteResponseInvalid      = "invite_response_invalid"
	CodeInviteAPIUnauthorized      = "invite_api_unauthorized"
	CodeAPITokenUnauthorized       = "api_token_unauthorized"
	CodeAPIForbidden               = "api_forbidden"
	CodeLoginVerifyFailed          = "login_verify_failed"
	CodeLegacyConfigDirPresent     = config.CodeLegacyConfigDirPresent
	CodeConfigLoadFailed           = config.CodeConfigLoadFailed
	CodeCredentialURLMismatch      = "credential_control_url_mismatch"
	CodeMCPElevatedInviteRefused   = "mcp_elevated_invite_refused"

	ProvisionReasonDaemonDisabled      = "daemon_disabled"
	ProvisionReasonServiceDisabled     = "service_disabled"
	ProvisionReasonNoUsableOwner       = "no_usable_owner"
	ProvisionReasonEnsureFailed        = "ensure_failed"
	ProvisionReasonWriteUnknown        = "write_outcome_unknown"
	ProvisionReasonPolicyUpdated       = "policy_updated"
	ProvisionReasonPolicySatisfied     = "policy_already_satisfied"
	ProvisionReasonNotAttempted        = "not_attempted"
	ProvisionReasonNetmapTimeout       = "netmap_timeout"
	ProvisionReasonNetmapPollFailed    = "netmap_poll_unavailable"
	ProvisionReasonHTTPSDisabled       = "https_disabled"
	ProvisionReasonSettingsUnavailable = "settings_unavailable"
	ProvisionReasonPortUnsupported     = "port_unsupported"

	ErrFunnelAllowedUsers = "funnel services do not support allowed_users; public Funnel cannot be combined with TSLink allow lists"
	ErrFunnelPublicAck    = "funnel services require recorded public acknowledgement; re-run `tslink add ... --funnel --public` or set public_ack:true and funnel_expires_at (an RFC 3339 deadline or \"never\") after confirming public internet exposure"
	ErrFunnelControlURL   = "funnel services do not support per-service control_url; use the default Tailscale control server or disable funnel"
	ErrFunnelTypeConflict = "funnel can only be used with proxy services; public Funnel is not supported for file or tcp services"
)

var nameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var tagRegexp = regexp.MustCompile(`^tag:[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type CodedError struct {
	Code      string
	Message   string
	Next      []string
	Provision *ProvisionOutcome
	// MessageOnly keeps wrapped human errors readable while StableCode still
	// exposes the machine discriminator.
	MessageOnly bool
}

// ProvisionOutcome is stable machine-readable context for Funnel policy
// provisioning failures. Reason is a code, not prose, so agents never need to
// substring-match Message to distinguish opt-out, ensure failure, or timeout.
type ProvisionOutcome struct {
	Attempted    bool   `json:"attempted"`
	Target       string `json:"target,omitempty"`
	Changed      bool   `json:"changed"`
	Reason       string `json:"reason"`
	WriteOutcome string `json:"write_outcome,omitempty"`
}

func (e CodedError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	if e.MessageOnly {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

func (e CodedError) StableCode() string {
	return e.Code
}

// NextCommands returns deterministic recovery commands for machine consumers.
func (e CodedError) NextCommands() []string {
	return append([]string(nil), e.Next...)
}

// StableCodeError attaches a stable machine code and recovery commands to an
// error while preserving the wrapped chain, so callers that already classify
// with errors.Is (for example ErrPolicyAccessDenied) keep working and JSON
// consumers still receive error.code and error.next.
type StableCodeError struct {
	Code string
	Next []string
	Err  error
}

func (e *StableCodeError) Error() string {
	if e.Err == nil {
		return e.Code
	}
	return e.Err.Error()
}

func (e *StableCodeError) Unwrap() error { return e.Err }

func (e *StableCodeError) StableCode() string { return e.Code }

// NextCommands returns deterministic recovery commands for machine consumers.
func (e *StableCodeError) NextCommands() []string {
	return append([]string(nil), e.Next...)
}

func ErrorCode(err error) (string, bool) {
	var coded interface {
		StableCode() string
	}
	if errors.As(err, &coded) {
		return coded.StableCode(), true
	}
	return "", false
}

func FunnelAllowedUsersError() error {
	return CodedError{Code: CodeFunnelAllowConflict, Message: ErrFunnelAllowedUsers, Next: []string{"tslink add --help"}}
}

func FunnelPublicAckError() error {
	return CodedError{Code: CodeFunnelPublicAckRequired, Message: ErrFunnelPublicAck, Next: []string{"tslink add ... --funnel --public"}}
}

// FunnelExpiryRequiredError refuses a Funnel entry that records neither a
// deadline nor the explicit never. Absence is not a choice, so the entry is not
// made public at all until one of them is written.
func FunnelExpiryRequiredError(serviceName string) error {
	return CodedError{
		Code:    CodeFunnelExpiryRequired,
		Message: fmt.Sprintf("funnel service %q records no funnel_expires_at; set an RFC 3339 deadline or \"never\" to decide how long it stays public", serviceName),
		// Only a hand edit fixes it: like every other entry issue, it makes
		// the registry refuse typed rewrites until it is resolved.
		Next: []string{
			fmt.Sprintf(`Edit registry.json: on service %q set "funnel_expires_at" to the RFC 3339 time the Funnel should stop, such as "funnel_expires_at": "2030-01-01T00:00:00Z", or set "funnel_expires_at": "never" to keep it public with no deadline`, serviceName),
			"tslink registry check --json",
		},
		MessageOnly: true,
	}
}

func FunnelControlURLError() error {
	return CodedError{Code: CodeFunnelControlURLConflict, Message: ErrFunnelControlURL, Next: []string{"tslink add --help"}}
}

func FunnelTypeConflictError(serviceType string) error {
	message := ErrFunnelTypeConflict
	if serviceType != "" {
		message = fmt.Sprintf("%s (got %q)", message, serviceType)
	}
	return CodedError{Code: CodeFunnelTypeConflict, Message: message, Next: []string{"tslink add --help"}}
}

func FunnelCapabilityMissingError(serviceName string, cause error) error {
	message := fmt.Sprintf("service %q cannot enable Funnel because its tsnet node is not authorized: %v", serviceName, cause)
	return CodedError{
		Code:    CodeFunnelCapabilityMissing,
		Message: message,
		Next: []string{
			"Open the Tailscale admin console > Access controls > Funnel, then select Add Funnel to policy",
			fmt.Sprintf("Ensure nodeAttrs targets service node %q and includes attr [\"funnel\"], then restart the managed daemon with `tslink install` or restart the foreground `tslink serve` process", serviceName),
			fmt.Sprintf("tslink status --urls --name %s --json", serviceName),
		},
		MessageOnly: true,
	}
}

// FunnelCapabilityMissingProvisionError carries a structured provisioning
// outcome and caller-selected recovery steps for the precise missing
// capability cause.
func FunnelCapabilityMissingProvisionError(serviceName string, cause error, provision ProvisionOutcome, next []string) error {
	return CodedError{
		Code:        CodeFunnelCapabilityMissing,
		Message:     fmt.Sprintf("service %q cannot enable Funnel: %v", serviceName, cause),
		Next:        append([]string(nil), next...),
		Provision:   &provision,
		MessageOnly: true,
	}
}

func FunnelListenFailedError(serviceName string, cause error) error {
	return CodedError{
		Code:        CodeFunnelListenFailed,
		Message:     fmt.Sprintf("service %q passed Funnel capability preflight but its Funnel listener failed: %v", serviceName, cause),
		Next:        []string{fmt.Sprintf("tslink status --urls --name %s --json", serviceName), "tslink logs --level error --json"},
		MessageOnly: true,
	}
}

func ServiceStartTimeoutError(serviceName string, timeout time.Duration) error {
	return CodedError{
		Code:        CodeServiceStartTimeout,
		Message:     fmt.Sprintf("service %q did not reach a running tsnet state within %s", serviceName, timeout),
		Next:        []string{fmt.Sprintf("tslink status --urls --name %s --json", serviceName), "tslink logs --level error --json"},
		MessageOnly: true,
	}
}

func ServiceTypeAmbiguousError() error {
	return CodedError{
		Code:        CodeServiceTypeAmbiguous,
		Message:     "exactly one of --proxy, --dir, or --tcp must be provided",
		Next:        []string{"tslink add --help"},
		MessageOnly: true,
	}
}

func AllowUnsupportedTCPError() error {
	return CodedError{
		Code:        CodeAllowUnsupportedTCP,
		Message:     "tcp services do not support allowed_users; --allow is not supported for --tcp services",
		Next:        []string{"tslink add --help"},
		MessageOnly: true,
	}
}

func PathMustBeAbsoluteError(path string) error {
	return CodedError{
		Code:        CodePathMustBeAbsolute,
		Message:     fmt.Sprintf("file service path %q must be absolute", path),
		Next:        []string{"tslink add --help"},
		MessageOnly: true,
	}
}

func PathNotFoundError(path string) error {
	return CodedError{
		Code:        CodePathNotFound,
		Message:     fmt.Sprintf("file service path %q does not exist", path),
		Next:        []string{fmt.Sprintf("Create the directory at %q", path), "Retry the original tslink add command"},
		MessageOnly: true,
	}
}

func PathNotDirectoryError(path string) error {
	return CodedError{
		Code:        CodePathNotDirectory,
		Message:     fmt.Sprintf("file service path %q is not a directory", path),
		Next:        []string{"Choose an existing directory", "Retry the original tslink add command with --dir <absolute-directory>"},
		MessageOnly: true,
	}
}

// PathExposesConfigDirError refuses a file service whose served path is,
// contains, or lies inside TSLink's own config directory, which holds every
// service's node key and the stored credentials.
func PathExposesConfigDirError(path, configDir string) error {
	return CodedError{
		Code:        CodePathExposesConfigDir,
		Message:     fmt.Sprintf("file service path %q would serve TSLink's config directory %q (node keys, credentials and state); share a directory that neither is, contains, nor lies inside it", path, configDir),
		Next:        []string{"Choose a directory that does not contain TSLink's config directory", "Retry the original tslink add or tslink share command"},
		MessageOnly: true,
	}
}

func PathNotAccessibleError(path string, cause error) error {
	return CodedError{
		Code:        CodePathNotAccessible,
		Message:     fmt.Sprintf("file service path %q is not accessible: %v", path, cause),
		Next:        []string{fmt.Sprintf("Grant the current user read and traverse access to %q", path), "Retry the original tslink add command"},
		MessageOnly: true,
	}
}

// metadataHost is the well-known cloud metadata endpoint hostname. It is
// refused by name as well as by address: resolving it reaches the same
// link-local plane the address rule blocks, and a raw-string rule is what
// makes the refusal hold before any DNS lookup happens.
const metadataHost = "metadata.google.internal"

// LinkLocalTargetRefusedError is deliberately CodedError-shaped so an MCP
// caller receives a machine discriminator and recovery steps instead of a
// generic invalid-target failure it cannot classify.
func LinkLocalTargetRefusedError(target string) error {
	return CodedError{
		Code:        CodeLinkLocalTargetRefused,
		Message:     fmt.Sprintf("invalid target %q: link-local / cloud metadata addresses are refused", target),
		Next:        []string{"Choose a loopback, LAN, or public address the daemon can reach on its own network", "tslink add --help"},
		MessageOnly: true,
	}
}

// isRefusedTargetHost reports whether host names the link-local address space
// (IPv4 169.254.0.0/16, IPv6 fe80::/10), the unspecified address (0.0.0.0 or
// ::), or the cloud metadata endpoint. Only literals are judged; the check must
// stay hermetic, so a hostname that happens to resolve into link-local is a
// DNS-layer concern, not this one.
func isRefusedTargetHost(host string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	switch normalized {
	case metadataHost, "metadata.tencentyun.com", "instance-data", "metadata":
		return true
	}
	// An IPv6 literal can carry a zone (fe80::1%en0); the zone selects an
	// interface and does not change which address the literal denotes.
	if percent := strings.IndexByte(normalized, '%'); percent >= 0 {
		normalized = normalized[:percent]
	}
	ip := net.ParseIP(normalized)
	if ip != nil {
		return ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.String() == "fd00:ec2::254" || ip.String() == "100.100.100.200"
	}
	return isNumericHostLabel(normalized)
}

// isNumericHostLabel recognizes traditional inet_aton IPv4 spellings before
// judging their address. A numeric last label alone is not a refused target:
// for example 127.1 is loopback, while 169.254.43518 is metadata.
func isNumericHostLabel(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		return false
	}
	var address uint64
	for i, part := range parts {
		base := 10
		if strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X") {
			part, base = part[2:], 16
			if !allASCIIHexDigits(part) {
				return false
			}
		} else {
			if !allASCIIDecimalDigits(part) {
				return false
			}
			if len(part) > 1 && part[0] == '0' {
				base = 8
			}
		}
		bits := 8
		if i == len(parts)-1 {
			bits = 8 * (5 - len(parts))
		}
		value, err := strconv.ParseUint(part, base, bits)
		if err != nil {
			return false
		}
		address = address<<bits | value
	}
	ip := net.IPv4(byte(address>>24), byte(address>>16), byte(address>>8), byte(address))
	return isRefusedTargetHost(ip.String())
}

func allASCIIDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func allASCIIHexDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return false
	}
	return true
}

func URLNotReadyError(name string) error {
	return CodedError{
		Code:        CodeURLNotReady,
		Message:     fmt.Sprintf("node %q has not reported a tailnet hostname yet", name),
		Next:        []string{"tslink status --json", fmt.Sprintf("tslink url %s --wait=30s", name)},
		MessageOnly: true,
	}
}

func ValidateFunnelGuardrails(serviceType string, funnel bool, allowedUsers []string, controlURL string, publicAck bool) error {
	if !funnel {
		return nil
	}
	if serviceType != TypeProxy {
		return FunnelTypeConflictError(serviceType)
	}
	if len(allowedUsers) > 0 {
		return FunnelAllowedUsersError()
	}
	if controlURL != "" {
		return FunnelControlURLError()
	}
	if !publicAck {
		return FunnelPublicAckError()
	}
	return nil
}

// lockFn, unlockFn, and marshalFn are test hooks.
var (
	lockFn    = filelock.Lock
	unlockFn  = filelock.Unlock
	marshalFn = json.MarshalIndent
)

type Service struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
	// PreserveHost forwards the node's canonical external name as Host instead of the target's host.
	// Absent or false retains the behaviour of existing services and templates.
	PreserveHost bool          `json:"preserve_host,omitempty"`
	Health       *HealthConfig `json:"health,omitempty"`
	Path         string        `json:"path,omitempty"`
	// File narrows a file service to exactly one name inside Path. It is the
	// bare file name, never a path. Empty means the whole Path subtree is
	// served, which is also what every registry written before this field
	// existed means, so an older file keeps its directory behaviour.
	File            string     `json:"file,omitempty"`
	Port            int        `json:"port,omitempty"`
	Ephemeral       bool       `json:"ephemeral,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
	AllowedUsers    []string   `json:"allowed_users,omitempty"`
	PeopleScoped    bool       `json:"people_scoped,omitempty"`
	ControlURL      string     `json:"control_url,omitempty"`
	Funnel          bool       `json:"funnel,omitempty"`
	FunnelExpiresAt *time.Time `json:"funnel_expires_at,omitempty"`
	PublicAck       bool       `json:"public_ack,omitempty"`
	NoAutoProvision bool       `json:"no_auto_provision,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`

	// funnelExpiryUndecided is set only by UnmarshalJSON, for a Funnel entry
	// whose stored funnel_expires_at is absent or null. In memory a nil
	// FunnelExpiresAt on a Funnel service means never; this marker is what
	// keeps "nobody decided" from reading as that.
	funnelExpiryUndecided bool
}

// FunnelExpiryUndecided reports whether this entry was read from a registry
// that records neither a Funnel deadline nor the explicit never.
func (s Service) FunnelExpiryUndecided() bool {
	return s.Funnel && s.funnelExpiryUndecided
}

// MarshalJSON stores the Funnel lifetime explicitly: an RFC 3339 deadline, or
// "never" for a Funnel without one. An entry read without a decision is
// written back without one, so a rewrite never turns it into a permanent
// Funnel.
func (s Service) MarshalJSON() ([]byte, error) {
	type serviceFields Service
	wire := struct {
		serviceFields
		FunnelExpiresAt any `json:"funnel_expires_at,omitempty"`
	}{serviceFields: serviceFields(s)}
	switch {
	case s.FunnelExpiresAt != nil:
		wire.FunnelExpiresAt = s.FunnelExpiresAt
	case s.Funnel && !s.funnelExpiryUndecided:
		wire.FunnelExpiresAt = FunnelNeverExpires
	}
	return json.Marshal(wire)
}

// UnmarshalJSON reads funnel_expires_at as a deadline or the explicit never
// and marks a Funnel entry that has neither. It refuses unknown keys for every
// caller: a decoder's DisallowUnknownFields does not reach a custom
// unmarshaler, and the registry policy is strict. Recognized fields are filled
// even when the entry is refused, so diagnostics can still name what it holds.
func (s *Service) UnmarshalJSON(data []byte) error {
	type serviceFields Service
	wire := struct {
		*serviceFields
		FunnelExpiresAt json.RawMessage `json:"funnel_expires_at"`
	}{serviceFields: (*serviceFields)(s)}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&wire)

	s.FunnelExpiresAt = nil
	s.funnelExpiryUndecided = false
	raw := bytes.TrimSpace(wire.FunnelExpiresAt)
	var expiryErr error
	switch {
	case len(raw) == 0 || bytes.Equal(raw, []byte("null")):
		s.funnelExpiryUndecided = s.Funnel
	case bytes.Equal(raw, []byte(`"`+FunnelNeverExpires+`"`)):
	default:
		var deadline time.Time
		if err := deadline.UnmarshalJSON(raw); err != nil {
			expiryErr = fmt.Errorf("funnel_expires_at must be an RFC 3339 time or %q; got %s", FunnelNeverExpires, raw)
		} else {
			s.FunnelExpiresAt = &deadline
		}
	}
	if decodeErr != nil {
		return decodeErr
	}
	return expiryErr
}

// ParseFunnelTTL accepts only the public CLI contract. In particular, Go's
// time.ParseDuration does not understand days, so 7d is mapped explicitly.
func ParseFunnelTTL(value string) (duration time.Duration, never bool, err error) {
	switch value {
	case "1h":
		return time.Hour, false, nil
	case "8h":
		return 8 * time.Hour, false, nil
	case "24h":
		return DefaultFunnelTTL, false, nil
	case "72h":
		return 72 * time.Hour, false, nil
	case "7d":
		return 7 * 24 * time.Hour, false, nil
	case "never":
		return 0, true, nil
	default:
		return 0, false, fmt.Errorf("funnel TTL must be one of: 1h, 8h, 24h, 72h, 7d, never")
	}
}

// FunnelExpiredAt reads wall-clock state. A nil deadline on a decided Funnel is
// never, stored as "never"; an undecided entry never reaches the runtime.
func FunnelExpiredAt(svc Service, now time.Time) bool {
	return svc.Funnel && svc.FunnelExpiresAt != nil && !now.Before(*svc.FunnelExpiresAt)
}

// EffectiveServiceAt returns the tailnet-only form after a Funnel deadline.
// It deliberately preserves FunnelExpiresAt for observability and audit.
func EffectiveServiceAt(svc Service, now time.Time) Service {
	if FunnelExpiredAt(svc, now) {
		svc.Funnel = false
	}
	return svc
}

// FunnelRemainingAt returns a stable human/JSON duration: "never" for a Funnel
// stored without a deadline, and exactly "0s" once a deadline has passed. nil
// means no Funnel, or a Funnel whose lifetime was never decided.
func FunnelRemainingAt(svc Service, now time.Time) *string {
	if !svc.Funnel || svc.FunnelExpiryUndecided() {
		return nil
	}
	if svc.FunnelExpiresAt == nil {
		value := "never"
		return &value
	}
	remaining := svc.FunnelExpiresAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}
	value := remaining.Round(time.Second).String()
	return &value
}

type Registry struct {
	SchemaVersion int       `json:"schema_version"`
	Services      []Service `json:"services"`
	People        []Person  `json:"people,omitempty"`
}

type RegistryFileState string

const (
	RegistryFileMissing RegistryFileState = "missing"
	RegistryFileEmpty   RegistryFileState = "empty"
	RegistryFileValid   RegistryFileState = "valid"
)

// ServiceIssue is a recoverable, name-addressable registry error. Runtime
// reconciliation can fail this one service closed while continuing to serve
// other valid services. Mutating callers must reject every issue so a typed
// rewrite never discards invalid or unknown raw fields.
type ServiceIssue struct {
	Index   int
	Name    string
	Service Service
	Err     error
}

func (i ServiceIssue) Error() string {
	return fmt.Sprintf("service %q: %v; edit registry.json", i.Name, i.Err)
}

func (i ServiceIssue) Unwrap() error { return i.Err }

type registryWire struct {
	SchemaVersion int               `json:"schema_version"`
	Services      []json.RawMessage `json:"services"`
	People        []Person          `json:"people,omitempty"`
}

var unknownJSONFieldRegexp = regexp.MustCompile(`^json: unknown field "([^"]+)"$`)

func ValidateName(name string) error {
	if len(name) > 63 {
		return CodedError{Code: CodeInvalidServiceName, Message: fmt.Sprintf("invalid service name: %q exceeds 63-character DNS label limit", name), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	if !nameRegexp.MatchString(name) {
		return CodedError{Code: CodeInvalidServiceName, Message: fmt.Sprintf("invalid service name: %q", name), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	if IsWindowsReservedDeviceName(name) {
		return CodedError{Code: CodeInvalidServiceName, Message: fmt.Sprintf("invalid service name: %q is a reserved device name on Windows, and service names are also file names; choose another name", name), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	return nil
}

// IsWindowsReservedDeviceName reports whether name is one of the device names
// Windows reserves as a file name (CON, PRN, AUX, NUL, COM1-COM9, LPT1-LPT9;
// learn.microsoft.com/windows/win32/fileio/naming-a-file). A service name
// becomes nodes/<name> and node-identities/<name>.json, and the registry is a
// portable document, so the names are refused on every platform. name has
// already matched the lowercase DNS-label grammar.
func IsWindowsReservedDeviceName(name string) bool {
	switch name {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(name) == 4 && (strings.HasPrefix(name, "com") || strings.HasPrefix(name, "lpt")) && name[3] >= '1' && name[3] <= '9'
}

func ValidateTag(tag string) error {
	if len(tag) > maxTagLength {
		return CodedError{Code: CodeInvalidTag, Message: fmt.Sprintf("invalid tag %q: exceeds %d characters; must match %s", tag, maxTagLength, TagGrammar), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	if !tagRegexp.MatchString(tag) {
		return CodedError{Code: CodeInvalidTag, Message: fmt.Sprintf("invalid tag %q: must match %s", tag, TagGrammar), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	return nil
}

func ValidateControlURL(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", value, err)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("invalid URL %q: scheme must be http or https", value)
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid URL %q: host is required", value)
	}
	return nil
}

func ValidateService(svc Service) error {
	if svc.PeopleScoped && !PeopleServiceSupported(svc) {
		return CodedError{Code: "people_service_unsupported", Message: "person-scoped services must be private HTTP proxies or files; TCP cannot enforce people and Funnel is public"}
	}
	if err := ValidateHealthConfig(svc.Type, svc.Health); err != nil {
		return CodedError{Code: CodeInvalidServiceConfig, Message: err.Error(), Next: []string{"tslink add --help"}}
	}
	if err := ValidateName(svc.Name); err != nil {
		return err
	}
	if err := ValidateFunnelGuardrails(svc.Type, svc.Funnel, svc.AllowedUsers, svc.ControlURL, svc.PublicAck); err != nil {
		return err
	}
	if svc.FunnelExpiryUndecided() {
		return FunnelExpiryRequiredError(svc.Name)
	}
	if err := ValidateControlURL(svc.ControlURL); err != nil {
		return err
	}
	for _, tag := range svc.Tags {
		if err := ValidateTag(tag); err != nil {
			return err
		}
	}
	if err := validateServiceShape(svc); err != nil {
		return err
	}
	return nil
}

func validateServiceShape(svc Service) error {
	if svc.PreserveHost && svc.Type != TypeProxy {
		return fmt.Errorf("%s services do not support preserve_host; only proxy services do", svc.Type)
	}
	switch svc.Type {
	case TypeProxy:
		if svc.Target == "" {
			return fmt.Errorf("proxy services require target")
		}
		if svc.Path != "" {
			return fmt.Errorf("proxy services do not support path")
		}
		if svc.Port != 0 {
			return fmt.Errorf("proxy services do not support port")
		}
		if svc.File != "" {
			return fmt.Errorf("proxy services do not support file")
		}
		if err := ValidateProxyTarget(svc.Target); err != nil {
			return err
		}
	case TypeFile:
		if svc.Path == "" {
			return fmt.Errorf("file services require path")
		}
		if svc.Target != "" {
			return fmt.Errorf("file services do not support target")
		}
		if svc.Port != 0 {
			return fmt.Errorf("file services do not support port")
		}
		if err := validateFileRootShape(svc.Path); err != nil {
			return err
		}
		if err := ValidateServedFile(svc.File); err != nil {
			return err
		}
		if err := refuseConfigDirExposure(svc.Path, svc.File); err != nil {
			return err
		}
	case TypeTCP:
		if svc.Target == "" {
			return fmt.Errorf("tcp services require target")
		}
		if svc.Path != "" {
			return fmt.Errorf("tcp services do not support path")
		}
		if svc.File != "" {
			return fmt.Errorf("tcp services do not support file")
		}
		if svc.Funnel {
			return FunnelTypeConflictError(svc.Type)
		}
		if len(svc.AllowedUsers) > 0 {
			return AllowUnsupportedTCPError()
		}
		if err := ValidateTCPTarget(svc.Target); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported service type %q; must be one of: %s, %s, %s", svc.Type, TypeProxy, TypeFile, TypeTCP)
	}
	return nil
}

func ValidateProxyTarget(target string) error {
	parsed, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("invalid proxy target %q: %w", target, err)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("invalid proxy target %q: scheme must be http or https", target)
	}
	if parsed.Host == "" {
		return fmt.Errorf("invalid proxy target %q: host is required", target)
	}
	if isRefusedTargetHost(parsed.Hostname()) {
		return LinkLocalTargetRefusedError(target)
	}
	return nil
}

// ValidateFileRoot admits the root of a directory share: an existing absolute
// directory whose tree does not include TSLink's own config directory.
func ValidateFileRoot(path string) error {
	if err := validateFileRootShape(path); err != nil {
		return err
	}
	return refuseConfigDirExposure(path, "")
}

func validateFileRootShape(path string) error {
	if path == "" {
		return fmt.Errorf("file services require non-empty absolute path")
	}
	if !filepath.IsAbs(path) {
		return PathMustBeAbsoluteError(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PathNotFoundError(path)
		}
		return PathNotAccessibleError(path, err)
	}
	if !info.IsDir() {
		return PathNotDirectoryError(path)
	}
	return nil
}

// configDirFn resolves the config directory a share must not expose. It is
// the process's own config.Dir, the same one the daemon reads its node state
// from.
var configDirFn = config.Dir

// refuseConfigDirExposure refuses a file service that would serve TSLink's
// config directory. A directory share (file empty) serves its whole tree, so
// its root must neither be, contain, nor lie inside the config directory. A
// single-file share serves one regular file, so the directory that file
// lives in and the file's own resolved location matter. The directory is
// checked whether or not the file exists yet: the daemon serves the file once
// it appears. Both sides are compared as files (os.SameFile) after
// resolving symlinks, so another spelling of the same directory, a symlink to
// it, or a case variant on a case-insensitive file system is the same
// directory. It does not change what an accepted share serves.
func refuseConfigDirExposure(root, file string) error {
	configDir, err := configDirFn()
	if err != nil {
		return fmt.Errorf("resolve TSLink's config directory to check file service path %q: %w", root, err)
	}
	if absolute, err := filepath.Abs(configDir); err == nil {
		configDir = absolute
	}
	served := root
	if file != "" {
		served = filepath.Join(root, file)
	}
	servedInfo, err := os.Stat(served)
	missingFile := file != "" && errors.Is(err, fs.ErrNotExist)
	if err != nil && !missingFile {
		return PathNotAccessibleError(served, err)
	}

	if file == "" {
		// The root is the config directory or one of its ancestors, along
		// either the path as spelled or the path its symlinks resolve to.
		for _, dir := range configDirAncestry(configDir) {
			if info, err := os.Stat(dir); err == nil && os.SameFile(info, servedInfo) {
				return PathExposesConfigDirError(root, configDir)
			}
		}
	}

	configInfo, err := os.Stat(configDir)
	if err != nil {
		// A config directory that does not exist yet has nothing inside it.
		return nil
	}
	var locations []string
	if file != "" {
		parent, ok := resolveThroughExistingAncestor(root)
		if !ok {
			return PathNotAccessibleError(root, fs.ErrNotExist)
		}
		locations = append(locations, parent)
	}
	if !missingFile {
		resolved, err := filepath.EvalSymlinks(served)
		if err != nil {
			return PathNotAccessibleError(served, err)
		}
		locations = append(locations, resolved)
	}
	for _, location := range locations {
		for _, dir := range pathAndAncestors(location) {
			if info, err := os.Stat(dir); err == nil && os.SameFile(info, configInfo) {
				return PathExposesConfigDirError(root, configDir)
			}
		}
	}
	return nil
}

// configDirAncestry lists configDir and its ancestors as spelled, then as
// resolved through symlinks. The config directory itself need not exist yet:
// its nearest existing ancestor is resolved and the rest appended.
func configDirAncestry(configDir string) []string {
	dirs := pathAndAncestors(configDir)
	if resolved, ok := resolveThroughExistingAncestor(configDir); ok {
		dirs = append(dirs, pathAndAncestors(resolved)...)
	}
	return dirs
}

// resolveThroughExistingAncestor resolves the symlinks of path's nearest
// existing ancestor (path itself when it exists) and appends the part that
// does not exist yet.
func resolveThroughExistingAncestor(path string) (string, bool) {
	rest := ""
	for dir := path; ; {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

func pathAndAncestors(path string) []string {
	var dirs []string
	for dir := filepath.Clean(path); ; {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			return dirs
		}
		dir = parent
	}
}

// IsHomeDir reports whether path is the current user's home directory. A
// directory share of home is accepted when the config directory lives
// elsewhere, and callers warn about it.
func IsHomeDir(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	homeInfo, err := os.Stat(home)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && os.SameFile(info, homeInfo)
}

// ValidateServedFile constrains Service.File to one name resolved inside the
// service's own Path. The daemon anchors every open at an os.Root handle on
// Path, so a separator or a dot segment here cannot reach outside that root in
// the first place; the point of rejecting them at the registry boundary is that
// "file" must not be able to express a second directory level at all. A file
// service whose File names a subdirectory entry would still be one file, but it
// would also make Path stop describing what the URL serves, and every reader of
// the registry -- inspect, doctor, the docs -- would then be describing a
// different thing from the daemon.
//
// Empty is valid and means the whole Path subtree, which is what every registry
// written before this field existed says by omission.
func ValidateServedFile(file string) error {
	if file == "" {
		return nil
	}
	return ValidateServedFileName(file)
}

// ValidateServedFileName is the shape rule itself, with no empty-means-whole-
// directory reading. internal/server calls it from NewSingleFileHandler so the
// registry boundary and the handler constructor accept exactly the same set:
// two nearly-identical copies of this predicate had already drifted apart, and
// the direction of the drift was the dangerous one -- the registry accepted a
// whitespace-only name that the handler rejected, so a hand-written or
// third-party registry entry passed `tslink registry check` and then failed at
// node startup, with the error surfacing in the daemon rather than at the
// boundary whose job is to refuse it.
func ValidateServedFileName(file string) error {
	// Whitespace-only is not a file name. It is worth naming separately from
	// the other rejections because it is the one a human produces by accident.
	if strings.TrimSpace(file) == "" {
		return fmt.Errorf("file services require file to be a file name; got %q, which is empty or whitespace only", file)
	}
	// Control characters never appear in a name a user meant to type, and they
	// do appear in a name built by a broken writer or an injection attempt: a
	// newline splits a log line, and a NUL truncates the name for any C API
	// that later receives it.
	if idx := strings.IndexFunc(file, func(r rune) bool { return r < 0x20 || r == 0x7f }); idx >= 0 {
		return fmt.Errorf("file services require file to be free of control characters; got %q", file)
	}
	// Both separators are rejected on every platform. A registry file is a
	// portable document: one written on Windows is readable on Unix, where
	// filepath.Base would not treat a backslash as a separator and would accept
	// the whole thing as a single strange name.
	if strings.ContainsAny(file, `/\`) || file != filepath.Base(file) || file == "." || file == ".." {
		return fmt.Errorf("file services require file to be a bare file name inside path, without a path separator or dot segment; got %q", file)
	}
	return nil
}

func ValidateTCPTarget(target string) error {
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("tcp target %q must be host:port: %w", target, err)
	}
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("tcp target %q must include a host", target)
	}
	if isRefusedTargetHost(host) {
		return LinkLocalTargetRefusedError(target)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("tcp target %q has invalid port %q", target, portText)
	}
	return nil
}

// readRegistryFile is a seam. The two loaders below both turn a missing file
// into a valid empty registry, and that single os.IsNotExist check is all that
// separates "no services configured yet" from "the registry is unreadable, so
// every configured service would be torn down". A test has to be able to
// produce a non-ENOENT read error to hold that line, and on Unix a regular
// file the caller owns cannot be made unreadable to that caller.
var readRegistryFile = os.ReadFile

// LoadForRuntime strictly decodes registry.json while isolating errors whose
// service name remains trustworthy. A malformed top-level document or a
// service without a usable name is global-invalid because runtime cannot know
// which existing listener the raw entry was intended to replace.
func LoadForRuntime(path string) (*Registry, []ServiceIssue, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nil, nil, err
	}
	data, err := readRegistryFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyRegistry(), nil, nil
		}
		return nil, nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, fmt.Errorf("registry.json is empty: %s", path)
	}
	reg, issues, err := decodeForRuntime(data)
	return reg, issues, registryLoadError(path, err)
}

// Preflight reads and strictly validates a registry copy without changing its
// mode or contents. It is suitable for compatibility checks before upgrading.
func Preflight(path string) (*Registry, []ServiceIssue, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, fmt.Errorf("registry.json is empty: %s", path)
	}
	reg, issues, err := decodeForRuntime(data)
	return reg, issues, registryLoadError(path, err)
}

func decodeForRuntime(data []byte) (*Registry, []ServiceIssue, error) {
	var wire registryWire
	if err := strictJSONDecode(data, &wire); err != nil {
		return nil, nil, configDecodeError("registry", err)
	}
	reg := &Registry{SchemaVersion: wire.SchemaVersion, Services: make([]Service, 0, len(wire.Services)), People: wire.People}
	if err := migrate(reg); err != nil {
		return nil, nil, err
	}
	if err := validatePeople(reg.People); err != nil {
		return nil, nil, err
	}

	issues := make([]ServiceIssue, 0)
	seenNames := make(map[string]struct{}, len(wire.Services))
	for index, raw := range wire.Services {
		var identity struct {
			Name json.RawMessage `json:"name"`
		}
		if err := json.Unmarshal(raw, &identity); err != nil {
			return nil, nil, fmt.Errorf("registry service at index %d is malformed: %w", index, err)
		}
		var name string
		if len(identity.Name) == 0 || json.Unmarshal(identity.Name, &name) != nil || strings.TrimSpace(name) == "" {
			return nil, nil, fmt.Errorf("registry service at index %d has no usable string name", index)
		}
		if _, duplicate := seenNames[name]; duplicate {
			return nil, nil, fmt.Errorf("registry contains duplicate service name %q", name)
		}
		seenNames[name] = struct{}{}

		var svc Service
		// Preserve all recognized fields for fail-closed runtime evidence even
		// when strict decoding below finds an unknown key.
		_ = json.Unmarshal(raw, &svc)
		if err := strictJSONDecode(raw, &svc); err != nil {
			svc.Name = name
			issues = append(issues, ServiceIssue{Index: index, Name: name, Service: svc, Err: configDecodeError(name, err)})
			continue
		}
		if err := ValidateService(svc); err != nil {
			issues = append(issues, ServiceIssue{Index: index, Name: name, Service: svc, Err: err})
			continue
		}
		reg.Services = append(reg.Services, svc)
	}
	return reg, issues, nil
}

// registryLoadError classifies malformed user input at the file boundary so
// every reader retains the path and recovery guidance. Existing specific
// codes (such as unknown_config_key) keep their classification.
func registryLoadError(path string, err error) error {
	if err == nil {
		return nil
	}
	if _, coded := ErrorCode(err); coded {
		return err
	}
	return &StableCodeError{
		Code: "usage_error",
		Err:  fmt.Errorf("load registry %q: %w", path, err),
		Next: []string{"Check and repair the JSON syntax and field types in " + path, "tslink registry check --help"},
	}
}

func strictJSONDecode(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func configDecodeError(scope string, err error) error {
	matches := unknownJSONFieldRegexp.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return err
	}
	key := matches[1]
	message := fmt.Sprintf("unknown registry.json key %q in %s", key, scope)
	switch key {
	case "allow":
		message += "; registry.json uses allowed_users; allow is API-only"
	case "domain", "acme_email", "middleware":
		message += "; custom domains, ACME and middleware are not implemented, so the key was removed; delete it"
	}
	return CodedError{Code: CodeUnknownConfigKey, Message: message, Next: []string{"tslink registry check --json"}, MessageOnly: true}
}

func Load(path string) (*Registry, error) {
	reg, _, err := LoadWithFileState(path)
	return reg, err
}

// LoadForDiagnostics retains trustworthy names and recognized fields from
// isolated entries alongside healthy services. Callers must display issues;
// this view must never be used to rewrite the registry. Mutation loaders stay
// strict so unknown input is never silently discarded.
func LoadForDiagnostics(path string) (*Registry, []ServiceIssue, error) {
	reg, issues, err := LoadForRuntime(path)
	if err != nil || len(issues) == 0 {
		return reg, issues, err
	}
	if len(reg.Services) == 0 {
		var blocking []error
		for _, issue := range issues {
			if code, _ := ErrorCode(issue.Err); code == CodeUnknownConfigKey {
				blocking = append(blocking, issue)
			}
		}
		if len(blocking) > 0 {
			// Preserve the strict refusal when there are no healthy entries
			// to display. The error still names each unknown-key entry.
			return nil, issues, errors.Join(blocking...)
		}
	}
	services := make([]Service, len(reg.Services)+len(issues))
	validIndex, issueIndex := 0, 0
	for index := range services {
		if issueIndex < len(issues) && issues[issueIndex].Index == index {
			services[index] = issues[issueIndex].Service
			issueIndex++
		} else {
			services[index] = reg.Services[validIndex]
			validIndex++
		}
	}
	reg.Services = services
	return reg, issues, nil
}

// LoadWithFileState preserves Load's compatibility behavior while exposing
// whether its empty registry came from an absent file, blank contents, or a
// successfully decoded registry. Deletion callers use this distinction to
// fail closed without treating a valid zero-service registry as corruption.
func LoadWithFileState(path string) (*Registry, RegistryFileState, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nil, "", err
	}
	data, err := readRegistryFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyRegistry(), RegistryFileMissing, nil
		}
		return nil, "", err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return emptyRegistry(), RegistryFileEmpty, nil
	}
	reg, issues, err := decodeForRuntime(data)
	if err != nil {
		return nil, "", registryLoadError(path, err)
	}
	var blocking []error
	for _, issue := range issues {
		if code, _ := ErrorCode(issue.Err); code == CodeUnknownConfigKey {
			blocking = append(blocking, issue)
		}
	}
	if len(blocking) > 0 {
		return nil, "", errors.Join(blocking...)
	}
	if len(issues) == 0 {
		return reg, RegistryFileValid, nil
	}
	// Diagnostic readers historically inspect recognized-but-invalid service
	// shapes (for example doctor/access warnings). Unknown keys are never
	// admitted above. Mutations use loadForMutation and reject every issue.
	// The document is decoded into a fresh value: encoding/json reuses the
	// elements of a slice it decodes into, so decoding over the valid
	// services would give an invalid entry every field it omits from the
	// valid entry at its index.
	reg = &Registry{}
	if err := json.Unmarshal(data, reg); err != nil {
		return nil, "", err
	}
	if err := migrate(reg); err != nil {
		return nil, "", err
	}
	if reg.Services == nil {
		reg.Services = []Service{}
	}
	return reg, RegistryFileValid, nil
}

func loadForMutation(path string) (*Registry, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyRegistry(), nil
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return emptyRegistry(), nil
	}
	reg, issues, err := decodeForRuntime(data)
	if err != nil {
		return nil, registryLoadError(path, err)
	}
	if len(issues) == 0 {
		return reg, nil
	}
	errs := make([]error, 0, len(issues))
	for _, issue := range issues {
		errs = append(errs, issue)
	}
	return nil, errors.Join(errs...)
}

func emptyRegistry() *Registry {
	return &Registry{SchemaVersion: LegacyRegistrySchemaVersion, Services: []Service{}}
}

func migrate(reg *Registry) error {
	version := reg.SchemaVersion
	if version == 0 {
		version = LegacyRegistrySchemaVersion
	}

	switch version {
	case LegacyRegistrySchemaVersion, CurrentRegistrySchemaVersion:
		reg.SchemaVersion = version
		return nil
	default:
		return fmt.Errorf("unsupported registry schema_version: %d", reg.SchemaVersion)
	}
}

func withLock(regPath string, fn func() error) error {
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(regPath)); err != nil {
		return err
	}
	if err := atomicfile.ConvergePrivateFile(regPath + ".lock"); err != nil {
		return err
	}

	lockFile, err := os.OpenFile(regPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := lockFn(lockFile); err != nil {
		return err
	}
	defer unlockFn(lockFile)

	return fn()
}

// tryWithLock uses the same on-disk lock as mutations, but never waits for a
// writer. Final orphan cleanup can then retain state when proof is unavailable.
func tryWithLock(regPath string, fn func() error) (bool, error) {
	if err := atomicfile.EnsurePrivateDir(filepath.Dir(regPath)); err != nil {
		return false, err
	}
	if err := atomicfile.ConvergePrivateFile(regPath + ".lock"); err != nil {
		return false, err
	}
	lockFile, err := os.OpenFile(regPath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lockFile.Close()
	acquired, err := filelock.TryLock(lockFile)
	if err != nil || !acquired {
		return acquired, err
	}
	defer filelock.Unlock(lockFile)
	return true, fn()
}

// WithLockedFileState runs a short local-state decision under the same lock as
// Add and Remove. It lets a cleanup caller recheck membership and act before a
// concurrent registry mutation can re-add that service.
func WithLockedFileState(path string, fn func(*Registry, RegistryFileState) error) error {
	return withLock(path, func() error {
		reg, state, err := LoadWithFileState(path)
		if err != nil {
			return err
		}
		return fn(reg, state)
	})
}

// TryWithLockedFileState reports false on lock contention without invoking fn.
func TryWithLockedFileState(path string, fn func(*Registry, RegistryFileState) error) (bool, error) {
	return tryWithLock(path, func() error {
		reg, state, err := LoadWithFileState(path)
		if err != nil {
			return err
		}
		return fn(reg, state)
	})
}

func save(path string, reg *Registry) error {
	if reg.Services == nil {
		reg.Services = []Service{}
	}
	reg.SchemaVersion = LegacyRegistrySchemaVersion
	if len(reg.People) > 0 {
		reg.SchemaVersion = PeopleRegistrySchemaVersion
	}
	for _, svc := range reg.Services {
		if svc.PeopleScoped {
			reg.SchemaVersion = PeopleRegistrySchemaVersion
		}
	}

	data, err := marshalFn(reg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return atomicfile.WriteFile(path, data)
}

func Add(path string, svc Service) (created bool, err error) {
	return AddWithOptions(path, svc, AddOptions{})
}

type AddOptions struct {
	// PreserveFunnelExpiry keeps an existing entry's deadline. It is used when
	// --funnel-ttl was not explicitly supplied, including an explicit never.
	PreserveFunnelExpiry bool
	// Now is injectable for deterministic expiration decisions. Zero uses the
	// current wall clock.
	Now time.Time
}

func AddWithOptions(path string, svc Service, options AddOptions) (created bool, err error) {
	outcome, err := AddWithOutcome(path, svc, options)
	return outcome.Created, err
}

type AddOutcome struct {
	Created              bool
	RearmedExpiredFunnel bool
	// Replaced is the entry this add replaced, read under the registry lock;
	// nil when the add created the service.
	Replaced *Service
}

// ChangedFields names, by their registry.json keys and in sorted order, the
// fields whose stored value differs between before and after, including
// fields after no longer has. Tag order is not a change; name and created_at
// are identity and bookkeeping, not settings, and are not compared.
func ChangedFields(before, after Service) ([]string, error) {
	beforeFields, err := serviceFieldMap(before)
	if err != nil {
		return nil, err
	}
	afterFields, err := serviceFieldMap(after)
	if err != nil {
		return nil, err
	}
	changed := []string{}
	for key := range beforeFields {
		if _, ok := afterFields[key]; !ok {
			afterFields[key] = nil
		}
	}
	for key, value := range afterFields {
		if key == "name" || key == "created_at" {
			continue
		}
		if key == "tags" {
			if !sameTagSet(before.Tags, after.Tags) {
				changed = append(changed, key)
			}
			continue
		}
		if !bytes.Equal(beforeFields[key], value) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func serviceFieldMap(svc Service) (map[string]json.RawMessage, error) {
	data, err := json.Marshal(svc)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func sameTagSet(a, b []string) bool {
	seen := make(map[string]bool, len(a))
	for _, tag := range a {
		seen[tag] = true
	}
	other := make(map[string]bool, len(b))
	for _, tag := range b {
		if !seen[tag] {
			return false
		}
		other[tag] = true
	}
	return len(seen) == len(other)
}

func AddWithOutcome(path string, svc Service, options AddOptions) (outcome AddOutcome, err error) {
	if err := ValidateService(svc); err != nil {
		return AddOutcome{}, err
	}
	now := options.Now.UTC()
	if options.Now.IsZero() {
		now = time.Now().UTC()
	}

	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}

		for i, existing := range reg.Services {
			if existing.Name != svc.Name {
				continue
			}

			previous := existing
			outcome.Replaced = &previous
			svc.CreatedAt = existing.CreatedAt
			svc.PeopleScoped = svc.PeopleScoped || existing.PeopleScoped
			if err := ValidateService(svc); err != nil {
				return err
			}
			if options.PreserveFunnelExpiry {
				if svc.Funnel && existing.FunnelExpiresAt != nil && !existing.FunnelExpiresAt.After(now) {
					rearmed := now.Add(DefaultFunnelTTL).UTC()
					svc.FunnelExpiresAt = &rearmed
					outcome.RearmedExpiredFunnel = true
				} else {
					svc.FunnelExpiresAt = existing.FunnelExpiresAt
				}
			}
			reg.Services[i] = svc
			outcome.Created = false
			return save(path, reg)
		}

		if svc.CreatedAt.IsZero() {
			svc.CreatedAt = time.Now().UTC()
		}

		reg.Services = append(reg.Services, svc)
		outcome.Created = true
		return save(path, reg)
	})
	return outcome, err
}

func AddIfMissing(path string, svc Service) (created bool, err error) {
	return addIfMissing(path, svc, false)
}

// AddTentative is AddIfMissing for a caller that may still undo the creation
// with RemoveIfUnchanged: the service it creates stays tentative until the
// caller, or anyone else relying on it, settles it with KeepIfUnchanged.
func AddTentative(path string, svc Service) (created bool, err error) {
	return addIfMissing(path, svc, true)
}

func addIfMissing(path string, svc Service, tentative bool) (created bool, err error) {
	if err := ValidateService(svc); err != nil {
		return false, err
	}

	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}

		for _, existing := range reg.Services {
			if existing.Name == svc.Name {
				created = false
				return nil
			}
		}

		if svc.CreatedAt.IsZero() {
			svc.CreatedAt = time.Now().UTC()
		}

		// The mark goes first, so a registration its creator may still
		// roll back always carries one; a failed save takes it away again.
		if tentative {
			if err := atomicfile.WriteFile(tentativeMarkPath(path, svc.Name), tentativeMark(svc)); err != nil {
				return err
			}
		}
		reg.Services = append(reg.Services, svc)
		if err := save(path, reg); err != nil {
			// A directory sync can fail after rename published the service.
			// Report that creation and keep its mark so share can compensate.
			published, readErr := Load(path)
			if readErr == nil {
				for _, stored := range published.Services {
					if reflect.DeepEqual(stored, svc) {
						created = true
						break
					}
				}
			}
			if tentative && readErr == nil && !created {
				_ = dropTentativeMark(path, svc.Name)
			}
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func Remove(path, name string) (removed bool, err error) {
	_, removed, err = RemoveAndReturn(path, name)
	return removed, err
}

func RemoveAndReturn(path, name string) (removedService Service, removed bool, err error) {
	return RemoveAndReturnWithin(path, name, func(_ Service, commit func() error) error {
		return commit()
	})
}

// RemoveAndReturnWithin is RemoveAndReturn for a caller that must record the
// removal elsewhere in the same step. Once the service is found under the
// registry lock, within receives it and commit, which writes registry.json
// without it; the service is removed only when within calls commit and commit
// succeeds. Nothing can add, change or remove the service between the lookup
// and the commit.
func RemoveAndReturnWithin(path, name string, within func(svc Service, commit func() error) error) (removedService Service, removed bool, err error) {
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}

		for i, svc := range reg.Services {
			if svc.Name != name {
				continue
			}

			remaining := append(append([]Service{}, reg.Services[:i]...), reg.Services[i+1:]...)
			commit := func() error {
				reg.Services = remaining
				removeAppGrants(reg, name)
				if err := save(path, reg); err != nil {
					return err
				}
				removedService, removed = svc, true
				return nil
			}
			return within(svc, commit)
		}

		return nil
	})
	return removedService, removed, err
}

// A registration is tentative while the call that created it may still undo
// the creation: `tslink share` registers a service, waits for it, and removes
// it again when the wait fails. Content equality cannot tell that removal
// whether another call relies on the registration by now -- an identical share
// reuses it unchanged and may already have reported it ready -- so the creation
// leaves a mark beside registry.json holding its created_at, and every caller
// that relies on the registration as stored takes the mark away under the
// registry lock. The compensating removal applies only while the mark of that
// very creation is still there.
func tentativeMarkPath(regPath, name string) string {
	return regPath + ".tentative-" + name
}

func tentativeMark(svc Service) []byte {
	return []byte(svc.CreatedAt.UTC().Format(time.RFC3339Nano) + "\n")
}

// A tentative mutation keeps the previous value's mark after its own first
// line, if that value was also tentative. Restoring it then restores the
// earlier caller's right to compensate. Repeated mutations retain the chain.
func splitTentativeMark(mark []byte) (current, previous []byte) {
	if end := bytes.IndexByte(mark, '\n'); end >= 0 {
		return mark[:end+1], mark[end+1:]
	}
	return mark, nil
}

func dropTentativeMark(regPath, name string) error {
	if err := os.Remove(tentativeMarkPath(regPath, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// KeepIfUnchanged settles expected when it is still stored exactly as given:
// afterwards RemoveIfUnchanged no longer removes it, whoever created it. A
// share reusing an existing registration calls it before relying on that
// registration, and the share that created one calls it once it succeeds. It
// reports false, and settles nothing, when the stored service changed or is
// gone.
func KeepIfUnchanged(path string, expected Service) (kept bool, err error) {
	err = withLock(path, func() error {
		reg, err := Load(path)
		if err != nil {
			return err
		}
		for _, svc := range reg.Services {
			if svc.Name != expected.Name || !reflect.DeepEqual(svc, expected) {
				continue
			}
			if err := dropTentativeMark(path, expected.Name); err != nil {
				return err
			}
			kept = true
			return nil
		}
		return nil
	})
	return kept, err
}

// RemoveIfUnchanged undoes an AddTentative: it removes expected only when the
// currently stored service is byte-for-byte equivalent and still carries the
// tentative mark of its creation. It deletes neither a service another process
// changed after creation nor one another call has kept since.
func RemoveIfUnchanged(path string, expected Service) (removed bool, err error) {
	err = withLock(path, func() error {
		mark, err := os.ReadFile(tentativeMarkPath(path, expected.Name))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		current, _ := splitTentativeMark(mark)
		if !bytes.Equal(current, tentativeMark(expected)) {
			return nil
		}
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, svc := range reg.Services {
			if svc.Name != expected.Name || !reflect.DeepEqual(svc, expected) {
				continue
			}
			reg.Services = append(reg.Services[:i], reg.Services[i+1:]...)
			removeAppGrants(reg, expected.Name)
			if err := save(path, reg); err != nil {
				return err
			}
			removed = true
			// The service is gone, so a mark left behind names nothing any
			// caller can match; failing to delete it is not a failed undo.
			_ = dropTentativeMark(path, expected.Name)
			return nil
		}
		return nil
	})
	return removed, err
}

// ReplaceIfUnchanged restores a prior service value only when the current
// value still equals expected. It keeps a compensating write from clobbering
// another process's registry edit or resurrecting a removed service.
func ReplaceIfUnchanged(path string, expected, replacement Service) (replaced bool, err error) {
	return replaceIfUnchanged(path, expected, replacement, false)
}

// RestoreTentativeIfUnchanged compensates a tentative mutation only while no
// other caller has kept the resulting registration.
func RestoreTentativeIfUnchanged(path string, expected, replacement Service) (bool, error) {
	return replaceIfUnchanged(path, expected, replacement, true)
}

func replaceIfUnchanged(path string, expected, replacement Service, tentative bool) (replaced bool, err error) {
	if expected.Name != replacement.Name {
		return false, fmt.Errorf("replacement service name differs from expected name")
	}
	if err := ValidateService(replacement); err != nil {
		return false, err
	}
	err = withLock(path, func() error {
		var previousMark []byte
		if tentative {
			mark, err := os.ReadFile(tentativeMarkPath(path, expected.Name))
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			current, previous := splitTentativeMark(mark)
			if !bytes.Equal(current, tentativeMark(expected)) {
				return nil
			}
			previousMark = previous
		}
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, svc := range reg.Services {
			if svc.Name != expected.Name {
				continue
			}
			if !reflect.DeepEqual(svc, expected) {
				return nil
			}
			reg.Services[i] = replacement
			if err := save(path, reg); err != nil {
				return err
			}
			replaced = true
			if tentative {
				if len(previousMark) > 0 {
					return atomicfile.WriteFile(tentativeMarkPath(path, replacement.Name), previousMark)
				}
				_ = dropTentativeMark(path, expected.Name)
			}
			return nil
		}
		return nil
	})
	return replaced, err
}

// DowngradeExpiredFunnels atomically converts every expired Funnel service to
// tailnet-only while retaining the service and its deadline. dryRun computes
// the same result without writing registry.json.
func DowngradeExpiredFunnels(path string, now time.Time, dryRun bool) (expired []Service, err error) {
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, svc := range reg.Services {
			if !FunnelExpiredAt(svc, now) {
				continue
			}
			expired = append(expired, svc)
			reg.Services[i] = EffectiveServiceAt(svc, now)
		}
		if dryRun || len(expired) == 0 {
			return nil
		}
		return save(path, reg)
	})
	return expired, err
}

func MutateService(path, name string, mutate func(Service) (Service, error)) (Service, error) {
	return mutateService(path, name, mutate, false)
}

// MutateServiceTentative marks the new value before publishing it so a failed
// caller can restore the previous value unless another caller keeps it first.
func MutateServiceTentative(path, name string, mutate func(Service) (Service, error)) (Service, error) {
	return mutateService(path, name, mutate, true)
}

func mutateService(path, name string, mutate func(Service) (Service, error), tentative bool) (Service, error) {
	var updated Service
	err := withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, existing := range reg.Services {
			if existing.Name != name {
				continue
			}
			next, err := mutate(existing)
			if err != nil {
				return err
			}
			if next.Name == "" {
				next.Name = existing.Name
			}
			if next.Name != existing.Name {
				return fmt.Errorf("service name mutation is not supported: %q to %q", existing.Name, next.Name)
			}
			if next.CreatedAt.IsZero() {
				next.CreatedAt = existing.CreatedAt
			}
			if err := ValidateService(next); err != nil {
				return err
			}
			if tentative {
				mark, err := os.ReadFile(tentativeMarkPath(path, existing.Name))
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					return err
				}
				nextMark := tentativeMark(next)
				current, _ := splitTentativeMark(mark)
				if bytes.Equal(current, tentativeMark(existing)) {
					nextMark = append(nextMark, mark...)
				}
				if err := atomicfile.WriteFile(tentativeMarkPath(path, next.Name), nextMark); err != nil {
					return err
				}
			}
			reg.Services[i] = next
			updated = next
			return save(path, reg)
		}
		return fmt.Errorf("service not found: %s", name)
	})
	return updated, err
}
