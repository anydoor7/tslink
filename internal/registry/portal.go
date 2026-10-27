package registry

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultPortalHostname = "home"
const CodePortalFunnelRefused = "portal_funnel_refused"

// PortalConfig is independent of Services: it can never enter the Funnel
// service listener, cleanup, health probing or service-state directories.
// Owner and Admins are explicit administrative identities for app access.
type PortalConfig struct {
	Enabled  bool           `json:"enabled"`
	Hostname string         `json:"hostname"`
	Owner    string         `json:"owner"`
	Admins   []string       `json:"admins,omitempty"`
	Funnel   bool           `json:"funnel,omitempty"`
	Gateway  *GatewayConfig `json:"gateway,omitempty"`
}

func portalConflict(name string) error {
	return CodedError{Code: "portal_hostname_conflict", Message: fmt.Sprintf("portal hostname %q is reserved; choose a different portal or app name", name)}
}

func ValidatePortal(p *PortalConfig) error {
	if p == nil {
		return nil
	}
	if p.Funnel {
		return CodedError{Code: CodePortalFunnelRefused, Message: "the portal is Tailnet-only and cannot use Funnel"}
	}
	if err := ValidateGatewayConfig(p.Gateway); err != nil {
		return err
	}
	if err := ValidateName(p.Hostname); err != nil {
		return err
	}
	for _, login := range append([]string{p.Owner}, p.Admins...) {
		if login == "" && p.Owner == "" && len(p.Admins) == 0 && p.Gateway != nil && p.Gateway.Enabled {
			continue // Enrollment has not established a human administrator yet.
		}
		normalized, err := NormalizePerson(login)
		if err != nil || normalized != login || strings.HasPrefix(login, "tag:") {
			return CodedError{Code: "portal_identity_invalid", Message: "portal owner and admins must be canonical Tailscale login identities"}
		}
	}
	return nil
}

// GatewayEnabled is the effective mode, including the persistent off switch.
func GatewayEnabled(reg *Registry) bool {
	return reg != nil && reg.Portal != nil && reg.Portal.Enabled && reg.Portal.Gateway != nil && reg.Portal.Gateway.Enabled
}

// initializeGateway runs only under the registry writer lock. Existing empty
// registries and recovery directories are installations, not first use.
func initializeGateway(path string, reg *Registry, priorInstall func() (bool, error)) (bool, error) {
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return false, err
	}
	for _, dir := range []string{filepath.Join(filepath.Dir(path), "nodes"), filepath.Join(filepath.Dir(path), "portal-nodes"), config.NodeIdentitiesDirIn(filepath.Dir(path))} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
		if len(entries) != 0 {
			return false, nil
		}
	}
	for _, name := range []string{"runtime.json", "tslink.pid", "node-ownership.json"} {
		if _, err := os.Lstat(filepath.Join(filepath.Dir(path), name)); !os.IsNotExist(err) {
			return false, err
		}
	}
	if priorInstall != nil {
		present, err := priorInstall()
		if err != nil || present {
			return false, err
		}
	}
	reg.Portal = &PortalConfig{Enabled: true, Hostname: DefaultPortalHostname,
		Gateway: &GatewayConfig{Enabled: true, Ports: DefaultGatewayPorts()}}
	return true, nil
}

// InitializeGateway initializes a genuinely new installation at startup.
// Read-only loaders deliberately never call it.
func InitializeGateway(ctx context.Context, path string, priorInstall func() (bool, error)) error {
	return withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		changed, err := initializeGateway(path, reg, priorInstall)
		if err != nil || !changed {
			return err
		}
		return save(path, reg)
	})
}

// BootstrapPortalOwner accepts only a caller-verified self identity and never
// overwrites an explicit owner or binds an identity to a replacement node.
func BootstrapPortalOwner(ctx context.Context, path, hostname, login string) error {
	canonical, err := NormalizePerson(login)
	if err != nil || strings.HasPrefix(canonical, "tag:") {
		return nil
	}
	return withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if !GatewayEnabled(reg) || reg.Portal.Hostname != hostname || reg.Portal.Owner != "" {
			return nil
		}
		reg.Portal.Owner = canonical
		return save(path, reg)
	})
}

func defaultGatewayRoute(reg *Registry, svc *Service) error {
	if !GatewayEnabled(reg) || svc.Funnel || svc.GuestGate || !PeopleServiceSupported(*svc) {
		return nil
	}
	if svc.PortalRoute == nil {
		port, err := AllocateGatewayPort(reg, svc.Name, 0)
		if err != nil {
			return err
		}
		svc.PortalRoute = &PortalRoute{Port: port}
	}
	svc.PeopleScoped = true
	return nil
}

// SetPortal serializes with all registry writers and preserves app and people
// state. Disabling preserves identities and hostname for a later enable.
func SetPortal(path string, p *PortalConfig) error {
	return SetPortalAuthorized(path, p, nil)
}

// SetPortalAuthorized checks the current authority under the same lock as the
// update. Local owner processes can use SetPortal without a remote check.
func SetPortalAuthorized(path string, p *PortalConfig, authorize func(*Registry) error, contexts ...context.Context) error {
	return withLockContext(optionalMutationContext(contexts), path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		before := cloneAccessRegistry(reg)
		if authorize != nil {
			if err := authorize(reg); err != nil {
				return err
			}
		}
		if err := ValidatePortal(p); err != nil {
			return err
		}
		if p != nil {
			for _, svc := range reg.Services {
				if svc.Name == p.Hostname {
					return portalConflict(p.Hostname)
				}
			}
		}
		reg.Portal = p
		if p != nil && p.Owner != "" && (before.Portal == nil || before.Portal.Owner == "") {
			if err := preserveAccessExcept(before, reg, "", nil, time.Now()); err != nil {
				return err
			}
		}
		return save(path, reg)
	})
}

func DisablePortal(path string) error {
	return DisablePortalContext(context.Background(), path)
}

func DisablePortalContext(ctx context.Context, path string) error {
	return DisablePortalAuthorizedContext(ctx, path, nil)
}

// DisablePortalAuthorizedContext checks authority against the current portal
// while holding the registry write lock.
func DisablePortalAuthorizedContext(ctx context.Context, path string, authorize func(*Registry) error) error {
	return withLockContext(ctx, path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		if reg.Portal == nil || !reg.Portal.Enabled {
			return nil
		}
		if authorize != nil {
			if err := authorize(reg); err != nil {
				return err
			}
		}
		reg.Portal.Enabled = false
		return save(path, reg)
	})
}
