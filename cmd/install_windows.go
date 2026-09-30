//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

const windowsStartupScriptName = "tslink.vbs"

var (
	windowsExecutablePathFn = os.Executable
	windowsEvalSymlinksFn   = filepath.EvalSymlinks
)

type InstallResult struct {
	Path           string `json:"path"`
	Installed      bool   `json:"installed"`
	Started        bool   `json:"started"`
	ServiceManager string `json:"service_manager"`
	Warning        string `json:"warning,omitempty"`
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install from Windows Startup",
	Long: `Register TSLink in the Windows Startup folder so it launches in the
background when you sign in.

This command:
  1. Creates a VBScript at %APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs
  2. The script runs 'tslink serve' silently (no console window) at login

Re-running 'tslink install' is the supported upgrade path; it rewrites the
Startup script to point at the current executable.

The installer intentionally does not inspect or stop a daemon that is running
now: writing the Startup script does not start another process in this session.
The command reports Started=false; the script takes effect only at next sign-in.

Note: Unlike macOS LaunchAgent and Linux systemd, the Windows Startup script
does not auto-restart on crash. If the process exits, it will only restart on
the next login.

To verify the script exists:
  dir "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs"

To remove the autostart:
  tslink uninstall

	Examples:
	  tslink install                Register the Startup script`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withSupervisorTransaction(cmd.Context(), func() error {
			return runInstallLocked(cmd, args)
		})
	},
}

// runInstallLocked requires the per-user supervisor transaction lock.
func runInstallLocked(cmd *cobra.Command, args []string) error {
	noAutoProvision, err := cmd.Flags().GetBool("no-auto-provision")
	if err != nil {
		return fmt.Errorf("read --no-auto-provision: %w", err)
	}
	// No daemon-conflict guard is needed here. Unlike launchd/systemd, the
	// Startup folder does not take ownership or start a process during install.
	exe, err := windowsExecutablePathFn()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	exe, err = windowsEvalSymlinksFn(exe)
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}

	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(startupPath), 0o755); err != nil {
		return fmt.Errorf("create Startup directory: %w", err)
	}

	configDir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	script := "Set shell = CreateObject(\"Wscript.Shell\")\r\n" + windowsConfigEnvironment(configDir) + "\r\n" + windowsStartupScript(exe, noAutoProvision)
	if err := atomicfile.WriteFileInExistingDir(startupPath, []byte(script), atomicfile.PrivateFileMode); err != nil {
		return fmt.Errorf("write Startup script: %w", err)
	}

	if jsonOutput(cmd) {
		output.Success("install", InstallResult{
			Path:           startupPath,
			Installed:      true,
			Started:        false,
			ServiceManager: "windows-startup",
			Warning:        "Windows Startup launches TSLink only at next sign-in and does not auto-restart on crash",
		})
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ Startup script installed: %s\n", startupPath)
	return nil
}

func windowsStartupScriptPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA is not set")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", windowsStartupScriptName), nil
}

func windowsStartupScript(exe string, noAutoProvision bool) string {
	serveArgs := " serve"
	if noAutoProvision {
		serveArgs += " --no-auto-provision"
	}
	return `shell.Environment("Process")("TSLINK_MANAGED_LOGS") = "1"` + "\r\n" + fmt.Sprintf("CreateObject(\"Wscript.Shell\").Run \"\"\"\" & %s & \"\"\"%s\", 0, False\r\n", vbsStringLiteral(exe), serveArgs)
}

func vbsStringLiteral(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func init() {
	installCmd.Flags().Bool("no-auto-provision", false, "Install the managed daemon with Funnel policy auto-provisioning disabled")
	rootCmd.AddCommand(installCmd)
}
