package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var (
	inviteRegistryPathFn        = config.RegistryPath
	invitePIDPathFn             = config.PIDPath
	inviteRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	inviteLoadRegistryFn        = registry.Load
	inviteLoadSnapshotFn        = tsruntime.Load
	inviteIsRunningFn           = daemon.IsRunning
	inviteReadPIDFn             = daemon.ReadPID
	invitePIDModTimeFn          = func(path string) (time.Time, error) {
		info, err := os.Stat(path)
		if err != nil {
			return time.Time{}, err
		}
		return info.ModTime(), nil
	}
	inviteCreateUserFn   = tailapi.CreateUserInvite
	inviteCreateDeviceFn = tailapi.CreateDeviceInvite
	inviteListFn         = tailapi.ListInvites
	inviteRevokeFn       = tailapi.RevokeInvite
	inviteResendFn       = tailapi.ResendInvite
)

type InviteMutationResult struct {
	tailapi.Invite
	RemoteSideEffectPlan security.RemoteSideEffectPlan `json:"remote_side_effect_plan"`
}

type InviteResendResult struct {
	Kind                 string                        `json:"kind"`
	ID                   string                        `json:"id"`
	Recipient            string                        `json:"recipient,omitempty"`
	Email                string                        `json:"email"`
	Emailed              bool                          `json:"emailed"`
	Service              string                        `json:"service,omitempty"`
	RemoteSideEffectPlan security.RemoteSideEffectPlan `json:"remote_side_effect_plan"`
}

type InviteRevokeResult struct {
	Kind                 string                        `json:"kind"`
	ID                   string                        `json:"id"`
	Service              string                        `json:"service,omitempty"`
	Revoked              bool                          `json:"revoked"`
	RemoteSideEffectPlan security.RemoteSideEffectPlan `json:"remote_side_effect_plan"`
}

func invitePaths() (regPath, pidPath, snapshotPath string, err error) {
	regPath, err = inviteRegistryPathFn()
	if err != nil {
		return "", "", "", err
	}
	pidPath, err = invitePIDPathFn()
	if err != nil {
		return "", "", "", err
	}
	snapshotPath, err = inviteRuntimeSnapshotPathFn()
	if err != nil {
		return "", "", "", err
	}
	return regPath, pidPath, snapshotPath, nil
}

func inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath string) ([]tailapi.DeviceTarget, error) {
	reg, err := inviteLoadRegistryFn(regPath)
	if err != nil {
		return nil, err
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		return nil, err
	}

	nodeIDs := make(map[string]string)
	snapshot, loadErr := inviteLoadSnapshotFn(snapshotPath)
	expected := tsruntime.ExpectedRuntime{CurrentRegistryFingerprint: fingerprint}
	if inviteIsRunningFn(pidPath) {
		if pid, pidErr := inviteReadPIDFn(pidPath); pidErr == nil {
			expected.DaemonPID = pid
		}
		if lowerBound, modErr := invitePIDModTimeFn(pidPath); modErr == nil {
			expected.DaemonStartedAtLowerBound = lowerBound
		}
	}
	freshness := tsruntime.Classify(snapshot, loadErr, expected)
	if freshness.Exact && snapshot != nil {
		for _, service := range snapshot.Services {
			if service.RuntimeState == tsruntime.ServiceRuntimeRunning {
				nodeIDs[service.Name] = service.NodeID
			}
		}
	}

	targets := make([]tailapi.DeviceTarget, 0, len(reg.Services))
	for _, service := range reg.Services {
		targets = append(targets, tailapi.DeviceTarget{
			Service:  service.Name,
			Hostname: service.Name,
			NodeID:   nodeIDs[service.Name],
		})
	}
	return targets, nil
}

func inviteDeviceTargetsForListPaths(regPath, pidPath, snapshotPath string) ([]tailapi.DeviceTarget, error) {
	registryData, err := os.ReadFile(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, output.ErrConflict("registry.json is missing; invite completeness cannot be established")
		}
		return nil, fmt.Errorf("read registry.json for invite completeness: %w", err)
	}
	if strings.TrimSpace(string(registryData)) == "" {
		return nil, output.ErrConflict("registry.json is empty; invite completeness cannot be established")
	}
	return inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
}

