package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

// Result structs for JSON output.

type TagsListResult struct {
	Services []TagsServiceEntry `json:"services"`
}

type TagsServiceEntry struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type TagsPullResult struct {
	Tags       []string `json:"tags"`
	DefaultTag string   `json:"default_tag"`
	Skipped    bool     `json:"skipped,omitempty"`
	SkipReason string   `json:"skip_reason,omitempty"`
}

type TagsAddResult struct {
	Service        string `json:"service"`
	Tag            string `json:"tag"`
	AlreadyExisted bool   `json:"already_existed"`
}

type TagsSetResult struct {
	Service string   `json:"service"`
	Tags    []string `json:"tags"`
}

type TagsSetDefaultResult struct {
	Tag string `json:"tag"`
}

type TagsDeleteResult struct {
	Tag                          string                        `json:"tag"`
	RemoteACLTagOwnerRuleRemoved bool                          `json:"remote_acl_tag_owner_rule_removed"`
	Message                      string                        `json:"message"`
	RemoteSideEffectPlan         security.RemoteSideEffectPlan `json:"remote_side_effect_plan"`
}

const tagsRemoteAPITokenMessage = "remote tag deletion requires a Tailscale API access token; configure one with `tslink login --api-key ...`"

// Testable function variables for tags commands.
var (
	tagsReadTagsFn                                                   = tailapi.ReadTags
	tagsDeleteTagFn                                                  = tailapi.DeleteTag
	tagsRegistryPathFn                                               = config.RegistryPath
	tagsLoadRegistryFn                                               = registry.Load
	tagsAddRegistryFn   func(string, registry.Service) (bool, error) = registry.Add
	tagsMutateServiceFn                                              = registry.MutateService
	tagsEnsureDirFn                                                  = config.EnsureDir
	tagsLoadGlobalFn                                                 = config.LoadGlobalConfig
	tagsSaveGlobalFn                                                 = config.SaveGlobalConfig
	tagsGetDefaultFn                                                 = config.GetDefaultTag
)

func validateTagPrefix(tag string) error {
	return registry.ValidateTag(tag)
}

func findService(reg *registry.Registry, name string) (int, error) {
	for i, svc := range reg.Services {
		if svc.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("service not found: %s", name)
}

func isServiceNotFound(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "service not found:")
}

// tagsListRun lists all services and their tags.
func tagsListRun(out io.Writer, isJSON bool) error {
	regPath, err := tagsRegistryPathFn()
	if err != nil {
		return err
	}
	reg, err := tagsLoadRegistryFn(regPath)
	if err != nil {
		return err
	}
	if isJSON {
		entries := make([]TagsServiceEntry, len(reg.Services))
		for i, svc := range reg.Services {
			entries[i] = TagsServiceEntry{Name: svc.Name, Tags: svc.Tags}
		}
		output.Success("tags list", TagsListResult{Services: entries})
		return nil
	}
	if len(reg.Services) == 0 {
		fmt.Fprintln(out, "No services registered")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tTAGS")
	for _, svc := range reg.Services {
		tags := strings.Join(svc.Tags, ", ")
		if tags == "" {
			tags = "(none)"
		}
		fmt.Fprintf(w, "%s\t%s\n", svc.Name, tags)
	}
	return w.Flush()
}

// tagsPullRun fetches and displays remote ACL tags.
func tagsPullRun(ctx context.Context, out io.Writer, isJSON bool) error {
	tags, err := tagsReadTagsFn(ctx)
	if err != nil {
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			defaultTag := tagsGetDefaultFn()
			if isJSON {
				output.Success("tags pull", TagsPullResult{
					Tags:       nil,
					DefaultTag: defaultTag,
					Skipped:    true,
					SkipReason: err.Error(),
				})
				return nil
			}
			fmt.Fprintf(out, "Skipped remote tag read: %s. Configure an API access token with `tslink login --api-key ...` to read tailnet ACL tags.\n", err)
			return nil
		}
		return err
	}
	defaultTag := tagsGetDefaultFn()
	if isJSON {
		output.Success("tags pull", TagsPullResult{Tags: tags, DefaultTag: defaultTag})
		return nil
	}
	if len(tags) == 0 {
		fmt.Fprintln(out, "No tags found in tailnet ACL")
		return nil
	}
	fmt.Fprintln(out, "REMOTE TAGS (tailnet ACL)")
	for _, tag := range tags {
		if tag == defaultTag {
			fmt.Fprintf(out, "  %s\t(default)\n", tag)
		} else {
			fmt.Fprintf(out, "  %s\n", tag)
		}
	}
	return nil
}

