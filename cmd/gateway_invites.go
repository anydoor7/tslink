package cmd

import (
	"path/filepath"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

const gatewayInviteKey = "@gateway"

func inviteTargetKey(target tailapi.DeviceTarget) string {
	if target.Kind == registry.InviteTargetGateway {
		return gatewayInviteKey
	}
	return target.Service
}
func personInviteKey(op registry.PersonInvite) string { return inviteTargetKey(personInviteTarget(op)) }
func inviteLabel(inv PeopleInviteView) string {
	if inv.TargetKind == registry.InviteTargetGateway {
		return "Gateway"
	}
	return inv.App
}

func gatewayInviteProof(path string, target tailapi.DeviceTarget) error {
	if target.Kind != registry.InviteTargetGateway {
		return nil
	}
	if target.Service != "" || target.NodeID == "" || registry.ValidateName(target.Hostname) != nil {
		return registry.CodedError{Code: registry.CodeInviteOwnershipUnproven, Message: "Gateway invitation requires exact owned node evidence"}
	}
	ledger, err := tsruntime.LoadOwnership(filepath.Join(filepath.Dir(path), "portal-nodes", target.Hostname, "ownership.json"))
	if err == nil {
		for _, node := range ledger.Nodes {
			if node.ServiceName == "gateway" && node.NodeID == target.NodeID && node.RetiredAt == nil {
				return nil
			}
		}
	}
	return registry.CodedError{Code: registry.CodeInviteOwnershipUnproven, Message: "Gateway ownership ledger does not prove the invitation target", Next: []string{"tslink doctor --json"}}
}

func gatewayInviteTarget(path string, portal tsruntime.PortalState) tailapi.DeviceTarget {
	target := tailapi.DeviceTarget{Kind: registry.InviteTargetGateway, Hostname: portal.Hostname}
	if portal.State == "running" && portal.Gateway != nil && portal.Gateway.Enabled && portal.Gateway.Persistent && portal.Gateway.OwnershipProven {
		target.NodeID = portal.Gateway.NodeID
		if gatewayInviteProof(path, target) != nil {
			target.NodeID = ""
		}
	}
	return target
}

// Capture runtime identity before grant writes invalidate its fingerprint.
// Per-app targets retain their old path for non-routed apps.
func gatewayPeopleTargets(reg *registry.Registry, portal tsruntime.PortalState, path string, targets map[string]tailapi.DeviceTarget) {
	if reg.Portal == nil || !reg.Portal.Enabled || reg.Portal.Gateway == nil || !reg.Portal.Gateway.Enabled {
		return
	}
	target := gatewayInviteTarget(path, portal)
	for _, svc := range reg.Services {
		if svc.PortalRoute != nil {
			targets[svc.Name] = target
		}
	}
}