func inviteDeviceTargetForService(regPath, pidPath, snapshotPath, name string) (tailapi.DeviceTarget, error) {
	targets, err := inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		return tailapi.DeviceTarget{}, err
	}
	for _, target := range targets {
		if target.Service == name {
			return target, nil
		}
	}
	return tailapi.DeviceTarget{}, output.ErrNotFound(fmt.Sprintf("service not found: %s", name))
}

func invitePlan(invite tailapi.Invite, operation string) security.RemoteSideEffectPlan {
	return security.InviteMutationPlan(invite.Kind, operation, invite.ID, invite.Service, invite.DeviceID)
}

func writeInviteCreate(out io.Writer, command string, invite tailapi.Invite, isJSON bool) error {
	data := InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}
	if isJSON {
		output.WriteJSON(out, output.NewSuccess(command, data))
		return nil
	}
	if invite.Emailed {
		fmt.Fprintf(out, "→ Created %s invite %s and asked Tailscale to email %s\n", invite.Kind, invite.ID, invite.Recipient)
		return nil
	}
	fmt.Fprintln(out, invite.InviteURL)
	return nil
}

func inviteUserRun(ctx context.Context, out io.Writer, email, role string, printLink, isJSON bool) error {
	invite, err := inviteCreateUserFn(ctx, email, role, printLink)
	if err != nil {
		return err
	}
	return writeInviteCreate(out, "invite user", invite, isJSON)
}

func inviteDeviceRun(ctx context.Context, out io.Writer, service, email string, printLink, multiUse, allowExitNode, isJSON bool) error {
	regPath, pidPath, snapshotPath, err := invitePaths()
	if err != nil {
		return err
	}
	target, err := inviteDeviceTargetForService(regPath, pidPath, snapshotPath, service)
	if err != nil {
		return err
	}
	invite, err := inviteCreateDeviceFn(ctx, target, email, printLink, multiUse, allowExitNode)
	if err != nil {
		return err
	}
	return writeInviteCreate(out, "invite device", invite, isJSON)
}

func inviteListForOutput(result tailapi.InviteList, showURLs bool) tailapi.InviteList {
	result.UserInvites = append([]tailapi.Invite{}, result.UserInvites...)
	result.DeviceInvites = append([]tailapi.Invite{}, result.DeviceInvites...)
	result.DeviceTargets = append([]tailapi.InviteTargetStatus{}, result.DeviceTargets...)
	if showURLs {
		return result
	}
	for i := range result.UserInvites {
		result.UserInvites[i].InviteURL = ""
	}
	for i := range result.DeviceInvites {
		result.DeviceInvites[i].InviteURL = ""
	}
	return result
}

func writeInviteTargetErrors(out io.Writer, result tailapi.InviteList) {
	for _, target := range result.DeviceTargets {
		if target.Error != nil {
			fmt.Fprintf(out, "! Device invite check incomplete for %s [%s]: %s\n", target.Service, target.Error.Code, target.Error.Message)
			continue
		}
		if !target.Checked {
			fmt.Fprintf(out, "! Device invite check incomplete for %s: target was not checked\n", target.Service)
		}
	}
}

func inviteListRun(ctx context.Context, out io.Writer, showURLs, isJSON bool) error {
	regPath, pidPath, snapshotPath, err := invitePaths()
	if err != nil {
		return err
	}
	targets, err := inviteDeviceTargetsForListPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		return err
	}
	result, err := inviteListFn(ctx, targets)
	if err != nil {
		return err
	}
	result = inviteListForOutput(result, showURLs)
	if isJSON {
		output.WriteJSON(out, output.NewSuccess("invite list", result))
		return nil
	}
	if result.Count == 0 {
		if !result.Complete {
			fmt.Fprintln(out, "No open user or verified device invites; some device checks were incomplete")
			writeInviteTargetErrors(out, result)
			return nil
		}
		fmt.Fprintln(out, "No open invites")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if showURLs {
		fmt.Fprintln(w, "KIND\tID\tSERVICE\tRECIPIENT\tEMAILED\tINVITE URL")
	} else {
		fmt.Fprintln(w, "KIND\tID\tSERVICE\tRECIPIENT\tEMAILED")
	}
	entries := append(append([]tailapi.Invite(nil), result.UserInvites...), result.DeviceInvites...)
	for _, invite := range entries {
		service := invite.Service
		if service == "" {
			service = "-"
		}
		recipient := invite.Email
		if recipient == "" {
			recipient = "-"
		}
		if showURLs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\t%s\n", invite.Kind, invite.ID, service, recipient, invite.Emailed, invite.InviteURL)
		} else {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\n", invite.Kind, invite.ID, service, recipient, invite.Emailed)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if !result.Complete {
		writeInviteTargetErrors(out, result)
	}
	return nil
}

