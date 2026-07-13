//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const windowsStartupScriptName = "tslink.vbs"

var (
	windowsExecutablePathFn = os.Executable
	windowsEvalSymlinksFn   = filepath.EvalSymlinks
)

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install from Windows Startup",
	Long: `Register TSLink in the Windows Startup folder so it launches in the
background when you sign in.

This command:
  1. Creates a VBScript at %APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs
  2. The script runs 'tslink serve' silently (no console window) at login

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

		script := windowsStartupScript(exe)
		if err := os.WriteFile(startupPath, []byte(script), 0o644); err != nil {
			return fmt.Errorf("write Startup script: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ Startup script installed: %s\n", startupPath)
		return nil
	},
}

func windowsStartupScriptPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA is not set")
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", windowsStartupScriptName), nil
}

func windowsStartupScript(exe string) string {
	return fmt.Sprintf("CreateObject(\"Wscript.Shell\").Run \"\"\"\" & %s & \"\"\" serve\", 0, False\r\n", vbsStringLiteral(exe))
}

func vbsStringLiteral(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func init() {
	rootCmd.AddCommand(installCmd)
}