// tagsAddRun appends a tag to a service (dedup).
func tagsAddRun(out io.Writer, serviceName, tag string, isJSON bool) error {
	if err := validateTagPrefix(tag); err != nil {
		return err
	}
	if err := tagsEnsureDirFn(); err != nil {
		return err
	}
	regPath, err := tagsRegistryPathFn()
	if err != nil {
		return err
	}
	alreadyExisted := false
	if _, err := tagsMutateServiceFn(regPath, serviceName, func(svc registry.Service) (registry.Service, error) {
		for _, t := range svc.Tags {
			if t == tag {
				alreadyExisted = true
				return svc, nil
			}
		}
		svc.Tags = append(svc.Tags, tag)
		return svc, nil
	}); err != nil {
		if isJSON && isServiceNotFound(err) {
			return output.ErrNotFound(err.Error())
		}
		return err
	}
	if alreadyExisted {
		if isJSON {
			output.Success("tags add", TagsAddResult{Service: serviceName, Tag: tag, AlreadyExisted: true})
			return nil
		}
		fmt.Fprintf(out, "→ %s already on %s\n", tag, serviceName)
		return nil
	}
	if isJSON {
		output.Success("tags add", TagsAddResult{Service: serviceName, Tag: tag, AlreadyExisted: false})
		return nil
	}
	fmt.Fprintf(out, "→ Added %s to %s\n", tag, serviceName)
	return nil
}

// tagsSetRun replaces a service's tags with a single tag.
func tagsSetRun(out io.Writer, serviceName, tag string, isJSON bool) error {
	if err := validateTagPrefix(tag); err != nil {
		return err
	}
	if err := tagsEnsureDirFn(); err != nil {
		return err
	}
	regPath, err := tagsRegistryPathFn()
	if err != nil {
		return err
	}
	if _, err := tagsMutateServiceFn(regPath, serviceName, func(svc registry.Service) (registry.Service, error) {
		svc.Tags = []string{tag}
		return svc, nil
	}); err != nil {
		if isJSON && isServiceNotFound(err) {
			return output.ErrNotFound(err.Error())
		}
		return err
	}
	if isJSON {
		output.Success("tags set", TagsSetResult{Service: serviceName, Tags: []string{tag}})
		return nil
	}
	fmt.Fprintf(out, "→ Set %s tags to [%s]\n", serviceName, tag)
	return nil
}

// tagsSetDefaultRun sets the global default tag.
func tagsSetDefaultRun(out io.Writer, tag string, isJSON bool) error {
	if err := validateTagPrefix(tag); err != nil {
		return err
	}
	cfg, err := tagsLoadGlobalFn()
	if err != nil {
		return err
	}
	cfg.DefaultTag = tag
	if err := tagsSaveGlobalFn(cfg); err != nil {
		return err
	}
	if isJSON {
		output.Success("tags set-default", TagsSetDefaultResult{Tag: tag})
		return nil
	}
	fmt.Fprintf(out, "→ Default tag set to %s\n", tag)
	return nil
}

