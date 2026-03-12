package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const (
	TypeProxy = "proxy"
	TypeFile  = "file"
)

var nameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type Service struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Target    string    `json:"target,omitempty"`
	Path      string    `json:"path,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	return fn()
}

func save(path string, reg *Registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if reg.Services == nil {
		reg.Services = []Service{}
	}

	data, err := json.MarshalIndent(reg, "", "  ")
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

func Add(path string, svc Service) error {
	if err := ValidateName(svc.Name); err != nil {
		return err
	}

	return withLock(path, func() error {
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
			return save(path, reg)
		}

		if svc.CreatedAt.IsZero() {
			svc.CreatedAt = time.Now().UTC()
		}

		reg.Services = append(reg.Services, svc)
		return save(path, reg)
	})
}

func Remove(path, name string) error {
	return withLock(path, func() error {
		reg, err := Load(path)
		if err != nil {
			return err
		}

		for i, svc := range reg.Services {
			if svc.Name != name {
				continue
			}

			reg.Services = append(reg.Services[:i], reg.Services[i+1:]...)
			return save(path, reg)
		}

		return fmt.Errorf("service not found: %s", name)
	})
}
