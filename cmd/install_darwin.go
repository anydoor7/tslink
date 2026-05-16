//go:build darwin

package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

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
  5. Uses launchd throttling to avoid tight restart loops on repeated failures
  6. Loads the agent immediately via 'launchctl bootstrap gui/$(id -u)'

To check if the agent is loaded:
  launchctl list | grep tslink
  launchctl print gui/$(id -u)/com.tslink.daemon

To remove the autostart:
  tslink uninstall

Examples:
  tslink install                Register and start the LaunchAgent`,
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

		target := launchctlServiceTarget()
		loadOutput, loadErr := launchctlCombinedOutput("bootstrap", launchctlDomain(), plistPath)
		outputText := strings.TrimSpace(string(loadOutput))

		if jsonOutput(cmd) {
			result := InstallResult{
				PlistPath:       plistPath,
				Loaded:          loadErr == nil,
				LaunchctlTarget: target,
				LaunchctlOutput: outputText,
			}
			if loadErr != nil {
				result.Warning = launchctlWarning("LaunchAgent plist installed but launchctl bootstrap failed", loadErr, loadOutput)
			}
			output.Success("install", result)
			return nil
		}

		if loadErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "→ ⚠ %s\n", launchctlWarning("LaunchAgent installed but could not auto-load", loadErr, loadOutput))
			fmt.Fprintf(cmd.OutOrStdout(), "  Run 'launchctl bootstrap %s %s' manually\n", launchctlDomain(), plistPath)
		} else {
			if outputText != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "→ launchctl output: %s\n", outputText)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ LaunchAgent installed and loaded: %s\n", plistPath)
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

func launchctlServiceTarget() string {
	return launchctlDomain() + "/" + plistLabel
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
