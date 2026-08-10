package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

	// TagGrammar describes the strict Tailscale ACL tag syntax accepted by TSLink.
	TagGrammar = "tag:<lowercase-hyphen-name> using lowercase letters, numbers, and hyphens"

	CodeFunnelAllowConflict      = "funnel_allow_conflict"
	CodeFunnelPublicAckRequired  = "funnel_public_ack_required"
	CodeFunnelControlURLConflict = "funnel_control_url_conflict"
	CodeFunnelTypeConflict       = "funnel_type_conflict"
	CodeFeatureUnavailable       = "feature_unavailable"
	CodeServiceTypeAmbiguous     = "service_type_ambiguous"
	CodeInvalidServiceName       = "invalid_service_name"
	CodeInvalidTag               = "invalid_tag"
	CodeAllowUnsupportedTCP      = "allow_unsupported_for_tcp"
	CodePathMustBeAbsolute       = "path_must_be_absolute"
	CodeUnknownConfigKey         = "unknown_config_key"
	CodeURLNotReady              = "url_not_ready"

	ErrFunnelAllowedUsers = "funnel services do not support allowed_users; public Funnel cannot be combined with TSLink allow lists"
	ErrFunnelPublicAck    = "funnel services require recorded public acknowledgement; re-run `tslink add ... --funnel --public` or set public_ack:true after confirming public internet exposure"
	ErrFunnelControlURL   = "funnel services do not support per-service control_url; use the default Tailscale control server or disable funnel"
	ErrFunnelTypeConflict = "funnel can only be used with proxy services; public Funnel is not supported for file or tcp services"
)

var nameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var tagRegexp = regexp.MustCompile(`^tag:[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type CodedError struct {
	Code    string
	Message string
	Next    []string
	// MessageOnly keeps wrapped human errors readable while StableCode still
	// exposes the machine discriminator.
	MessageOnly bool
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
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Target       string            `json:"target,omitempty"`
	Path         string            `json:"path,omitempty"`
	Port         int               `json:"port,omitempty"`
	Ephemeral    bool              `json:"ephemeral,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	AllowedUsers []string          `json:"allowed_users,omitempty"`
	ControlURL   string            `json:"control_url,omitempty"`
	Funnel       bool              `json:"funnel,omitempty"`
	PublicAck    bool              `json:"public_ack,omitempty"`
	Domain       string            `json:"domain,omitempty"`
	AcmeEmail    string            `json:"acme_email,omitempty"`
	Middleware   *MiddlewareConfig `json:"middleware,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type Registry struct {
	SchemaVersion int       `json:"schema_version"`
	Services      []Service `json:"services"`
}

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
	case TypeTCP:
		if svc.Target == "" {
			return fmt.Errorf("tcp services require target")
		}
		if svc.Path != "" {
			return fmt.Errorf("tcp services do not support path")
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
		return fmt.Errorf("file service path %q is not accessible: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("file service path %q is not a directory", path)
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
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return fmt.Errorf("tcp target %q has invalid port %q", target, portText)
	}
	return nil
}

func Load(path string) (*Registry, error) {
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{SchemaVersion: CurrentRegistrySchemaVersion, Services: []Service{}}, nil
		}
		return nil, err
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return &Registry{SchemaVersion: CurrentRegistrySchemaVersion, Services: []Service{}}, nil
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}
	if err := migrate(&reg); err != nil {
		return nil, err
	}

	if reg.Services == nil {
		reg.Services = []Service{}
	}
	for _, svc := range reg.Services {
		if err := ValidateUnavailableFeatures(svc); err != nil {
			return nil, fmt.Errorf("service %q: %w; edit registry.json", svc.Name, err)
		}
	}

	return &reg, nil
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
	if err := ValidateService(svc); err != nil {
		return false, err
	}

	err = withLock(path, func() error {
		reg, err := Load(path)
		if err != nil {
			return err
		}

		for i, existing := range reg.Services {
			if existing.Name != svc.Name {
				continue
			}

			svc.CreatedAt = existing.CreatedAt
			reg.Services[i] = svc
			created = false
			return save(path, reg)
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

func AddIfMissing(path string, svc Service) (created bool, err error) {
	if err := ValidateService(svc); err != nil {
		return false, err
	}

	err = withLock(path, func() error {
		reg, err := Load(path)
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
		reg, err := Load(path)
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
		reg, err := Load(path)
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

func MutateService(path, name string, mutate func(Service) (Service, error)) (Service, error) {
	var updated Service
	err := withLock(path, func() error {
		reg, err := Load(path)
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
