package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/monody0007/tslink/internal/filelock"
)

const (
	TypeProxy = "proxy"
	TypeFile  = "file"
	TypeTCP   = "tcp"
)

var nameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var tagRegexp = regexp.MustCompile(`^tag:[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

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
	Domain       string            `json:"domain,omitempty"`
	AcmeEmail    string            `json:"acme_email,omitempty"`
	Middleware   *MiddlewareConfig `json:"middleware,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type Registry struct {
	Services []Service `json:"services"`
}

func ValidateName(name string) error {
	if !nameRegexp.MatchString(name) {
		return fmt.Errorf("invalid service name: %q", name)
	}
	return nil
}

func ValidateTag(tag string) error {
	if !tagRegexp.MatchString(tag) {
		return fmt.Errorf("invalid tag: %q", tag)
	}
	return nil
}

func ValidateService(svc Service) error {
	if err := ValidateName(svc.Name); err != nil {
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

func Remove(path, name string) (removed bool, err error) {
	err = withLock(path, func() error {
		reg, err := Load(path)
		if err != nil {
			return err
		}

		for i, svc := range reg.Services {
			if svc.Name != name {
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
