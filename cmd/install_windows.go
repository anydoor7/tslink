//go:build windows

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/output"
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
	Short: "Install a per-user Windows scheduled task",
	Long: `Install and start TSLink using Task Scheduler at user sign-in, with
least privilege and access to the current user's Credential Manager. No
administrator rights or stored Windows password are required. The built-in
supervisor restarts unexpected daemon exits with a 1-to-60-second exponential
backoff, resetting after 5 minutes. Eight consecutive unstable runs stop crash
recovery; inspect status/doctor and logs, then run install to reset the breaker.
A graceful stop stops both processes and stays stopped. Task restart settings
are a launcher backstop; they did not recover daemon crashes in VM measurements.
The user must remain signed in; this does not run before sign-in or after logout.

Re-running install updates the executable and gracefully restarts an identified
scheduler-owned daemon. Stop an existing manual or Startup daemon first. The old
Startup entry is removed only after the scheduled task is registered and checked.

If Task Scheduler or PowerShell is unavailable, use 'tslink install --startup'.
That fallback installs a Startup script for the next sign-in, with no crash
restart. Use 'tslink status' and 'tslink doctor' to inspect supervision.

Examples:
  tslink install
  tslink install --startup`,
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
	startup, _ := cmd.Flags().GetBool("startup")
	if cmd.Annotations["tslink.bootstrap"] == "true" && supervisorName() == "windows-startup" {
		startup = true
	}
	if !startup {
		return installWindowsTask(cmd, noAutoProvision)
	}
	// Fallback cannot coexist with a task we installed. If the task has no
	// local definition, confirm absence before adding another autostart owner.
	taskPath, err := windowsTaskPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(taskPath); err == nil {
		return fmt.Errorf("scheduled task definition exists; run tslink uninstall before choosing --startup")
	}
	name, err := windowsTaskName()
	if err != nil {
		return err
	}
	if existing, queryErr := windowsSchedulerFn("query", name, nil); queryErr == nil && existing.Exists {
		return fmt.Errorf("scheduled task exists; run tslink uninstall before choosing --startup")
	}
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
	installCmd.Flags().Bool("startup", false, "Use the Windows Startup fallback without crash restart when Task Scheduler is unavailable")
	mustMarkFlagPlatforms(installCmd, "startup", "windows")
	rootCmd.AddCommand(installCmd)
}

func installWindowsTask(cmd *cobra.Command, noAutoProvision bool) error {
	name, err := windowsTaskName()
	if err != nil {
		return err
	}
	old, err := windowsSchedulerFn("query", name, nil)
	if err != nil {
		return fmt.Errorf("task scheduler unavailable (fallback: tslink install --startup): %w", err)
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	var oldSpec windowsTaskSpec
	if old.Exists {
		oldSpec, err = windowsTaskSpecFromDefinition([]byte(old.XML), dir)
		if err != nil {
			return fmt.Errorf("refusing to replace foreign scheduler task: %w", err)
		}
	}
	pidPath, err := config.PIDPath()
	if err != nil {
		return err
	}
	if isRunningFn(pidPath) {
		pid, err := readPIDFn(pidPath)
		if err != nil {
			return err
		}
		// Reinstall repairs health, including a task left disabled after a
		// failed stop. Prove process ownership independently of enabled/restart.
		if !old.Exists || !windowsDaemonRunningFn(pidPath) || old.State != 4 || !windowsTaskOwnsPIDFn(pid, old.Engines, oldSpec.Executable) {
			return output.ErrConflict("daemon already running without verified Task Scheduler ownership; run tslink stop, then tslink install")
		}
	} else if !daemon.IsPIDFileMissing(pidPath) && !daemon.IsProcessAbsentFromPIDFile(pidPath) && !daemon.IsForeignProcessFromPIDFile(pidPath) {
		return output.ErrConflict("daemon PID identity is unverified; inspect tslink doctor before install")
	}
	if old.State == 4 || len(old.Engines) > 0 || isRunningFn(pidPath) {
		if _, err := windowsSchedulerFn("disable", name, nil); err != nil {
			return err
		}
		if _, err := stopSupervisorFn(pidPath); err != nil {
			return fmt.Errorf("task disabled; supervisor stop failed (definition retained): %w", err)
		}
		if isRunningFn(pidPath) {
			if err := stopDaemonFn(pidPath); err != nil {
				return fmt.Errorf("task disabled; graceful stop failed (definition retained): %w", err)
			}
		}
	}
	if old.State == 4 || len(old.Engines) > 0 {
		current, err := windowsSchedulerFn("query", name, nil)
		if err != nil {
			return err
		}
		if current.State == 4 || len(current.Engines) > 0 {
			return output.ErrConflict("task engine still running; retry after graceful shutdown")
		}
	}
	exe, err := windowsExecutablePathFn()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	exe, err = windowsEvalSymlinksFn(exe)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	sid, err := windowsSIDFn()
	if err != nil {
		return err
	}
	spec := windowsTaskSpec{sid, exe, dir, windowsPowerShellPath(), noAutoProvision}
	definition, err := renderWindowsTask(spec)
	if err != nil {
		return err
	}
	path, err := windowsTaskPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := config.EnsureDir(); err != nil {
		return err
	}
	if err := clearBuiltinSupervisor(pidPath); err != nil {
		return err
	}
	// Retain evidence before mutation: a timed-out COM call may have committed.
	if err := atomicfile.WriteFileInExistingDir(path, definition, atomicfile.PrivateFileMode); err != nil {
		return err
	}
	installed, err := windowsSchedulerFn("register", name, definition)
	if err != nil {
		return fmt.Errorf("scheduler registration uncertain; definition retained at %s; inspect doctor/uninstall before retry: %w", path, err)
	}
	if !installed.Exists || !installed.Enabled || !windowsTaskMatches([]byte(installed.XML), spec) {
		if installed.Exists {
			_, _ = windowsSchedulerFn("disable", name, nil)
		}
		return fmt.Errorf("loaded scheduler definition verification failed; definition retained at %s", path)
	}
	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}
	if err := os.Remove(startupPath); err != nil && !os.IsNotExist(err) {
		_, disableErr := windowsSchedulerFn("disable", name, nil)
		return fmt.Errorf("startup migration cleanup failed; task not started; disable result=%v; inspect before next sign-in: %w", disableErr, err)
	}
	if _, err := windowsSchedulerFn("run", name, nil); err != nil {
		return fmt.Errorf("task registered but could not start: %w", err)
	}
	if _, err := waitStableDaemon(cmd.Context(), func() (int, error) {
		if !isRunningFn(pidPath) {
			return 0, nil
		}
		pid, err := readPIDFn(pidPath)
		if err != nil {
			return 0, err
		}
		if s := detectSupervisionFn(pidPath, true, pid); s.Manager != "windows-task-scheduler" || !verifiedDaemonSupervision(s) {
			return 0, fmt.Errorf("scheduler ownership/restart policy unverified: %s", s.Detail)
		}
		return pid, nil
	}, bootstrapTimeout, bootstrapInterval, bootstrapSettle); err != nil {
		return fmt.Errorf("task installed but daemon did not settle; definition retained: %w", err)
	}
	if jsonOutput(cmd) {
		output.Success("install", InstallResult{Path: path, Installed: true, Started: true, ServiceManager: "windows-task-scheduler"})
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ Task Scheduler task installed and start requested: %s\nDefinition: %s\n", name, path)
	return nil
}
