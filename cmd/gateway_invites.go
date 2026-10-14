package cmd

import (
	"fmt"
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

func gatewayPeopleMessage(result PeopleResult, printLinks bool) string {
	text := fmt.Sprintf("Install Tailscale from https://tailscale.com/download and sign in as %s. Keep it connected. Accept the gateway invitation once; the owner can then change which routed apps you can use.\n", result.Person.Login)
	for _, inv := range result.Invites {
		label := inviteLabel(inv)
		if inv.Code != "" {
			text += label + ": invitation is not ready; ask the owner for help.\n"
		} else if printLinks && inv.InviteURL != "" {
			text += label + ": accept " + inv.InviteURL + "\n"
		} else if inv.State == registry.PersonInviteAccepted {
			text += label + ": invitation already accepted.\n"
		} else {
			text += label + ": ask the owner for the invitation link.\n"
		}
	}
	if result.Portal.URL != "" {
		text += "Open and bookmark " + result.Portal.URL + " for your apps and their current access deadlines."
	} else {
		text += "The gateway dashboard address is pending; ask the owner to check tslink status --urls."
	}
	for _, warning := range result.Warnings {
		text += "\n" + warning
	}
	return text
}
