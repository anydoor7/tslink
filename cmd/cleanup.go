package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/lifecycle"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var cleanupReconcileFn = lifecycle.Reconcile
var cleanupNowFn = time.Now
var cleanupFindExactDeviceNodeIDFn = tailapi.FindExactDeviceNodeID
var cleanupAdoptOwnedNodeFn = tsruntime.AdoptOwnedNode
var cleanupPreviewAdoptOwnedNodeFn = tsruntime.PreviewAdoptOwnedNode

type cleanupAdoptionErrorData struct {
	Matches int `json:"matches"`
}

func init() {
	cleanupCmd := &cobra.Command{
		Use:   "cleanup",
		Args:  cobra.NoArgs,
		Short: "Reconcile expired Funnel exposure and TSLink-owned resources",
		Long: `Reconcile Funnel expiration, orphan TSLink-owned tailnet devices, and
optionally the unused shared Funnel ACL grant.

The command defaults to dry-run because it is an explicit one-shot remote
maintenance entry point. Use --dry-run=false to apply. Device deletion is
authorized only by a durable exact NodeID previously observed from that
service's tsnet node; hostname matches alone are always protected. Remote ACL
removal additionally requires --manage-acl and retains the canonical-grant and
ETag guards of the normal tag deletion path.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			adopt, _ := cmd.Flags().GetString("adopt")
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			now := cleanupNowFn()
			if adopt != "" {
				if err := registry.ValidateName(adopt); err != nil {
					return err
				}
				if !force {
					return output.ErrUsage("--adopt requires --force")
				}
			} else if force {
				return output.ErrUsage("--force is supported only with --adopt <hostname>")
			}
			if err := ensureDirFn(); err != nil {
				return err
			}
			regPath, err := config.RegistryPath()
			if err != nil {
				return err
			}
			ownershipPath, err := config.NodeOwnershipPath()
			if err != nil {
				return err
			}
			adoptionMatches := 0
			adoptionWarning := ""
			var ownershipOverride *tsruntime.OwnershipLedger
			if adopt != "" {
				nodeID, matches, err := cleanupFindExactDeviceNodeIDFn(cmd.Context(), adopt)
				if err != nil {
					return err
				}
				if matches != 1 {
					return output.ErrConflictWithData(
						fmt.Sprintf("--adopt requires exactly one literal hostname match; matched %d", matches),
						cleanupAdoptionErrorData{Matches: matches},
					)
				}
				adoptionMatches = matches
				reg, registryState, err := registry.LoadWithFileState(regPath)
				if err != nil {
					return err
				}
				retireAdoption := false
				if registryState == registry.RegistryFileValid {
					retireAdoption = true
					for _, svc := range reg.Services {
						if svc.Name == adopt {
							retireAdoption = false
							break
						}
					}
				} else {
					adoptionWarning = fmt.Sprintf("registry.json is %s and cannot be trusted for service membership; retired_at was not set on the adopted ownership proof; repair registry.json before deciding whether the service is retired", registryState)
				}
				if dryRun {
					preview, err := cleanupPreviewAdoptOwnedNodeFn(ownershipPath, adopt, nodeID, now, retireAdoption)
					if err != nil {
						return err
					}
					ownershipOverride = &preview
				} else {
					if err := cleanupAdoptOwnedNodeFn(ownershipPath, adopt, nodeID, now, retireAdoption); err != nil {
						return err
					}
				}
			}
			manageACL, _ := cmd.Flags().GetBool("manage-acl")
			result, err := cleanupReconcileFn(cmd.Context(), lifecycle.Options{
				RegistryPath:      regPath,
				OwnershipPath:     ownershipPath,
				OwnershipOverride: ownershipOverride,
				Now:               now,
				DryRun:            dryRun,
				ManageACL:         manageACL,
				CheckUnusedACL:    true,
			})
			if err != nil {
				// The ownership proof was already written under an untrusted
				// registry; that fact must not disappear just because the
				// reconcile that followed it failed.
				if adoptionWarning != "" {
					return fmt.Errorf("%w; %s", err, adoptionWarning)
				}
				return err
			}
			if adoptionWarning != "" {
				result.Warnings = append(result.Warnings, adoptionWarning)
			}
			if adopt != "" {
				result.Adoption = &lifecycle.AdoptionResult{ServiceName: adopt, Matches: adoptionMatches, Written: !dryRun}
				if !dryRun {
					result.DevicesAdopted = []string{adopt}
				}
			}
			if jsonOutput(cmd) {
				output.Success("cleanup", result)
				return nil
			}
			mode := "dry-run"
			if !dryRun {
				mode = "applied"
			}
			if result.Adoption != nil {
				if result.Adoption.Written {
					fmt.Fprintf(cmd.OutOrStdout(), "→ adopted exact TSLink-tagged hostname match into ownership ledger: %s (matches=%d)\n", result.Adoption.ServiceName, result.Adoption.Matches)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "→ adoption preview (not written): service=%s matches=%d\n", result.Adoption.ServiceName, result.Adoption.Matches)
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "→ cleanup %s: expired_funnels=%d devices_deleted=%d devices_protected=%d acl=%s\n",
				mode, len(result.ExpiredFunnels), len(result.DevicesDeleted), len(result.DevicesProtected), result.ACLAction)
			if len(result.DevicesWouldDelete) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "→ would delete owned devices: %s\n", strings.Join(result.DevicesWouldDelete, ", "))
			}
			for _, warning := range result.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "→ warning: %s\n", warning)
			}
			return nil
		},
	}
	cleanupCmd.Flags().Bool("dry-run", true, "Preview reconciliation without registry, device, or ACL deletion (set --dry-run=false to apply)")
	cleanupCmd.Flags().Bool("manage-acl", false, "Opt in to removing the unused canonical Funnel tag owner and nodeAttrs grant")
	cleanupCmd.Flags().String("adopt", "", "Preview or record exact ownership for one literal TSLink-tagged legacy device hostname (writing requires --dry-run=false)")
	cleanupCmd.Flags().Bool("force", false, "Confirm the explicitly named --adopt migration")
	rootCmd.AddCommand(cleanupCmd)
}
