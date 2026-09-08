package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

const daemonSettleWindow = time.Second

// Supervision separates current liveness from verified restart configuration.
// Unknown ownership falls back to manual/none, with a diagnostic, never a
// promise that this process will survive a reboot.
type Supervision struct {
	Manager       string `json:"manager"`
	Installed     bool   `json:"installed"`
	Autostart     bool   `json:"autostart"`
	RestartOnExit bool   `json:"restart_on_exit"`
	Path          string `json:"path,omitempty"`
	Detail        string `json:"detail"`
}

var (
	detectSupervisionFn = detectSupervision
	ensureDaemonFn      = ensureDaemon
	installDaemonFn     = installDaemon
	bootstrapTimeout    = 15 * time.Second
	bootstrapInterval   = 250 * time.Millisecond
	bootstrapSettle     = daemonSettleWindow
	managerOutputFn     = boundedManagerOutput
)

func boundedManagerOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func unmanagedSupervision(running bool, detail string) Supervision {
	manager := "none"
	if running {
		manager = "manual"
	}
	return Supervision{Manager: manager, Detail: detail}
}

func windowsStartupSupervision(path string) Supervision {
	return Supervision{Manager: "windows-startup", Installed: true, Autostart: true, Path: path,
		Detail: "Windows Startup registration exists for this config; starts at sign-in, with no crash restart or provable live PID ownership. Undo: tslink uninstall"}
}

func formatSupervision(s Supervision, out io.Writer) {
	fmt.Fprintf(out, "Supervision: %s (autostart=%t, restart_on_exit=%t)\n", s.Manager, s.Autostart, s.RestartOnExit)
	if s.Detail != "" {
		fmt.Fprintln(out, s.Detail)
	}
}

func daemonNotRunningError() error {
	return registry.CodedError{Code: "daemon_not_running", Message: "TSLink is not running; install and start its background service with 'tslink install'", Next: []string{"tslink install"}}
}

func daemonSetupError(err error) error {
	state := "No supervisor definition was found after this failure."
	if path, pathErr := supervisorPath(); pathErr == nil {
		if _, statErr := os.Stat(path); statErr == nil {
			state = "A supervisor definition remains at " + path + "; it may be enabled and restarting/crash-looping. Read logs before retrying."
		} else if !os.IsNotExist(statErr) {
			state = "The remaining supervisor definition could not be inspected: " + path
		}
	}
	return registry.CodedError{Code: "daemon_setup_failed", Message: fmt.Sprintf("background service setup failed: %v. %s Check saved configuration with 'tslink list'. Inspect 'tslink logs' and 'tslink doctor' before repairing with 'tslink install'", err, state), Next: []string{"tslink logs", "tslink doctor", "tslink install"}}
}

// installDaemon calls the existing platform installer, including its conflict
// checks and upgrade rollback. A detached command keeps --json output to one
// envelope and sends every installation announcement to the caller's stderr.
func installDaemon(ctx context.Context, out io.Writer) error {
	cmd := &cobra.Command{Use: "install"}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.Flags().Bool("no-auto-provision", false, "")
	cmd.Flags().Bool("force", false, "")
	if err := installCmd.RunE(cmd, nil); err != nil {
		return err
	}
	return startInstalledDaemon(ctx, out)
}

// CLI and CI have the same behavior; no terminal detection or prompts. The
// opt-out is configuration-only and cannot make a stopped daemon look ready.
func ensureDaemon(ctx context.Context, out io.Writer, noInstall bool) error {
	pidPath, err := config.PIDPath()
	if err != nil {
		return err
	}
	if isRunningFn(pidPath) {
		return nil
	}
	if noInstall {
		fmt.Fprintln(out, "Background service installation skipped (--no-daemon-install). Configuration only; URLs are unavailable until 'tslink install'.")
		return nil
	}
	path, err := supervisorPath()
	if err != nil {
		return daemonSetupError(err)
	}
	// One manager slot per OS user, even when callers use different config
	// directories. Never let concurrent add commands replace each other's job.
	return daemon.WithPIDLock(path+".bootstrap", func() error {
		if isRunningFn(pidPath) {
			return nil
		}
		if err := checkBootstrapScope(path); err != nil {
			return daemonSetupError(err)
		}
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Installing TSLink background service (%s): %s\nConfig: %s\nAutostart persists across sign-in/reboot; undo with: tslink uninstall\n", supervisorName(), path, dir)
		if err := installDaemonFn(ctx, out); err != nil {
			return daemonSetupError(err)
		}
		snapshotPath, err := config.RuntimeSnapshotPath()
		if err != nil {
			return err
		}
		_, err = waitStableDaemon(ctx, func() (int, error) {
			if !isRunningFn(pidPath) {
				return 0, nil
			}
			pid, err := readPIDFn(pidPath)
			if err != nil {
				return 0, err
			}
			s := detectSupervisionFn(pidPath, true, pid)
			if !s.Autostart || s.Manager == "manual" || s.Manager == "none" {
				return 0, fmt.Errorf("supervisor ownership/autostart unconfirmed: %s", s.Detail)
			}
			// A PID alone precedes server initialization. Require a fresh
			// business artifact from this process (including enrollment).
			snapshot, loadErr := tsruntime.Load(snapshotPath)
			if loadErr == nil && snapshot != nil && snapshot.DaemonPID == pid && snapshot.GlobalError == nil && time.Since(snapshot.UpdatedAt) >= 0 && time.Since(snapshot.UpdatedAt) < 30*time.Second {
				return pid, nil
			}
			handoff, loadErr := loadAuthHandoff(filepath.Join(filepath.Dir(pidPath), "auth-handoff.json"))
			if loadErr == nil && handoff.DaemonPID == pid && handoff.ExpiresAt.After(time.Now()) {
				return pid, nil
			}
			return 0, nil
		}, bootstrapTimeout, bootstrapInterval, bootstrapSettle)
		if err != nil {
			return daemonSetupError(err)
		}
		fmt.Fprintln(out, "TSLink background service is ready and autostart is verified.")
		return nil
	})
}

// At least two consistent samples spanning settle are mandatory. Once a
// process has been seen, death, a changed PID, or a changed state fails the
// check, rather than blessing a crash/restart loop on its next lucky sample.
func waitStableDaemon(ctx context.Context, sample func() (int, error), timeout, interval, settle time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	var firstGood time.Time
	var previousPID, samples int
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		pid, err := sample()
		if err != nil {
			return 0, err
		}
		if previousPID > 0 && pid != previousPID {
			return 0, fmt.Errorf("daemon did not settle: PID/state changed (%d -> %d)", previousPID, pid)
		}
		if pid > 0 {
			if previousPID == 0 {
				firstGood = time.Now()
			}
			previousPID = pid
			samples++
			if samples >= 2 && time.Since(firstGood) >= settle {
				return pid, nil
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, fmt.Errorf("daemon did not settle within %s (%d ready samples)", timeout, samples)
		}
		timer := time.NewTimer(min(interval, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func absoluteConfigDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Abs(dir)
}

// Existing definitions may have been authored externally or refer to another
// config. Autoinstall only replaces a definition whose binding we can prove.
func checkBootstrapScope(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return checkUnregisteredSupervisor()
	}
	if err != nil {
		return err
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	if !supervisorConfigMatches(data, dir) {
		return fmt.Errorf("existing supervisor at %s is not bound to config %s; automatic replacement refused; inspect it before explicitly running 'tslink install'", path, dir)
	}
	return checkSupervisorProcessScope()
}
