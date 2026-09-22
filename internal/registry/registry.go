package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/filelock"
)

const (
	TypeProxy = "proxy"
	TypeFile  = "file"
	TypeTCP   = "tcp"

	CurrentRegistrySchemaVersion = 1

	maxTagLength = 63

	// FunnelTag is the shared plumbing identity derived for every Funnel node at
	// construction time. It is intentionally stable so the tailnet policy needs
	// a single tag-owner rule and a single nodeAttrs grant for all Funnel nodes;
	// registry persistence is neither required nor relied upon.
	FunnelTag = "tag:tslink-funnel"

	DefaultFunnelTTL = 24 * time.Hour

	// TagGrammar describes the strict Tailscale ACL tag syntax accepted by TSLink.
	TagGrammar = "tag:<lowercase-hyphen-name> using lowercase letters, numbers, and hyphens"

	CodeFunnelAllowConflict        = "funnel_allow_conflict"
	CodeFunnelPublicAckRequired    = "funnel_public_ack_required"
	CodeFunnelControlURLConflict   = "funnel_control_url_conflict"
	CodeFunnelTypeConflict         = "funnel_type_conflict"
	CodeFunnelCapabilityMissing    = "funnel_capability_missing"
	CodeFunnelListenFailed         = "funnel_listen_failed"
	CodeServiceStartTimeout        = "service_start_timeout"
	CodeFeatureUnavailable         = "feature_unavailable"
	CodeServiceTypeAmbiguous       = "service_type_ambiguous"
	CodeInvalidServiceName         = "invalid_service_name"
	CodeInvalidTag                 = "invalid_tag"
	CodeAllowUnsupportedTCP        = "allow_unsupported_for_tcp"
	CodePathMustBeAbsolute         = "path_must_be_absolute"
	CodePathNotFound               = "path_not_found"
	CodePathNotDirectory           = "path_not_directory"
	CodePathNotAccessible          = "path_not_accessible"
	CodeLinkLocalTargetRefused     = "link_local_target_refused"
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
	ErrFunnelPublicAck    = "funnel services require recorded public acknowledgement; re-run `tslink add ... --funnel --public` or set public_ack:true after confirming public internet exposure"
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

func FeatureUnavailableError(message string) error {
	return CodedError{Code: CodeFeatureUnavailable, Message: message, Next: []string{"tslink add --help"}}
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
	if normalized == metadataHost {
		return true
	}
	// An IPv6 literal can carry a zone (fe80::1%en0); the zone selects an
	// interface and does not change which address the literal denotes.
	if percent := strings.IndexByte(normalized, '%'); percent >= 0 {
		normalized = normalized[:percent]
	}
	ip := net.ParseIP(normalized)
	if ip != nil {
		return ip.IsLinkLocalUnicast() || ip.IsUnspecified()
	}
	// The platform resolver accepts non-canonical spellings of an IPv4 address
	// that net.ParseIP rejects -- hexadecimal 0xA9FEA9FE, the dotted 32-bit
	// form 169.254.43518, octal-looking 0251.0376.0251.0376, and bare decimal
	// 2852039166 all fold into the same address the daemon would then dial.
	// Every such spelling ends in a wholly numeric label, while a real
	// hostname's last label (its TLD) is never all digits, so refusing that
	// shape closes the bypass without touching ordinary names such as
	// 169.254.169.254.example.com or 169.254.169.254.nip.io.
	if dot := strings.LastIndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[dot+1:]
	}
	return isNumericHostLabel(normalized)
}

