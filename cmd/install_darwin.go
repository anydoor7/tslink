//go:build darwin

package cmd

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

// InstallResult is the JSON payload for the install command.
type InstallResult struct {
	PlistPath       string `json:"plist_path"`
	Loaded          bool   `json:"loaded"`
	LaunchctlTarget string `json:"launchctl_target"`
	LaunchctlOutput string `json:"launchctl_output,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

var (
	userHomeDirFn           = os.UserHomeDir
	executablePathFn        = os.Executable
	evalSymlinksFn          = filepath.EvalSymlinks
	userUIDFn               = os.Getuid
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		return exec.Command("launchctl", args...).CombinedOutput()
	}
)

const plistLabel = "com.tslink.daemon"
const launchdThrottleInterval = 30

type launchctlLoadResult struct {
	Domain  string
	Target  string
	Output  string
	Err     error
	Warning string
}

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Executable}}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>{{.ThrottleInterval}}</integer>
    <key>StandardOutPath</key>
    <string>{{.OutLog}}</string>
    <key>StandardErrorPath</key>
    <string>{{.ErrLog}}</string>
</dict>
</plist>
`))

type plistData struct {
	Label            string
	Executable       string
	OutLog           string
	ErrLog           string
	ThrottleInterval int
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as macOS LaunchAgent",
	Long: `Register TSLink as a macOS LaunchAgent so it starts automatically
when you log in and restarts if it crashes.

This command:
  1. Creates a LaunchAgent plist at ~/Library/LaunchAgents/com.tslink.daemon.plist
  2. Configures it to run 'tslink serve' at login with auto-restart (KeepAlive)
  3. Logs stdout to ~/.config/tslink/logs/tslink.out.log
  4. Logs stderr to ~/.config/tslink/logs/tslink.err.log
  5. Uses launchd ThrottleInterval=30 to avoid tight restart loops on failures
  6. Reloads the agent immediately with bootout-then-bootstrap
  7. Falls back from gui/$(id -u) to user/$(id -u) in SSH/headless sessions

To check if the agent is loaded:
  launchctl list | grep tslink
  launchctl print gui/$(id -u)/com.tslink.daemon
  launchctl print user/$(id -u)/com.tslink.daemon

To remove the autostart:
  tslink uninstall

Headless/SSH caveat:
  macOS may not expose gui/$(id -u) until a desktop login exists. In that case
  tslink install tries launchctl bootstrap user/$(id -u) and prints the domain
  it used. Re-run tslink install from a desktop login to move back to gui/$(id -u).

	Examples:
	  tslink install                Register and start the LaunchAgent`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		exe, err := executablePathFn()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = evalSymlinksFn(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		logDir, _ := config.LogDir()
		outLog := filepath.Join(logDir, "tslink.out.log")
		errLog := filepath.Join(logDir, "tslink.err.log")

		plistPath, err := plistPath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			return fmt.Errorf("create LaunchAgents directory: %w", err)
		}

		data := plistData{
			Label:            plistLabel,
			Executable:       exe,
			OutLog:           outLog,
			ErrLog:           errLog,
			ThrottleInterval: launchdThrottleInterval,
		}
		var plist bytes.Buffer
		if err := plistTemplate.Execute(&plist, data); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}
		if err := os.WriteFile(plistPath, plist.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}

		loadResult := reinstallLaunchAgent(plistPath)

		if jsonOutput(cmd) {
			result := InstallResult{
				PlistPath:       plistPath,
				Loaded:          loadResult.Err == nil,
				LaunchctlTarget: loadResult.Target,
				LaunchctlOutput: loadResult.Output,
				Warning:         loadResult.Warning,
			}
			if loadResult.Err != nil && result.Warning == "" {
				result.Warning = launchctlWarning("LaunchAgent plist installed but launchctl bootstrap failed", loadResult.Err, []byte(loadResult.Output))
			}
			output.Success("install", result)
			return nil
		}

		if loadResult.Err != nil {
			warning := loadResult.Warning
			if warning == "" {
				warning = launchctlWarning("LaunchAgent installed but could not auto-load", loadResult.Err, []byte(loadResult.Output))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "→ ⚠ %s\n", warning)
			fmt.Fprintf(cmd.OutOrStdout(), "  Run 'launchctl bootstrap %s %s' manually\n", loadResult.Domain, plistPath)
		} else {
			if loadResult.Warning != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "→ ⚠ %s\n", loadResult.Warning)
			}
			if loadResult.Output != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "→ launchctl output: %s\n", loadResult.Output)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ LaunchAgent installed and loaded in %s: %s\n", loadResult.Domain, plistPath)
		}
		return nil
	},
}