// tagsDeleteRemoteRun deletes a tag from the tailnet ACL after safety checks.
func tagsDeleteRemoteRun(ctx context.Context, out io.Writer, tag string, force, manageACL bool, isJSON bool) error {
	if err := validateTagPrefix(tag); err != nil {
		return err
	}
	plan := security.ACLMutationPlan("delete_tag_owner", []string{tag}, manageACL)
	// Check if tag is the current default
	defaultTag := tagsGetDefaultFn()
	if tag == defaultTag {
		msg := fmt.Sprintf("cannot delete default tag %q — change the default first with: tslink tags set-default <other-tag>", tag)
		if isJSON {
			return output.ErrConflict(msg)
		}
		return fmt.Errorf("%s", msg)
	}
	// Check local registry for services using this tag
	regPath, err := tagsRegistryPathFn()
	if err != nil {
		return err
	}
	reg, err := tagsLoadRegistryFn(regPath)
	if err != nil {
		return err
	}
	var usedBy []string
	for _, svc := range reg.Services {
		for _, t := range svc.Tags {
			if t == tag {
				usedBy = append(usedBy, svc.Name)
				break
			}
		}
	}
	if len(usedBy) > 0 {
		msg := fmt.Sprintf("cannot delete %q — in use by services: %s", tag, strings.Join(usedBy, ", "))
		if isJSON {
			return output.ErrConflict(msg)
		}
		return fmt.Errorf("%s", msg)
	}
	forceMsg := fmt.Sprintf("refusing to delete %q from the tailnet ACL without --force; this removes the ACL tag owner rule globally", tag)
	if !force {
		if isJSON {
			return output.ErrConflict(forceMsg)
		}
		return fmt.Errorf("%s", forceMsg)
	}
	if !manageACL {
		msg := fmt.Sprintf("refusing to delete %q from the tailnet ACL without --manage-acl; remote ACL mutation is disabled by default; remote_side_effect_plan=%s", tag, compactJSON(plan))
		if isJSON {
			return output.ErrConflict(msg)
		}
		return fmt.Errorf("%s", msg)
	}
	if err := tagsDeleteTagFn(ctx, tag); err != nil {
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			if isJSON {
				return output.ErrAuth(tagsRemoteAPITokenMessage)
			}
			return fmt.Errorf("%s", tagsRemoteAPITokenMessage)
		}
		return err
	}
	message := fmt.Sprintf("deleted %s from the tailnet ACL; the ACL tag owner rule was removed globally", tag)
	if isJSON {
		output.Success("tags delete-remote", TagsDeleteResult{
			Tag:                          tag,
			RemoteACLTagOwnerRuleRemoved: true,
			Message:                      message,
			RemoteSideEffectPlan:         plan,
		})
		return nil
	}
	fmt.Fprintf(out, "→ Deleted %s from tailnet ACL; ACL tag owner rule removed globally\n", tag)
	return nil
}

func init() {
	tagsCmd := &cobra.Command{
		Use:   "tags",
		Short: "Manage service ACL tags",
		Long: `Manage ACL tags for registered services and the tailnet.

Subcommands:
  list            Show tags for all registered services
  pull            Fetch remote tags from tailnet ACL
  add             Add a tag to a service
  set             Replace a service's tags
  set-default     Change the default tag for new services
  delete-remote   Remove an ACL tag owner rule globally after local safety checks and --manage-acl

Examples:
  tslink tags list
  tslink tags pull
  tslink tags add myapp tag:shared
  tslink tags set myapp tag:web
  tslink tags set-default tag:myteam
  tslink tags delete-remote tag:old --force --manage-acl`,
	}

	tagsListCmd := &cobra.Command{
		Use:   "list",
		Short: "Show tags for all registered services",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsListRun(cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	tagsPullCmd := &cobra.Command{
		Use:   "pull",
		Short: "Fetch remote tags from tailnet ACL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsPullRun(cmd.Context(), cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	tagsAddCmd := &cobra.Command{
		Use:   "add <service> <tag>",
		Short: "Add a tag to a service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsAddRun(cmd.OutOrStdout(), args[0], args[1], jsonOutput(cmd))
		},
	}

	tagsSetCmd := &cobra.Command{
		Use:   "set <service> <tag>",
		Short: "Replace a service's tags with a single tag",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsSetRun(cmd.OutOrStdout(), args[0], args[1], jsonOutput(cmd))
		},
	}

	tagsSetDefaultCmd := &cobra.Command{
		Use:   "set-default <tag>",
		Short: "Set the default tag for new services",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsSetDefaultRun(cmd.OutOrStdout(), args[0], jsonOutput(cmd))
		},
	}

	tagsDeleteRemoteCmd := &cobra.Command{
		Use:   "delete-remote <tag> --force --manage-acl",
		Short: "Remove an ACL tag owner rule globally after local safety checks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			manageACL, _ := cmd.Flags().GetBool("manage-acl")
			return tagsDeleteRemoteRun(cmd.Context(), cmd.OutOrStdout(), args[0], force, manageACL, jsonOutput(cmd))
		},
	}
	tagsDeleteRemoteCmd.Flags().Bool("force", false, "Delete the ACL tag owner rule globally after local safety checks")
	tagsDeleteRemoteCmd.Flags().Bool("manage-acl", false, "Opt in to remote Tailscale ACL tag-owner mutation using a machine-readable side-effect plan")

	tagsCmd.AddCommand(tagsListCmd, tagsPullCmd, tagsAddCmd, tagsSetCmd, tagsSetDefaultCmd, tagsDeleteRemoteCmd)
	rootCmd.AddCommand(tagsCmd)
}

func compactJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(data)
}