func inviteTargetsForMutation(kind string) ([]tailapi.DeviceTarget, error) {
	if kind == tailapi.InviteKindUser {
		return nil, nil
	}
	regPath, pidPath, snapshotPath, err := invitePaths()
	if err != nil {
		return nil, err
	}
	return inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
}

func inviteRevokeRun(ctx context.Context, out io.Writer, kind, id string, isJSON bool) error {
	if err := tailapi.ValidateInviteID(id); err != nil {
		return err
	}
	if err := tailapi.ValidateInviteKind(kind); err != nil {
		return err
	}
	targets, err := inviteTargetsForMutation(kind)
	if err != nil {
		return err
	}
	invite, err := inviteRevokeFn(ctx, kind, id, targets)
	if err != nil {
		return err
	}
	data := InviteRevokeResult{Kind: invite.Kind, ID: invite.ID, Service: invite.Service, Revoked: true, RemoteSideEffectPlan: invitePlan(invite, "revoke")}
	if isJSON {
		output.WriteJSON(out, output.NewSuccess("invite revoke", data))
		return nil
	}
	fmt.Fprintf(out, "→ Revoked %s invite %s\n", invite.Kind, invite.ID)
	return nil
}

func inviteResendResult(invite tailapi.Invite) InviteResendResult {
	return InviteResendResult{
		Kind:                 invite.Kind,
		ID:                   invite.ID,
		Recipient:            invite.Recipient,
		Email:                invite.Email,
		Emailed:              invite.Emailed,
		Service:              invite.Service,
		RemoteSideEffectPlan: invitePlan(invite, "resend"),
	}
}

func inviteResendRun(ctx context.Context, out io.Writer, kind, id string, isJSON bool) error {
	if err := tailapi.ValidateInviteID(id); err != nil {
		return err
	}
	if err := tailapi.ValidateInviteKind(kind); err != nil {
		return err
	}
	targets, err := inviteTargetsForMutation(kind)
	if err != nil {
		return err
	}
	invite, err := inviteResendFn(ctx, kind, id, targets)
	if err != nil {
		return err
	}
	data := inviteResendResult(invite)
	if isJSON {
		output.WriteJSON(out, output.NewSuccess("invite resend", data))
		return nil
	}
	fmt.Fprintf(out, "→ Resent %s invite %s to %s\n", invite.Kind, invite.ID, invite.Email)
	return nil
}

func (h *apiHandler) inviteTargets() ([]tailapi.DeviceTarget, error) {
	snapshotPath := h.runtimeSnapshotPath
	if snapshotPath == "" {
		var err error
		snapshotPath, err = inviteRuntimeSnapshotPathFn()
		if err != nil {
			return nil, err
		}
	}
	return inviteDeviceTargetsForPaths(h.regPath, h.pidPath, snapshotPath)
}

func (h *apiHandler) inviteListTargets() ([]tailapi.DeviceTarget, error) {
	snapshotPath := h.runtimeSnapshotPath
	if snapshotPath == "" {
		var err error
		snapshotPath, err = inviteRuntimeSnapshotPathFn()
		if err != nil {
			return nil, err
		}
	}
	return inviteDeviceTargetsForListPaths(h.regPath, h.pidPath, snapshotPath)
}