// isNumericHostLabel reports whether label is a wholly decimal host label
// (169.254.43518) or a 0x/0X-prefixed hexadecimal one (0xA9FEA9FE). Both are
// numeric IPv4 spellings the platform resolver folds into a single address.
func isNumericHostLabel(label string) bool {
	if label == "" {
		return false
	}
	if len(label) > 2 && (strings.HasPrefix(label, "0x") || strings.HasPrefix(label, "0X")) {
		return allASCIIHexDigits(label[2:])
	}
	return allASCIIDecimalDigits(label)
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

// MiddlewareConfig defines optional middleware settings for a service.
type MiddlewareConfig struct {
	RateLimit   float64  `json:"rate_limit,omitempty"`    // requests per second, 0 = disabled
	BasicAuth   string   `json:"basic_auth,omitempty"`    // "user:pass" format
	IPAllowList []string `json:"ip_allow_list,omitempty"` // CIDR strings
	CORSOrigins []string `json:"cors_origins,omitempty"`  // allowed origins
}

type Service struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path,omitempty"`
	// File narrows a file service to exactly one name inside Path. It is the
	// bare file name, never a path. Empty means the whole Path subtree is
	// served, which is also what every registry written before this field
	// existed means, so an older file keeps its directory behaviour.
	File            string            `json:"file,omitempty"`
	Port            int               `json:"port,omitempty"`
	Ephemeral       bool              `json:"ephemeral,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	AllowedUsers    []string          `json:"allowed_users,omitempty"`
	ControlURL      string            `json:"control_url,omitempty"`
	Funnel          bool              `json:"funnel,omitempty"`
	FunnelExpiresAt *time.Time        `json:"funnel_expires_at,omitempty"`
	PublicAck       bool              `json:"public_ack,omitempty"`
	NoAutoProvision bool              `json:"no_auto_provision,omitempty"`
	Domain          string            `json:"domain,omitempty"`
	AcmeEmail       string            `json:"acme_email,omitempty"`
	Middleware      *MiddlewareConfig `json:"middleware,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
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

// FunnelExpiredAt reads wall-clock state. A missing timestamp is the legacy
// compatibility representation of never, not an implicit 24-hour deadline.
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

// FunnelRemainingAt returns a stable human/JSON duration. nil means never or
// not configured; expired deadlines return exactly "0s".
func FunnelRemainingAt(svc Service, now time.Time) *string {
	if !svc.Funnel {
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
}

var unknownJSONFieldRegexp = regexp.MustCompile(`^json: unknown field "([^"]+)"$`)

func ValidateName(name string) error {
	if len(name) > 63 {
		return CodedError{Code: CodeInvalidServiceName, Message: fmt.Sprintf("invalid service name: %q exceeds 63-character DNS label limit", name), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	if !nameRegexp.MatchString(name) {
		return CodedError{Code: CodeInvalidServiceName, Message: fmt.Sprintf("invalid service name: %q", name), Next: []string{"tslink add --help"}, MessageOnly: true}
	}
	return nil
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
	if err := ValidateName(svc.Name); err != nil {
		return err
	}
	if err := ValidateFunnelGuardrails(svc.Type, svc.Funnel, svc.AllowedUsers, svc.ControlURL, svc.PublicAck); err != nil {
		return err
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
	if err := ValidateUnavailableFeatures(svc); err != nil {
		return err
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
		if err := ValidateFileRoot(svc.Path); err != nil {
			return err
		}
		if err := ValidateServedFile(svc.File); err != nil {
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

func ValidateUnavailableFeatures(svc Service) error {
	if svc.Domain != "" || svc.AcmeEmail != "" {
		return FeatureUnavailableError(fmt.Sprintf("custom-domain/ACME runtime is not wired; remove domain/acme_email from service %q", svc.Name))
	}
	if MiddlewareConfigured(svc.Middleware) {
		return FeatureUnavailableError(fmt.Sprintf("middleware runtime is not wired; remove middleware from service %q", svc.Name))
	}
	return nil
}

func MiddlewareConfigured(mw *MiddlewareConfig) bool {
	if mw == nil {
		return false
	}
	return mw.BasicAuth != "" || mw.RateLimit != 0 || len(mw.IPAllowList) > 0 || len(mw.CORSOrigins) > 0
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

func ValidateFileRoot(path string) error {
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
	return decodeForRuntime(data)
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
	return decodeForRuntime(data)
}

func decodeForRuntime(data []byte) (*Registry, []ServiceIssue, error) {
	var wire registryWire
	if err := strictJSONDecode(data, &wire); err != nil {
		return nil, nil, configDecodeError("registry", err)
	}
	reg := &Registry{SchemaVersion: wire.SchemaVersion, Services: make([]Service, 0, len(wire.Services))}
	if err := migrate(reg); err != nil {
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
	if key == "allow" {
		message += "; registry.json uses allowed_users; allow is API-only"
	}
	return CodedError{Code: CodeUnknownConfigKey, Message: message, Next: []string{"tslink registry check --json"}, MessageOnly: true}
}

func Load(path string) (*Registry, error) {
	reg, _, err := LoadWithFileState(path)
	return reg, err
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
		return nil, "", err
	}
	var blocking []error
	for _, issue := range issues {
		code, _ := ErrorCode(issue.Err)
		if code == CodeUnknownConfigKey || code == CodeFeatureUnavailable {
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
		return nil, err
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
	return &Registry{SchemaVersion: CurrentRegistrySchemaVersion, Services: []Service{}}
}

func migrate(reg *Registry) error {
	version := reg.SchemaVersion
	if version == 0 {
		version = CurrentRegistrySchemaVersion
	}

	switch version {
	case CurrentRegistrySchemaVersion:
		reg.SchemaVersion = CurrentRegistrySchemaVersion
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

func save(path string, reg *Registry) error {
	if reg.Services == nil {
		reg.Services = []Service{}
	}
	reg.SchemaVersion = CurrentRegistrySchemaVersion

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
	// --funnel-ttl was not explicitly supplied, including legacy nil=never.
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

			svc.CreatedAt = existing.CreatedAt
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

		reg.Services = append(reg.Services, svc)
		created = true
		return save(path, reg)
	})
	return created, err
}

func Remove(path, name string) (removed bool, err error) {
	_, removed, err = RemoveAndReturn(path, name)
	return removed, err
}

func RemoveAndReturn(path, name string) (removedService Service, removed bool, err error) {
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}

		for i, svc := range reg.Services {
			if svc.Name != name {
				continue
			}

			removedService = svc
			reg.Services = append(reg.Services[:i], reg.Services[i+1:]...)
			removed = true
			return save(path, reg)
		}

		return nil
	})
	return removedService, removed, err
}

// RemoveIfUnchanged removes expected only when the currently stored service is
// byte-for-byte equivalent. It is intended for compensating transactions that
// must not delete a service another process changed after creation.
func RemoveIfUnchanged(path string, expected Service) (removed bool, err error) {
	err = withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i, svc := range reg.Services {
			if svc.Name != expected.Name || !reflect.DeepEqual(svc, expected) {
				continue
			}
			reg.Services = append(reg.Services[:i], reg.Services[i+1:]...)
			removed = true
			return save(path, reg)
		}
		return nil
	})
	return removed, err
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
			reg.Services[i] = next
			updated = next
			return save(path, reg)
		}
		return fmt.Errorf("service not found: %s", name)
	})
	return updated, err
}
