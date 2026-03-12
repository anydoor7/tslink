package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/monody0007/tslink/internal/config"
	"github.com/spf13/cobra"
)

const plistLabel = "com.tslink.daemon"

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
    <key>StandardOutPath</key>
    <string>{{.OutLog}}</string>
    <key>StandardErrorPath</key>
    <string>{{.ErrLog}}</string>
</dict>
</plist>
`))

type plistData struct {
	Label      string
	Executable string
	OutLog     string
	ErrLog     string
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as macOS LaunchAgent (auto-start on login)",
	Long: `Register TSLink as a macOS LaunchAgent so it starts automatically
when you log in and restarts if it crashes.

Example:
  tslink install`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := config.EnsureDir(); err != nil {
			return err
		}

		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		logDir, _ := config.LogDir()
		outLog := filepath.Join(logDir, "tslink.out.log")
		errLog := filepath.Join(logDir, "tslink.err.log")

		plistPath := plistPath()
		f, err := os.Create(plistPath)
		if err != nil {
			return fmt.Errorf("create plist: %w", err)
		}
		defer f.Close()

		data := plistData{
			Label:      plistLabel,
			Executable: exe,
			OutLog:     outLog,
			ErrLog:     errLog,
		}
		if err := plistTemplate.Execute(f, data); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}

		if err := exec.Command("launchctl", "load", plistPath).Run(); err != nil {
			fmt.Printf("→ ⚠ LaunchAgent installed but could not auto-load: %v\n", err)
			fmt.Println("  Run 'launchctl load " + plistPath + "' manually")
		} else {
			fmt.Printf("→ ✓ LaunchAgent installed and loaded: %s\n", plistPath)
		}
		return nil
	},
}

func plistPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
}

func init() {
	rootCmd.AddCommand(installCmd)
}
