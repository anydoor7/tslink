package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/filelock"
)

const (
	TypeProxy = "proxy"
	TypeFile  = "file"
	TypeTCP   = "tcp"

	maxTagLength = 63

	// TagGrammar describes the strict Tailscale ACL tag syntax accepted by TSLink.
	TagGrammar = "tag:<lowercase-hyphen-name> using lowercase letters, numbers, and hyphens"

	CodeFunnelAllowConflict      = "funnel_allow_conflict"
	CodeFunnelPublicAckRequired  = "funnel_public_ack_required"
	CodeFunnelControlURLConflict = "funnel_control_url_conflict"
	CodeFunnelTypeConflict       = "funnel_type_conflict"

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
}

func (e CodedError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

func (e CodedError) StableCode() string {
	return e.Code
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
	return CodedError{Code: CodeFunnelAllowConflict, Message: ErrFunnelAllowedUsers}
}

func FunnelPublicAckError() error {
	return CodedError{Code: CodeFunnelPublicAckRequired, Message: ErrFunnelPublicAck}
}

func FunnelControlURLError() error {
	return CodedError{Code: CodeFunnelControlURLConflict, Message: ErrFunnelControlURL}
}

func FunnelTypeConflictError(serviceType string) error {
	message := ErrFunnelTypeConflict
	if serviceType != "" {
		message = fmt.Sprintf("%s (got %q)", message, serviceType)
	}
	return CodedError{Code: CodeFunnelTypeConflict, Message: message}
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
	Services []Service `json:"services"`
}

func ValidateName(name string) error {
	if len(name) > 63 {
		return fmt.Errorf("invalid service name: %q exceeds 63-character DNS label limit", name)
	}
	if !nameRegexp.MatchString(name) {
		return fmt.Errorf("invalid service name: %q", name)
	}
	return nil
}

func ValidateTag(tag string) error {
	if len(tag) > maxTagLength {
		return fmt.Errorf("invalid tag %q: exceeds %d characters; must match %s", tag, maxTagLength, TagGrammar)
	}
	if !tagRegexp.MatchString(tag) {
		return fmt.Errorf("invalid tag %q: must match %s", tag, TagGrammar)
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
	if svc.Type == TypeTCP && len(svc.AllowedUsers) > 0 {
		return fmt.Errorf("tcp services do not support allowed_users; TSLink cannot enforce user ACLs on raw TCP services")
	}
	if err := ValidateControlURL(svc.ControlURL); err != nil {
		return err
	}
	for _, tag := range svc.Tags {
		if err := ValidateTag(tag); err != nil {
			return err
		}
	}
	return nil
}

func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Registry{Services: []Service{}}, nil
		}
		return nil, err
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return &Registry{Services: []Service{}}, nil
	}

	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, err
	}

	if reg.Services == nil {
		reg.Services = []Service{}
	}

	return &reg, nil
}

func withLock(regPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(regPath), 0o700); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if reg.Services == nil {
		reg.Services = []Service{}
	}

	data, err := marshalFn(reg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
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
