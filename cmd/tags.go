package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

// Testable function variables for tags commands.
var (
	tagsReadTagsFn     = tailapi.ReadTags
	tagsDeleteTagFn    = tailapi.DeleteTag
	tagsRegistryPathFn = config.RegistryPath
	tagsLoadRegistryFn = registry.Load
	tagsAddRegistryFn  = registry.Add
	tagsEnsureDirFn    = config.EnsureDir
	tagsLoadGlobalFn   = config.LoadGlobalConfig
	tagsSaveGlobalFn   = config.SaveGlobalConfig
	tagsGetDefaultFn   = config.GetDefaultTag
)

func validateTagPrefix(tag string) error {
	if !strings.HasPrefix(tag, "tag:") {
		return fmt.Errorf("tag must start with \"tag:\": got %q", tag)
	}
	return nil
}

func findService(reg *registry.Registry, name string) (int, error) {
	for i, svc := range reg.Services {
		if svc.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("service not found: %s", name)
}

// tagsListRun lists all services and their tags.
func tagsListRun(out io.Writer) error {
	regPath, err := tagsRegistryPathFn()
	if err != nil {
		return err
	}
	reg, err := tagsLoadRegistryFn(regPath)
	if err != nil {
		return err
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
func tagsPullRun(ctx context.Context, out io.Writer) error {
	tags, err := tagsReadTagsFn(ctx)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		fmt.Fprintln(out, "No tags found in tailnet ACL")
		return nil
	}
	defaultTag := tagsGetDefaultFn()
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
func tagsAddRun(out io.Writer, serviceName, tag string) error {
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
	reg, err := tagsLoadRegistryFn(regPath)
	if err != nil {
		return err
	}
	idx, err := findService(reg, serviceName)
	if err != nil {
		return err
	}
	svc := reg.Services[idx]
	for _, t := range svc.Tags {
		if t == tag {
			fmt.Fprintf(out, "→ %s already on %s\n", tag, serviceName)
			return nil
		}
	}
	svc.Tags = append(svc.Tags, tag)
	if err := tagsAddRegistryFn(regPath, svc); err != nil {
		return err
	}
	fmt.Fprintf(out, "→ Added %s to %s\n", tag, serviceName)
	return nil
}

// tagsSetRun replaces a service's tags with a single tag.
func tagsSetRun(out io.Writer, serviceName, tag string) error {
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
	reg, err := tagsLoadRegistryFn(regPath)
	if err != nil {
		return err
	}
	idx, err := findService(reg, serviceName)
	if err != nil {
		return err
	}
	svc := reg.Services[idx]
	svc.Tags = []string{tag}
	if err := tagsAddRegistryFn(regPath, svc); err != nil {
		return err
	}
	fmt.Fprintf(out, "→ Set %s tags to [%s]\n", serviceName, tag)
	return nil
}

// tagsSetDefaultRun sets the global default tag.
func tagsSetDefaultRun(out io.Writer, tag string) error {
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
	fmt.Fprintf(out, "→ Default tag set to %s\n", tag)
	return nil
}

// tagsDeleteRemoteRun deletes a tag from the tailnet ACL after safety checks.
func tagsDeleteRemoteRun(ctx context.Context, out io.Writer, tag string) error {
	if err := validateTagPrefix(tag); err != nil {
		return err
	}
	// Check if tag is the current default
	defaultTag := tagsGetDefaultFn()
	if tag == defaultTag {
		return fmt.Errorf("cannot delete default tag %q — change the default first with: tslink tags set-default <other-tag>", tag)
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
		return fmt.Errorf("cannot delete %q — in use by services: %s", tag, strings.Join(usedBy, ", "))
	}
	if err := tagsDeleteTagFn(ctx, tag); err != nil {
		return err
	}
	fmt.Fprintf(out, "→ Deleted %s from tailnet ACL\n", tag)
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
  delete-remote   Delete a tag from the tailnet ACL

Examples:
  tslink tags list
  tslink tags pull
  tslink tags add myapp tag:shared
  tslink tags set myapp tag:web
  tslink tags set-default tag:myteam
  tslink tags delete-remote tag:old`,
	}

	tagsListCmd := &cobra.Command{
		Use:   "list",
		Short: "Show tags for all registered services",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsListRun(cmd.OutOrStdout())
		},
	}

	tagsPullCmd := &cobra.Command{
		Use:   "pull",
		Short: "Fetch remote tags from tailnet ACL",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsPullRun(cmd.Context(), cmd.OutOrStdout())
		},
	}

	tagsAddCmd := &cobra.Command{
		Use:   "add <service> <tag>",
		Short: "Add a tag to a service",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsAddRun(cmd.OutOrStdout(), args[0], args[1])
		},
	}

	tagsSetCmd := &cobra.Command{
		Use:   "set <service> <tag>",
		Short: "Replace a service's tags with a single tag",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsSetRun(cmd.OutOrStdout(), args[0], args[1])
		},
	}

	tagsSetDefaultCmd := &cobra.Command{
		Use:   "set-default <tag>",
		Short: "Set the default tag for new services",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsSetDefaultRun(cmd.OutOrStdout(), args[0])
		},
	}

	tagsDeleteRemoteCmd := &cobra.Command{
		Use:   "delete-remote <tag>",
		Short: "Delete a tag from the tailnet ACL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return tagsDeleteRemoteRun(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}

	tagsCmd.AddCommand(tagsListCmd, tagsPullCmd, tagsAddCmd, tagsSetCmd, tagsSetDefaultCmd, tagsDeleteRemoteCmd)
	rootCmd.AddCommand(tagsCmd)
}