func (h *apiHandler) handleInviteUser(req APIRequest, out io.Writer) output.Result {
	if strings.TrimSpace(req.Email) == "" {
		return writeAPIError(out, apiActionInviteUser, output.ErrUsage("email is required"))
	}
	role := req.Role
	if role == "" {
		role = tailapi.InviteRoleMember
	}
	if err := tailapi.ValidateInviteRole(role); err != nil {
		return writeAPIError(out, apiActionInviteUser, err)
	}
	invite, err := inviteCreateUserFn(context.Background(), req.Email, role, req.PrintLink)
	if err != nil {
		return writeAPIError(out, apiActionInviteUser, err)
	}
	data := InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}
	return writeAPISuccess(out, apiActionInviteUser, data)
}

func (h *apiHandler) handleInviteDevice(req APIRequest, out io.Writer) output.Result {
	if strings.TrimSpace(req.Service) == "" {
		return writeAPIError(out, apiActionInviteDevice, output.ErrUsage("service is required"))
	}
	if strings.TrimSpace(req.Email) == "" {
		return writeAPIError(out, apiActionInviteDevice, output.ErrUsage("email is required"))
	}
	targets, err := h.inviteTargets()
	if err != nil {
		return writeAPIError(out, apiActionInviteDevice, err)
	}
	var target tailapi.DeviceTarget
	found := false
	for _, candidate := range targets {
		if candidate.Service == req.Service {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		return writeAPIError(out, apiActionInviteDevice, output.ErrNotFound(fmt.Sprintf("service not found: %s", req.Service)))
	}
	invite, err := inviteCreateDeviceFn(context.Background(), target, req.Email, req.PrintLink, req.MultiUse, req.AllowExitNode)
	if err != nil {
		return writeAPIError(out, apiActionInviteDevice, err)
	}
	data := InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}
	return writeAPISuccess(out, apiActionInviteDevice, data)
}

func (h *apiHandler) handleInviteList(req APIRequest, out io.Writer) output.Result {
	targets, err := h.inviteListTargets()
	if err != nil {
		return writeAPIError(out, apiActionInviteList, err)
	}
	result, err := inviteListFn(context.Background(), targets)
	if err != nil {
		return writeAPIError(out, apiActionInviteList, err)
	}
	result = inviteListForOutput(result, req.ShowURLs)
	return writeAPISuccess(out, apiActionInviteList, result)
}

func (h *apiHandler) handleInviteRevoke(req APIRequest, out io.Writer) output.Result {
	if err := tailapi.ValidateInviteID(req.InviteID); err != nil {
		return writeAPIError(out, apiActionInviteRevoke, err)
	}
	if err := tailapi.ValidateInviteKind(req.Kind); err != nil {
		return writeAPIError(out, apiActionInviteRevoke, err)
	}
	var targets []tailapi.DeviceTarget
	if req.Kind == tailapi.InviteKindDevice {
		var err error
		targets, err = h.inviteTargets()
		if err != nil {
			return writeAPIError(out, apiActionInviteRevoke, err)
		}
	}
	invite, err := inviteRevokeFn(context.Background(), req.Kind, req.InviteID, targets)
	if err != nil {
		return writeAPIError(out, apiActionInviteRevoke, err)
	}
	data := InviteRevokeResult{Kind: invite.Kind, ID: invite.ID, Service: invite.Service, Revoked: true, RemoteSideEffectPlan: invitePlan(invite, "revoke")}
	return writeAPISuccess(out, apiActionInviteRevoke, data)
}

func (h *apiHandler) handleInviteResend(req APIRequest, out io.Writer) output.Result {
	if err := tailapi.ValidateInviteID(req.InviteID); err != nil {
		return writeAPIError(out, apiActionInviteResend, err)
	}
	if err := tailapi.ValidateInviteKind(req.Kind); err != nil {
		return writeAPIError(out, apiActionInviteResend, err)
	}
	var targets []tailapi.DeviceTarget
	if req.Kind == tailapi.InviteKindDevice {
		var err error
		targets, err = h.inviteTargets()
		if err != nil {
			return writeAPIError(out, apiActionInviteResend, err)
		}
	}
	invite, err := inviteResendFn(context.Background(), req.Kind, req.InviteID, targets)
	if err != nil {
		return writeAPIError(out, apiActionInviteResend, err)
	}
	data := inviteResendResult(invite)
	return writeAPISuccess(out, apiActionInviteResend, data)
}

