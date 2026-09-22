//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// daemonLogFileMode is the mode the daemon's own stdout and stderr logs are
// created with, and narrowed to when they already exist.
//
// Owner-only rather than the 0644 these used to get, because of what is in
// them: the access log names every principal that reached a service, invite
// flows log recipient email addresses, and tsnet's authorization URL is a
// bearer link to the tailnet. A log directory created 0700 does not make the
// files inside it safe on its own -- a mode is the thing that survives the
// directory being opened up, a backup being restored, or the files being copied
// somewhere else.
const daemonLogFileMode os.FileMode = 0o600

// Seams for testing – overridden in tests to inject errors.
var (
	findProcess = os.FindProcess
	executable  = os.Executable
	execCommand = exec.Command
	startCmd    = func(cmd *exec.Cmd) error { return cmd.Start() }
	setUmask    = syscall.Umask
	chmodFile   = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }

	processExecutable = defaultProcessExecutable
	processStartTime  = defaultProcessStartTime
	processArguments  = defaultProcessArguments
)

// IsRunning reports whether the process referenced by path is alive.
func IsRunning(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}
	switch inspectProcessLiveness(pid) {
	case processLivenessAbsent:
		return false
	case processLivenessUnknown:
		return true
	}
	return identityVerifiedOrUnavailable(verifyProcessIdentity(path, pid))
}

// IsProcessRunning reports whether pid is alive and belongs to the TSLink
// product. Callers with a PID-file path should use IsRunning so the recorded
// process-instance identity is also checked.
func IsProcessRunning(pid int) bool {
	switch inspectProcessLiveness(pid) {
	case processLivenessAbsent:
		return false
	case processLivenessUnknown:
		return true
	}
	return identityVerifiedOrUnavailable(verifyProcessProduct(pid))
}

func inspectProcessLiveness(pid int) processLiveness {
	if pid <= 0 {
		return processLivenessAbsent
	}
	proc, err := findProcess(pid)
	if err != nil {
		return processLivenessUnknown
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			return processLivenessAbsent
		}
		if errors.Is(err, syscall.EPERM) {
			return processLivenessAlive
		}
		return processLivenessUnknown
	}

	return processLivenessAlive
}

// Daemonize re-launches the current binary in the background with the serve
// command. controlURL, manageACL, noAutoProvision and mcp are propagated to the
// child so the re-executed foreground `serve` observes the same opt-ins as the
// parent; a dropped --manage-acl or --mcp would silently disable the documented
// opt-in in daemon mode.
func Daemonize(outLog, errLog, controlURL string, manageACL, noAutoProvision, mcp bool) (int, error) {
	exe, err := executable()
	if err != nil {
		return 0, fmt.Errorf("find executable: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outLog), 0o700); err != nil {
		return 0, fmt.Errorf("create stdout log dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(errLog), 0o700); err != nil {
		return 0, fmt.Errorf("create stderr log dir: %w", err)
	}

	stdout, err := openDaemonLog(outLog, "stdout")
	if err != nil {
		return 0, err
	}

	stderr, err := openDaemonLog(errLog, "stderr")
	if err != nil {
		_ = stdout.Close()
		return 0, err
	}

	args := daemonServeArgs(controlURL, manageACL, noAutoProvision, mcp)

	cmd := execCommand(exe, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	oldUmask := setUmask(0o077)
	defer setUmask(oldUmask)
	startErr := startCmd(cmd)
	if startErr != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, fmt.Errorf("start daemon: %w", startErr)
	}

	pid := cmd.Process.Pid

	_ = cmd.Process.Release()
	_ = stdout.Close()
	_ = stderr.Close()

	return pid, nil
}

// openDaemonLog opens one of the daemon's log files for appending, creating it
// owner-only, and narrows it if it already exists with a wider mode.
//
// The chmod is the half that matters on an upgrade. O_CREATE applies its mode
// argument only when it creates the file, so a log left behind by a build that
// used 0644 would keep that mode for the rest of its life no matter what this
// call asks for; narrowing it here is what makes the fix apply to machines that
// already ran tslink rather than only to fresh ones.
//
// A chmod failure is logged and not returned. Refusing to start the daemon
// because a log file could not be tightened trades an exposed log for no
// service at all, and the operator cannot act on either without the daemon's
// own log, which is the file in question.
func openDaemonLog(path, label string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, daemonLogFileMode)
	if err != nil {
		return nil, fmt.Errorf("open %s log: %w", label, err)
	}
	// fchmod through the descriptor already held, not a second chmod by path:
	// the path could name a different file by now.
	if err := chmodFile(f, daemonLogFileMode); err != nil {
		slog.Warn("could not restrict daemon log file permissions", "stream", label, "path", path, "want_mode", daemonLogFileMode.String(), "error", err)
	}
	return f, nil
}

// StopDaemon sends SIGTERM to the daemon and waits up to 5 seconds for exit.
func StopDaemon(pidPath string) error {
	pid, err := ReadPID(pidPath)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}

	proc, err := findProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			RemovePID(pidPath)
			return nil
		}
		if !errors.Is(err, syscall.EPERM) {
			return fmt.Errorf("check process %d: %w", pid, err)
		}
	}

	if err := verifyProcessIdentity(pidPath, pid); err != nil {
		return fmt.Errorf("refusing to stop process from PID file: %w", err)
	}

	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			RemovePID(pidPath)
			return nil
		}
		return fmt.Errorf("signal SIGTERM to %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := proc.Signal(syscall.Signal(0))
		if err != nil && !errors.Is(err, syscall.EPERM) {
			RemovePID(pidPath)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("process %d did not exit after SIGTERM", pid)
}
