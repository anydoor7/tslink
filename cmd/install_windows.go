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

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install from Windows Startup",
	Long: `Register TSLink in the Windows Startup folder so it launches in the
background when you sign in.

Example:
  tslink install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = filepath.EvalSymlinks(exe)
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

		escapedExe := strings.ReplaceAll(exe, `"`, `""`)
		script := fmt.Sprintf(`CreateObject("Wscript.Shell").Run """" & "%s" & """ serve", 0, False
`, escapedExe)
		if err := os.WriteFile(startupPath, []byte(script), 0o644); err != nil {
			return fmt.Errorf("write Startup script: %w", err)
		}

		fmt.Printf("→ ✓ Startup script installed: %s\n", startupPath)
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

func init() {
	rootCmd.AddCommand(installCmd)
}