func plistPath() (string, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

func launchctlDomain() string {
	return fmt.Sprintf("gui/%d", userUIDFn())
}

func launchctlUserDomain() string {
	return fmt.Sprintf("user/%d", userUIDFn())
}

func launchctlServiceTarget() string {
	return launchctlServiceTargetForDomain(launchctlDomain())
}

func launchctlServiceTargetForDomain(domain string) string {
	return domain + "/" + plistLabel
}

func reinstallLaunchAgent(plistPath string) launchctlLoadResult {
	guiDomain := launchctlDomain()
	userDomain := launchctlUserDomain()
	bootoutLaunchAgentTargets(guiDomain, userDomain)

	output, err := launchctlCombinedOutput("bootstrap", guiDomain, plistPath)
	if err == nil {
		return launchctlLoadResult{
			Domain: guiDomain,
			Target: launchctlServiceTargetForDomain(guiDomain),
			Output: strings.TrimSpace(string(output)),
		}
	}
	if !launchctlDomainNotFound(output, err) {
		return launchctlLoadResult{
			Domain: guiDomain,
			Target: launchctlServiceTargetForDomain(guiDomain),
			Output: strings.TrimSpace(string(output)),
			Err:    err,
		}
	}

	fallbackOutput, fallbackErr := launchctlCombinedOutput("bootstrap", userDomain, plistPath)
	combinedOutput := combineLaunchctlOutput(output, fallbackOutput)
	warning := fmt.Sprintf("launchctl %s is unavailable in this SSH/headless session; tried %s fallback", guiDomain, userDomain)
	if fallbackErr != nil {
		return launchctlLoadResult{
			Domain:  userDomain,
			Target:  launchctlServiceTargetForDomain(userDomain),
			Output:  combinedOutput,
			Err:     fallbackErr,
			Warning: launchctlWarning(warning, fallbackErr, []byte(combinedOutput)),
		}
	}
	return launchctlLoadResult{
		Domain:  userDomain,
		Target:  launchctlServiceTargetForDomain(userDomain),
		Output:  combinedOutput,
		Warning: warning,
	}
}

func bootoutLaunchAgentTargets(domains ...string) {
	for _, domain := range domains {
		_, _ = launchctlCombinedOutput("bootout", launchctlServiceTargetForDomain(domain))
	}
}

func launchctlDomainNotFound(output []byte, err error) bool {
	text := strings.ToLower(string(output))
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "domain does not exist") ||
		strings.Contains(text, "could not find domain for:") ||
		strings.Contains(text, "domain is not found") ||
		strings.Contains(text, "no such domain")
}

func combineLaunchctlOutput(outputs ...[]byte) string {
	var parts []string
	for _, output := range outputs {
		text := strings.TrimSpace(string(output))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func launchctlWarning(message string, err error, combinedOutput []byte) string {
	detail := strings.TrimSpace(string(combinedOutput))
	if detail == "" {
		return fmt.Sprintf("%s: %v", message, err)
	}
	return fmt.Sprintf("%s: %v; output: %s", message, err, detail)
}

func init() {
	rootCmd.AddCommand(installCmd)
}
