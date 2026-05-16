//go:build linux

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

const systemdServiceName = "tslink.service"
const systemdRestartSec = 30

var (
	linuxUserHomeDirFn       = os.UserHomeDir
	linuxExecutablePathFn    = os.Executable
	linuxEvalSymlinksFn      = filepath.EvalSymlinks
	linuxUserNameFn          = defaultLinuxUserName
	linuxUserIDFn            = os.Getuid
	systemctlCombinedOutput  = func(args ...string) ([]byte, error) { return exec.Command("systemctl", args...).CombinedOutput() }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return exec.Command("loginctl", args...).CombinedOutput() }
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as systemd user service",
	Long: `Register TSLink as a systemd user service so it starts automatically
for your user session and restarts if it crashes.

This command:
  1. Creates a unit file at ~/.config/systemd/user/tslink.service
  2. Configures it to run 'tslink serve' with throttled auto-restart on failure
  3. Runs 'systemctl --user daemon-reload' to pick up the new unit
  4. Enables and restarts the service immediately so the new unit takes effect
  5. Checks systemd lingering and prints guidance for headless/logout survival

To check the service status:
  systemctl --user status tslink

To view logs:
  journalctl --user -u tslink

To remove the autostart:
  tslink uninstall

For headless Linux hosts where the service must survive logout:
  loginctl enable-linger "$USER"

If lingering was enabled only for TSLink, disable it after uninstall:
  loginctl disable-linger "$USER"

Examples:
  tslink install                Register and restart the systemd service`,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := linuxExecutablePathFn()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = linuxEvalSymlinksFn(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		servicePath, err := systemdServicePath()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
			return fmt.Errorf("create systemd user dir: %w", err)
		}

		service := systemdServiceContents(exe)
		if err := os.WriteFile(servicePath, []byte(service), 0o644); err != nil {
			return fmt.Errorf("write systemd service: %w", err)
		}

		if output, err := systemctlCombinedOutput("--user", "daemon-reload"); err != nil {
			return fmt.Errorf("reload systemd user daemon: %w: %s", err, output)
		}
		if output, err := systemctlCombinedOutput("--user", "enable", systemdServiceName); err != nil {
			return fmt.Errorf("enable systemd user service: %w: %s", err, output)
		}
		if output, err := systemctlCombinedOutput("--user", "restart", systemdServiceName); err != nil {
			return fmt.Errorf("restart systemd user service: %w: %s", err, output)
		}

		if warning := linuxLingerWarning(); warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", warning)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ systemd user service installed and restarted: %s\n", servicePath)
		return nil
	},
}

func systemdServiceContents(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=TSLink - Tailscale Service Gateway
After=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=%d

[Install]
WantedBy=default.target
`, systemdQuoteExecPath(exe), systemdRestartSec)
}

func systemdQuoteExecPath(path string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		switch r {
		case '%':
			b.WriteString("%%")
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '$':
			b.WriteString("$$")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func defaultLinuxUserName() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if logname := os.Getenv("LOGNAME"); logname != "" {
		return logname
	}
	return strconv.Itoa(linuxUserIDFn())
}

func linuxLingerWarning() string {
	user := linuxUserNameFn()
	guidance := `run 'loginctl enable-linger "$USER"' so the user service can survive logout; after uninstall, run 'loginctl disable-linger "$USER"' if lingering was enabled only for TSLink`
	if user == "" {
		return "could not check systemd lingering because USER is not set; " + guidance
	}

	output, err := loginctlCombinedOutputFn("show-user", user, "--property=Linger", "--value")
	if err != nil {
		return fmt.Sprintf("could not check systemd lingering with loginctl: %v%s; %s", err, commandOutputSuffix(output), guidance)
	}

	linger := strings.TrimSpace(string(output))
	switch linger {
	case "yes":
		return ""
	case "no", "":
		return fmt.Sprintf("systemd lingering is disabled for user %s; %s", user, guidance)
	default:
		return fmt.Sprintf("could not interpret systemd lingering status %q for user %s; %s", linger, user, guidance)
	}
}

func commandOutputSuffix(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	return ": " + text
}

func systemdServicePath() (string, error) {
	home, err := linuxUserHomeDirFn()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdServiceName), nil
}

func init() {
	rootCmd.AddCommand(installCmd)
}
