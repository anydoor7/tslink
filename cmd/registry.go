package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

type RegistryCheckIssue struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type RegistryCheckResult struct {
	Path          string               `json:"path"`
	SchemaVersion int                  `json:"schema_version"`
	ValidServices int                  `json:"valid_services"`
	TotalServices int                  `json:"total_services"`
	Issues        []RegistryCheckIssue `json:"issues"`
}

func registryCheck(path string) (RegistryCheckResult, error) {
	reg, issues, err := registry.Preflight(path)
	if err != nil {
		return RegistryCheckResult{}, err
	}
	result := RegistryCheckResult{
		Path:          path,
		SchemaVersion: reg.SchemaVersion,
		ValidServices: len(reg.Services),
		TotalServices: len(reg.Services) + len(issues),
		Issues:        make([]RegistryCheckIssue, 0, len(issues)),
	}
	if len(issues) == 0 {
		return result, nil
	}

	errs := make([]error, 0, len(issues))
	for _, issue := range issues {
		code, _ := registry.ErrorCode(issue.Err)
		result.Issues = append(result.Issues, RegistryCheckIssue{
			Index:   issue.Index,
			Name:    issue.Name,
			Code:    code,
			Message: issue.Err.Error(),
		})
		errs = append(errs, issue)
	}
	return result, errors.Join(errs...)
}

func formatRegistryCheck(result RegistryCheckResult, out io.Writer) {
	fmt.Fprintf(out, "registry valid: %d services (%s)\n", result.ValidServices, result.Path)
}

func init() {
	registryCmd := &cobra.Command{
		Use:   "registry",
		Short: "Inspect the local service registry",
		Args:  cobra.NoArgs,
		RunE:  runCommandGroup,
	}
	checkCmd := &cobra.Command{
		Use:   "check [path]",
		Short: "Strictly validate a registry.json without modifying it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			} else {
				var err error
				path, err = config.RegistryPath()
				if err != nil {
					return err
				}
			}
			result, err := registryCheck(path)
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("registry check", result)
				return nil
			}
			formatRegistryCheck(result, cmd.OutOrStdout())
			return nil
		},
	}
	registryCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(registryCmd)
}