func init() {
	inviteCmd := &cobra.Command{
		Use:   "invite",
		Short: "Invite users to the tailnet or share TSLink-owned devices",
		Args:  cobra.NoArgs,
		RunE:  runCommandGroup,
	}

	var userRole string
	var userPrintLink bool
	userCmd := &cobra.Command{
		Use:   "user <email>",
		Short: "Invite a user to join the tailnet",
		Args:  cobra.ExactArgs(1),
		PreRunE: func(*cobra.Command, []string) error {
			return tailapi.ValidateInviteRole(userRole)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return inviteUserRun(cmd.Context(), cmd.OutOrStdout(), args[0], userRole, userPrintLink, jsonOutput(cmd))
		},
	}
	userCmd.Flags().StringVar(&userRole, "role", tailapi.InviteRoleMember, "Role assigned on acceptance: "+strings.Join(tailapi.InviteRoles(), ", "))
	userCmd.Flags().BoolVar(&userPrintLink, "print-link", false, "Do not send email; return the API-provided invite URL for self-delivery")

	var devicePrintLink, deviceMultiUse, deviceAllowExitNode bool
	deviceCmd := &cobra.Command{
		Use:   "device <service> <email>",
		Short: "Share a TSLink-owned service device with an external user",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return inviteDeviceRun(cmd.Context(), cmd.OutOrStdout(), args[0], args[1], devicePrintLink, deviceMultiUse, deviceAllowExitNode, jsonOutput(cmd))
		},
	}
	deviceCmd.Flags().BoolVar(&devicePrintLink, "print-link", false, "Do not send email; return the API-provided invite URL for self-delivery")
	deviceCmd.Flags().BoolVar(&deviceMultiUse, "multi-use", false, "Allow the device invite to be accepted more than once")
	deviceCmd.Flags().BoolVar(&deviceAllowExitNode, "allow-exit-node", false, "Allow the recipient to use the shared device as an exit node")

	var listShowURLs bool
	listCmd := &cobra.Command{Use: "list", Short: "List open user and TSLink-owned device invites", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return inviteListRun(cmd.Context(), cmd.OutOrStdout(), listShowURLs, jsonOutput(cmd))
	}}
	listCmd.Flags().BoolVar(&listShowURLs, "show-urls", false, "Include bearer invite URLs in human and JSON output")

	var revokeKind string
	revokeCmd := &cobra.Command{Use: "revoke <id>", Short: "Revoke a user or TSLink-owned device invite", Args: cobra.ExactArgs(1), PreRunE: func(_ *cobra.Command, args []string) error {
		if err := tailapi.ValidateInviteID(args[0]); err != nil {
			return err
		}
		return tailapi.ValidateInviteKind(revokeKind)
	}, RunE: func(cmd *cobra.Command, args []string) error {
		return inviteRevokeRun(cmd.Context(), cmd.OutOrStdout(), revokeKind, args[0], jsonOutput(cmd))
	}}
	revokeCmd.Flags().StringVar(&revokeKind, "kind", "", "Required invite namespace: user or device")

	var resendKind string
	resendCmd := &cobra.Command{Use: "resend <id>", Short: "Resend an emailed user or TSLink-owned device invite", Args: cobra.ExactArgs(1), PreRunE: func(_ *cobra.Command, args []string) error {
		if err := tailapi.ValidateInviteID(args[0]); err != nil {
			return err
		}
		return tailapi.ValidateInviteKind(resendKind)
	}, RunE: func(cmd *cobra.Command, args []string) error {
		return inviteResendRun(cmd.Context(), cmd.OutOrStdout(), resendKind, args[0], jsonOutput(cmd))
	}}
	resendCmd.Flags().StringVar(&resendKind, "kind", "", "Required invite namespace: user or device")

	inviteCmd.AddCommand(userCmd, deviceCmd, listCmd, revokeCmd, resendCmd)
	rootCmd.AddCommand(inviteCmd)
}
