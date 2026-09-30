package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
)

// elevatedInviteProbe replaces the Tailscale invite API with a recorder.
type elevatedInviteProbe struct {
	userRoles []string
	exitNode  []bool
}

func stubInviteAPI(t *testing.T) *elevatedInviteProbe {
	t.Helper()
	probe := &elevatedInviteProbe{}
	oldUser, oldDevice := inviteCreateUserFn, inviteCreateDeviceFn
	t.Cleanup(func() { inviteCreateUserFn, inviteCreateDeviceFn = oldUser, oldDevice })
	inviteCreateUserFn = func(_ context.Context, email, role string, printLink bool) (tailapi.Invite, error) {
		probe.userRoles = append(probe.userRoles, role)
		return tailapi.Invite{Kind: tailapi.InviteKindUser, ID: "10", Email: email, Role: role, Emailed: !printLink}, nil
	}
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, email string, printLink, _, allowExitNode bool) (tailapi.Invite, error) {
		probe.exitNode = append(probe.exitNode, allowExitNode)
		return tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "11", Email: email, Service: target.Service, Emailed: !printLink}, nil
	}
	return probe
}

// mcpInviteFixture gives the MCP actions a registered service and a
// config.json with the given contents, or none when config is empty.
func mcpInviteFixture(t *testing.T, config string) mcpActions {
	t.Helper()
	path := writeGlobalConfigFixture(t, config)
	if config == "" {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	return defaultMCPActions(paths, io.Discard)
}

func assertElevatedInviteRefused(t *testing.T, err error, cli string) {
	t.Helper()
	if code, _ := registry.ErrorCode(err); code != registry.CodeMCPElevatedInviteRefused {
		t.Fatalf("error = %v, want %s", err, registry.CodeMCPElevatedInviteRefused)
	}
	var recovery interface{ NextCommands() []string }
	if !errors.As(err, &recovery) {
		t.Fatalf("error %v carries no next steps", err)
	}
	next := strings.Join(recovery.NextCommands(), "\n")
	if !strings.Contains(next, cli) || !strings.Contains(next, "allow_elevated_invites") {
		t.Fatalf("next = %q, want the CLI command %q and the config opt-in", next, cli)
	}
}

// TestMCPElevatedInvitesNeedTheOwnersOptIn is A2-2: through MCP (stdio and
// the remote control plane), invite_user accepted admin, it-admin,
// network-admin, billing-admin and auditor, and invite_device accepted
// allow_exit_node, behind nothing but a sentence asking the model to confirm.
// Without mcp.allow_elevated_invites in config.json they are refused in code
// before any API call; with it they reach the API.
func TestMCPElevatedInvitesNeedTheOwnersOptIn(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		probe := stubInviteAPI(t)
		actions := mcpInviteFixture(t, `{"mcp":{"enabled":false}}`)
		for _, role := range tailapi.InviteRoles() {
			if role == tailapi.InviteRoleMember {
				continue
			}
			_, err := actions.inviteUser(context.Background(), "alice@example.com", role, false)
			assertElevatedInviteRefused(t, err, "tslink invite user alice@example.com --role "+role)
		}
		_, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "web", Email: "alice@example.com", AllowExitNode: true})
		assertElevatedInviteRefused(t, err, "tslink invite device web alice@example.com --allow-exit-node")
		if len(probe.userRoles) != 0 || len(probe.exitNode) != 0 {
			t.Fatalf("refused invitations reached the API: roles=%v exit_node=%v", probe.userRoles, probe.exitNode)
		}

		// Control: membership and a plain device share still go through.
		for _, role := range []string{"", tailapi.InviteRoleMember} {
			if _, err := actions.inviteUser(context.Background(), "alice@example.com", role, false); err != nil {
				t.Fatalf("member invite (role %q) refused: %v", role, err)
			}
		}
		if _, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "web", Email: "alice@example.com"}); err != nil {
			t.Fatalf("plain device invite refused: %v", err)
		}
		if strings.Join(probe.userRoles, ",") != "member,member" || len(probe.exitNode) != 1 || probe.exitNode[0] {
			t.Fatalf("API saw roles=%v exit_node=%v, want two member invites and one plain device share", probe.userRoles, probe.exitNode)
		}
	})

	t.Run("no config.json", func(t *testing.T) {
		probe := stubInviteAPI(t)
		actions := mcpInviteFixture(t, "")
		_, err := actions.inviteUser(context.Background(), "alice@example.com", tailapi.InviteRoleAdmin, false)
		assertElevatedInviteRefused(t, err, "--role admin")
		if len(probe.userRoles) != 0 {
			t.Fatalf("refused invitation reached the API: %v", probe.userRoles)
		}
	})

	t.Run("config.json unreadable", func(t *testing.T) {
		probe := stubInviteAPI(t)
		actions := mcpInviteFixture(t, `{"mcp":{"allow_elevated_invites":true},}`)
		_, err := actions.inviteUser(context.Background(), "alice@example.com", tailapi.InviteRoleAdmin, false)
		if code, _ := registry.ErrorCode(err); code != registry.CodeConfigLoadFailed || len(probe.userRoles) != 0 {
			t.Fatalf("error = %v, API roles = %v; want %s and no API call", err, probe.userRoles, registry.CodeConfigLoadFailed)
		}
	})

	t.Run("on", func(t *testing.T) {
		probe := stubInviteAPI(t)
		actions := mcpInviteFixture(t, `{"mcp":{"allow_elevated_invites":true}}`)
		if _, err := actions.inviteUser(context.Background(), "alice@example.com", tailapi.InviteRoleAdmin, false); err != nil {
			t.Fatalf("admin invite with the opt-in: %v", err)
		}
		if _, err := actions.inviteDevice(context.Background(), mcpInviteDeviceArguments{Service: "web", Email: "alice@example.com", AllowExitNode: true}); err != nil {
			t.Fatalf("exit-node device invite with the opt-in: %v", err)
		}
		if strings.Join(probe.userRoles, ",") != tailapi.InviteRoleAdmin || len(probe.exitNode) != 1 || !probe.exitNode[0] {
			t.Fatalf("API saw roles=%v exit_node=%v, want admin and an exit-node share", probe.userRoles, probe.exitNode)
		}
	})
}

// TestCLIElevatedInvitesNeedNoOptIn: the CLI, where a person types the
// command, keeps every role and the exit-node share without the MCP opt-in.
func TestCLIElevatedInvitesNeedNoOptIn(t *testing.T) {
	probe := stubInviteAPI(t)
	writeGlobalConfigFixture(t, `{"mcp":{"enabled":false}}`)
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := inviteUserRun(context.Background(), &out, "alice@example.com", tailapi.InviteRoleAdmin, false, false); err != nil {
		t.Fatalf("tslink invite user --role admin: %v", err)
	}
	if _, err := inviteDeviceCreate(context.Background(), paths.Registry, paths.PID, paths.Snapshot, "web", "alice@example.com", false, false, true); err != nil {
		t.Fatalf("tslink invite device --allow-exit-node: %v", err)
	}
	if strings.Join(probe.userRoles, ",") != tailapi.InviteRoleAdmin || len(probe.exitNode) != 1 || !probe.exitNode[0] {
		t.Fatalf("API saw roles=%v exit_node=%v, want admin and an exit-node share", probe.userRoles, probe.exitNode)
	}
}
