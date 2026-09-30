package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/filelock"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

const daemonSettleWindow = time.Second

const (
	managerQueryTimeout = 2 * time.Second
	managerWaitDelay    = 500 * time.Millisecond
)

// Supervision separates current liveness from verified restart configuration.
// Unknown ownership falls back to manual/none, with a diagnostic, never a
// promise that this process will survive a reboot.
type Supervision struct {
	Manager   string `json:"manager"`
	Installed bool   `json:"installed"`
	Autostart bool   `json:"autostart"`
	// AutostartScope answers the question Autostart alone cannot: "boot" comes
	// back with the machine while nobody is logged in, "login" only comes back
	// once this user has a session. Every supervisor this tool installs is a
	// per-user one, so the difference is the whole of what "will it still be
	// there after a reboot" means on a headless host. It is empty whenever
	// autostart itself is unverified.
	AutostartScope string `json:"autostart_scope,omitempty"`
	RestartOnExit  bool   `json:"restart_on_exit"`
	Path           string `json:"path,omitempty"`
	Detail         string `json:"detail"`
}

const (
	// autostartScopeBoot is reserved for supervisors proven to start without a
	// login: a systemd user manager kept alive by lingering.
	autostartScopeBoot = "boot"
	// autostartScopeLogin is the honest answer for a launchd LaunchAgent, a
	// Windows Startup entry, and a systemd user unit without lingering.
	autostartScopeLogin = "login"
	// autostartScopeUnknown is used when the enabled unit is real but the
	// boot-versus-login question could not be answered.
	autostartScopeUnknown = "unknown"
)

var (
	detectSupervisionFn = detectSupervision
	ensureDaemonFn      = ensureDaemon
	// installDaemonFn is called only while the supervisor transaction is locked.
	installDaemonFn   = installDaemonLocked
	bootstrapTimeout  = 15 * time.Second
	bootstrapInterval = 250 * time.Millisecond
	bootstrapSettle   = daemonSettleWindow
	// The second gate is bounded separately and briefly. What it waits for is
	// produced by the Tailscale control plane, and the caller's own --wait is
	// the authoritative wait for that, so a long budget here would only be a
	// second copy of a wait that already exists downstream.
	bootstrapEvidenceTimeout = 3 * time.Second
	managerOutputFn          = boundedManagerOutput
	trySupervisorLockFn      = trySupervisorLock
	openSupervisorLockFn     = os.OpenFile

	// bootstrapNowFn is the clock waitStableDaemon measures its settle window
	// and deadline against, so a test can hold the window still while it
	// counts samples instead of racing the scheduler.
	bootstrapNowFn = time.Now
)

func boundedManagerOutput(name string, args ...string) ([]byte, error) {
	return runBoundedManagerCommand(name, managerQueryTimeout, args...)
}

// Each manager process has a finite bound. The settle windows around repeated
// queries do not interrupt a single blocked CombinedOutput call on their own.
func runBoundedManagerCommand(name string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = managerWaitDelay
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return output, fmt.Errorf("%s command exceeded %s: %w", name, timeout, ctx.Err())
	}
	return output, err
}

func unmanagedSupervision(running bool, detail string) Supervision {
	manager := "none"
	if running {
		manager = "manual"
	}
	return Supervision{Manager: manager, Detail: detail}
}

func windowsStartupSupervision(path string) Supervision {
	return Supervision{Manager: "windows-startup", Installed: true, Autostart: true, AutostartScope: autostartScopeLogin, Path: path,
		Detail: "Windows Startup registration exists for this config; starts at sign-in, so it waits for a sign-in rather than returning at boot, with no crash restart or provable live PID ownership. Undo: tslink uninstall"}
}

func formatSupervision(s Supervision, out io.Writer) {
	scope := ""
	if s.AutostartScope != "" {
		scope = ", autostart_scope=" + s.AutostartScope
	}
	fmt.Fprintf(out, "Supervision: %s (autostart=%t%s, restart_on_exit=%t)\n", s.Manager, s.Autostart, scope, s.RestartOnExit)
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
	return registry.CodedError{Code: "daemon_setup_failed", Message: fmt.Sprintf("background service setup failed: %v. %s Inspect 'tslink logs' and 'tslink doctor' before repairing with 'tslink install'", err, state), Next: []string{"tslink logs", "tslink doctor", "tslink install"}}
}

// daemonRegistryRetainedError is only for callers that retain registry entries
// on setup failure, including templates that skip every existing entry without
// writing. Share rolls back new registrations and must not use it.
func daemonRegistryRetainedError(err error) error {
	var coded registry.CodedError
	if errors.As(err, &coded) && (coded.Code == "daemon_supervision_unverified" || coded.Code == "daemon_setup_failed") {
		coded.Message += ". Configuration remains in the registry; view it with 'tslink list'. After resolving supervision, retry the original command"
		return coded
	}
	return err
}

