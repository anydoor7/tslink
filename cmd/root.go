package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anydoor7/tslink/internal/cliargs"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/spf13/cobra"
)

// Version is set by main before Execute() is called.
var Version string

// Commit is the VCS commit SHA, set by main before Execute() is called. It is
// empty for `go build`/dev builds and embedded via -ldflags on release builds.
var Commit string
var lastCommandName string

type VersionResult struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
}

var rootCmd = &cobra.Command{
	Use:   "tslink",
	Short: "Expose local services to your Tailscale network",
	Long: `TSLink is a local service gateway that exposes web services, file
directories, and TCP endpoints to your private Tailscale network.

Each registered service gets its own tailnet hostname and tailnet transport
identity. Proxy and file services use Tailscale HTTPS listeners; raw TCP
services are private tailnet TCP routes without TSLink HTTP identity
middleware or TLS termination.

WhoIs identity headers are best-effort for proxy requests and are enforced
when HTTP --allow or people grants are configured for proxy/file services. Public Funnel
exposure is off by default and requires explicit acknowledgement.

Supported on macOS, Linux, and Windows.

Examples:
  tslink add myapp --proxy localhost:3000        Expose a web service
  tslink serve                                   Enroll without an admin credential
  tslink login                                   Store an optional durable-install credential
  tslink add docs --dir ~/Documents              Expose a file directory
  tslink add mydb --tcp localhost:5432           Expose a TCP endpoint
  tslink serve --daemon                          Start as background daemon
  tslink list                                    List registered services
  tslink status                                  Show running status
  tslink config set control-url https://hs.example.com   Use Headscale

Use "tslink <command> --help" for detailed information about each command.`,
	Args: cobra.NoArgs,
	RunE: runCommandGroup,
}

// commandGroupUsageError keeps a group invocation machine-readable while
// preserving both the shared usage exit category and command-specific
// navigation. Unwrap supplies the numeric usage exit; StableCode and
// NextCommands supply the envelope's stable discriminator and recovery list.
type commandGroupUsageError struct {
	message string
	next    []string
}

func (e commandGroupUsageError) Error() string { return e.message }

func (e commandGroupUsageError) Unwrap() error { return output.ErrUsage(e.message) }

func (e commandGroupUsageError) StableCode() string {
	return output.StableErrorCode(output.ExitUsage)
}

func (e commandGroupUsageError) NextCommands() []string {
	return append([]string(nil), e.next...)
}

func runCommandGroup(cmd *cobra.Command, _ []string) error {
	if !jsonOutput(cmd) {
		return cmd.Help()
	}

	next := make([]string, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		next = append(next, child.CommandPath()+" --help")
	}
	sort.Strings(next)
	return commandGroupUsageError{
		message: fmt.Sprintf("%s is a command group; choose a subcommand", cmd.CommandPath()),
		next:    next,
	}
}

func init() {
	cliargs.RegisterRootFlags(rootCmd)
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return output.ErrUsage(err.Error())
	})
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
}

// WasJSONRequested returns true if --json was passed on the command line.
// Safe to call after rootCmd.Execute() returns.
func WasJSONRequested() bool {
	v, _ := rootCmd.PersistentFlags().GetBool("json")
	return v || argsContainJSON(os.Args[1:]) || lastCommandName == "extend"
}

func Execute() error {
	rootCmd.Version = versionDisplayString(Version, Commit)
	if rootVersionJSONRequested(os.Args[1:]) {
		lastCommandName = "version"
		output.Success("version", VersionResult{Version: Version, Commit: Commit})
		return nil
	}
	executed, err := rootCmd.ExecuteC()
	if executed != nil {
		lastCommandName = commandName(executed)
	}
	return err
}

func LastCommandName() string {
	return lastCommandName
}

func commandName(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	path := strings.Fields(cmd.CommandPath())
	if len(path) <= 1 {
		return ""
	}
	return strings.Join(path[1:], " ")
}

func argsContainJSON(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "--":
			return false
		case arg == "--json":
			return true
		case strings.HasPrefix(arg, "--json="):
			return strings.TrimPrefix(arg, "--json=") == "true"
		}
	}
	return false
}

func rootVersionJSONRequested(args []string) bool {
	if !argsContainJSON(args) {
		return false
	}
	hasVersion := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--version" {
			hasVersion = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return false
	}
	return hasVersion
}
