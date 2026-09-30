package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

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

// registryCheck strictly validates a registry file without modifying it.
//
// A registry file that does not exist yet is the ordinary first-run state, not
// a failure. `tslink list` already reports that state as zero services and exit
// 0; `registry check` used to report the same state as exit 1 / internal_error
// with a raw absolute filesystem path in the message. Two commands classifying
// one normal state differently turns a first run into an incident for an agent
// and leaves it no stable recovery step, and internal_error is reserved for
// unexpected failures rather than for a state the product creates on purpose.
//
// Only a genuinely absent path is reclassified, and "is this path absent?" is
// answered by a single os.Lstat rather than by reading the file a second time.
// The difference is the whole point, and reading twice was wrong three ways:
//
//   - Lstat does not follow symlinks. A registry.json that is a symlink to a
//     nonexistent target is present-but-broken, not absent; os.ReadFile reports
//     ENOENT for it exactly as it does for a missing file, so a second read
//     called it a healthy first run while `tslink list` — which converges the
//     same path through atomicfile.ConvergePrivateFile — called it an unsafe
//     state file. That is the same one-state-two-classifications defect this
//     function exists to remove, recreated on a different filesystem shape.
//   - Lstat does not open the file. A registry.json that is a FIFO (or any
//     other object whose open blocks) hands its single writer to the first
//     read; a second read then waits on a second writer that will never come,
//     and the command never returns at all.
//   - Lstat cannot reinterpret content. A second read of the bytes can turn a
//     genuine corruption error into success if the path changes underneath;
//     a stat can only ever answer "the path is gone now".
//
// The stat answers for the last path component only: Lstat still resolves every
// ancestor. A config dir that is itself a symlink therefore reports ENOENT for
// the registry inside it and is classified as a first run, while atomicfile's
// EnsurePrivateDir refuses that same directory, so the first write fails. The
// classification is deliberately left alone: Issues is only ever populated
// beside a returned error (see below), so there is no ok-with-a-warning shape to
// report this in, and reporting it as a failure would make `registry check`
// disagree with `tslink list` — which reports the same state as zero services —
// in the opposite direction, recreating the split this function exists to
// remove. The read commands agree; the write command is where an unusable state
// directory surfaces.
//
// A path that exists but is empty, unreadable, malformed or not a regular file
// keeps Preflight's original error. Registry writes go through
// internal/atomicfile, so tslink never leaves a zero-byte registry.json behind
// itself, and a strict validator should report that shape rather than silently
// call it a fresh install.
//
// The stat runs only to explain a failure, so the healthy path still touches
// the file exactly once.
func registryCheck(path string) (RegistryCheckResult, error) {
	reg, issues, err := registry.Preflight(path)
	if err != nil {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			return RegistryCheckResult{
				Path:          path,
				SchemaVersion: registry.CurrentRegistrySchemaVersion,
				ValidServices: 0,
				TotalServices: 0,
				Issues:        []RegistryCheckIssue{},
			}, nil
		}
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
				if _, err := os.Lstat(path); os.IsNotExist(err) {
					return output.ErrNotFound(fmt.Sprintf("registry file not found: %s", path))
				}
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