// installDaemonLocked is the bootstrap entry point; its caller owns the lock.
func installDaemonLocked(ctx context.Context, out io.Writer) error {
	cmd := &cobra.Command{Use: "install"}
	cmd.SetContext(ctx)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.Flags().Bool("no-auto-provision", false, "")
	cmd.Flags().Bool("force", false, "")
	if err := runInstallLocked(cmd, nil); err != nil {
		return err
	}
	return startInstalledDaemon(ctx, out)
}

// DaemonInstalled records a background service a command installed: the
// supervisor, the definition it wrote, and the command that undoes it. The
// install announcement goes to stderr, which an agent never reads, so share,
// add and template apply carry this in their result, and only when they
// installed one.
type DaemonInstalled struct {
	Manager string `json:"manager"`
	Path    string `json:"path"`
	Undo    string `json:"undo"`
}

// CLI and CI have the same behavior; no terminal detection or prompts. The
// opt-out is configuration-only and cannot make a stopped daemon look ready.
// It returns what it installed, or nil when a verified daemon already ran or
// installation was skipped.
func ensureDaemon(ctx context.Context, out io.Writer, noInstall bool) (*DaemonInstalled, error) {
	pidPath, err := config.PIDPath()
	if err != nil {
		return nil, err
	}
	if noInstall {
		if isRunningFn(pidPath) {
			return nil, nil
		}
		fmt.Fprintln(out, "Background service installation skipped (--no-daemon-install). Configuration only; URLs are unavailable until 'tslink install'.")
		return nil, nil
	}
	path, err := supervisorPath()
	if err != nil {
		return nil, daemonSetupError(err)
	}
	var installed *DaemonInstalled
	// One manager slot per OS user, even when callers use different config
	// directories. Never let concurrent add commands replace each other's job.
	err = withSupervisorTransaction(ctx, func() error {
		if isRunningFn(pidPath) {
			pid, err := readPIDFn(pidPath)
			if err == nil && pid <= 0 {
				err = fmt.Errorf("invalid daemon PID %d", pid)
			}
			if err == nil && pid > 0 {
				s := detectSupervisionFn(pidPath, true, pid)
				if verifiedDaemonSupervision(s) {
					return nil
				}
				err = fmt.Errorf("supervisor ownership/autostart unconfirmed (including restart policy): %s", s.Detail)
			}
			return registry.CodedError{Code: "daemon_supervision_unverified", Message: fmt.Sprintf("running daemon cannot be reused safely (%v); inspect with 'tslink doctor' and resolve the reported supervisor issue. For a manual daemon, run 'tslink stop', then 'tslink install'; no process was taken over", err), Next: []string{"tslink doctor", "tslink stop", "tslink install"}}
		}
		if err := checkBootstrapScope(path); err != nil {
			return daemonSetupError(err)
		}
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		// Nothing about this host has been inspected yet, so this line states
		// only what is being installed, where, and how to undo it. Whether the
		// supervisor comes back at boot or only once this user signs in is a
		// property of the host, not of the installer, and it is answered after
		// installation by the one renderer that owns that question:
		// formatSupervision, over a Supervision that was actually detected.
		// Promising reboot survival here contradicted that renderer within the
		// same command on any host without systemd lingering.
		fmt.Fprintf(out, "Installing TSLink background service (%s): %s\nConfig: %s\nUndo with: tslink uninstall. Autostart scope is not known before this host is inspected; 'tslink status' reports whether it returns at boot or only at sign-in.\n", supervisorName(), path, dir)
		if err := installDaemonFn(ctx, out); err != nil {
			return daemonSetupError(err)
		}
		snapshotPath, err := config.RuntimeSnapshotPath()
		if err != nil {
			return err
		}
		// Installation is judged by the first gate alone. Everything it reads is
		// local and deterministic: the supervisor definition, the process, and
		// whether the two agree, all held still across the settle window. When
		// this gate fails, the installation really did fail.
		pid, err := waitStableDaemon(ctx, func() (int, error) {
			if !isRunningFn(pidPath) {
				return 0, nil
			}
			pid, err := readPIDFn(pidPath)
			if err != nil {
				return 0, err
			}
			s := detectSupervisionFn(pidPath, true, pid)
			if !verifiedDaemonSupervision(s) {
				return 0, fmt.Errorf("supervisor ownership/autostart unconfirmed (including restart policy): %s", s.Detail)
			}
			return pid, nil
		}, bootstrapTimeout, bootstrapInterval, bootstrapSettle)
		if err != nil {
			return daemonSetupError(err)
		}
		// The second gate is remote and best effort. A first business artifact
		// can only appear once tsnet has reached the Tailscale coordination
		// server, so on a first run with no credentials its arrival time is a
		// network measurement, observed on one host between 2.4s and 28.9s
		// across ten first runs. Reporting a failed installation on that clock blames this
		// machine for someone else's latency, and the registry write and the
		// daemon are both already in place by then. Losing the process the
		// first gate verified is still a failure, and is now the only way to
		// reach the crash-loop wording.
		ready, err := waitDaemonEvidence(ctx, pidPath, snapshotPath, pid)
		if err != nil {
			return daemonSetupError(err)
		}
		installed = &DaemonInstalled{Manager: supervisorName(), Path: path, Undo: "tslink uninstall"}
		if ready {
			fmt.Fprintln(out, "TSLink background service is ready and autostart is verified.")
			return nil
		}
		fmt.Fprintf(out, "TSLink background service is running (pid %d) and autostart is verified. It has not published a first sync/enrollment artifact within %s; that step waits on the Tailscale coordination server, so the URL arrives through the wait this command already performs, or later through 'tslink status --json'.\n", pid, bootstrapEvidenceTimeout)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return installed, nil
}

// waitDaemonEvidence watches an already verified daemon for its first business
// artifact and reports whether one appeared. Not finding one is not an error:
// the caller continues, and the enrollment URL is resolved by the wait the
// caller already performs. The error return is reserved for the process going
// away or being replaced, which is the crash/restart loop the first gate
// cannot see after it returns.
func waitDaemonEvidence(ctx context.Context, pidPath, snapshotPath string, pid int) (bool, error) {
	handoffPath := filepath.Join(filepath.Dir(pidPath), "auth-handoff.json")
	deadline := time.Now().Add(bootstrapEvidenceTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		// Distinct from the first gate's identical-shaped message on purpose:
		// reaching either of these means the supervisor had already confirmed
		// ownership of this process and then lost it, which is the restart loop
		// the failure text describes.
		if !isRunningFn(pidPath) {
			return false, fmt.Errorf("daemon did not settle: verified process %d stopped after supervision confirmed it", pid)
		}
		if current, err := readPIDFn(pidPath); err == nil && current != pid {
			return false, fmt.Errorf("daemon did not settle: verified process was replaced (%d -> %d) after supervision confirmed it", pid, current)
		}
		if daemonEvidenceReady(snapshotPath, handoffPath, pid) {
			return true, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		timer := time.NewTimer(min(bootstrapInterval, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

// daemonEvidenceReady reports whether this exact process has published either
// of the two artifacts that prove it finished initializing: a fresh runtime
// snapshot, or an enrollment handoff still inside its validity window.
func daemonEvidenceReady(snapshotPath, handoffPath string, pid int) bool {
	snapshot, err := tsruntime.Load(snapshotPath)
	if err == nil && snapshot != nil && snapshot.DaemonPID == pid && snapshot.GlobalError == nil && time.Since(snapshot.UpdatedAt) >= 0 && time.Since(snapshot.UpdatedAt) < 30*time.Second {
		return true
	}
	handoff, err := loadAuthHandoff(handoffPath)
	return err == nil && handoff.DaemonPID == pid && handoff.ExpiresAt.After(time.Now())
}

// At least two consistent samples spanning settle are mandatory. Once a
// process has been seen, death, a changed PID, or a changed state fails the
// check, rather than blessing a crash/restart loop on its next lucky sample.
func waitStableDaemon(ctx context.Context, sample func() (int, error), timeout, interval, settle time.Duration) (int, error) {
	deadline := bootstrapNowFn().Add(timeout)
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
				firstGood = bootstrapNowFn()
			}
			previousPID = pid
			samples++
			if samples >= 2 && bootstrapNowFn().Sub(firstGood) >= settle {
				return pid, nil
			}
		}
		remaining := deadline.Sub(bootstrapNowFn())
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

// A detected launchd/systemd owner must include the restart policy promised by
// that platform. Windows Startup promises sign-in only, not crash restart.
func verifiedDaemonSupervision(s Supervision) bool {
	if !s.Installed || !s.Autostart {
		return false
	}
	switch s.Manager {
	case "launchd", "systemd":
		return s.RestartOnExit
	case "windows-startup":
		return supervisorName() == "windows-startup"
	default:
		return false
	}
}

// The lock key is the per-user supervisor definition, never the selected config.
// Hold it across capture, mutation, manager verification and rollback. Waiters
// may cancel without leaving a goroutine that later starts a stale transaction.
func withSupervisorTransaction(ctx context.Context, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := supervisorPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := openSupervisorLockFn(path+".bootstrap.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		acquired, err := trySupervisorLockFn(f)
		if err != nil {
			return err
		}
		if acquired {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer filelock.Unlock(f)
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
