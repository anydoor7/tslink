//go:build !windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Seams for testing – overridden in tests to inject errors.
var (
	findProcess = os.FindProcess
	executable  = os.Executable
	execCommand = exec.Command
	startCmd    = func(cmd *exec.Cmd) error { return cmd.Start() }
	setUmask    = syscall.Umask

	processExecutable = defaultProcessExecutable
	processStartTime  = defaultProcessStartTime
)

// IsRunning reports whether the process referenced by path is alive.
func IsRunning(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}
	if !isProcessAlive(pid) {
		return false
	}
	return verifyProcessIdentity(path, pid) == nil
}

// IsProcessRunning reports whether pid is alive and belongs to the TSLink
// product. Callers with a PID-file path should use IsRunning so the recorded
// process-instance identity is also checked.
func IsProcessRunning(pid int) bool {
	if !isProcessAlive(pid) {
		return false
	}
	return verifyProcessProduct(pid) == nil
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := findProcess(pid)
	if err != nil {
		return false
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}

	return true
}

// Daemonize re-launches the current binary in the background with the serve
// command. controlURL and manageACL are propagated to the child so the
// re-executed foreground `serve` observes the same opt-ins as the parent; a
// dropped --manage-acl would silently disable the documented opt-in in daemon
// mode.
func Daemonize(outLog, errLog, controlURL string, manageACL bool) (int, error) {
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

	stdout, err := os.OpenFile(outLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open stdout log: %w", err)
	}

	stderr, err := os.OpenFile(errLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		_ = stdout.Close()
		return 0, fmt.Errorf("open stderr log: %w", err)
	}

	args := daemonServeArgs(controlURL, manageACL)

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

func defaultProcessExecutable(pid int) (string, error) {
	if path, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return path, nil
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", err
	}
	identity := strings.TrimSpace(string(out))
	if identity == "" {
		return "", fmt.Errorf("empty process identity")
	}
	return identity, nil
}

func defaultProcessStartTime(pid int) (time.Time, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC0")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	value := strings.Join(strings.Fields(string(out)), " ")
	if value == "" {
		return time.Time{}, fmt.Errorf("empty process start time")
	}
	started, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse process start time %q: %w", value, err)
	}
	return started, nil
}

func legacyProcessProductFallback(pid int, executablePath string) bool {
	if filepath.Base(executablePath) != "tslink" {
		return false
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	return len(fields) >= 2 && fields[1] == "serve"
}
