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

	processExecutable = defaultProcessExecutable
)

// IsRunning reports whether the process referenced by path is alive.
func IsRunning(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}

	proc, err := findProcess(pid)
	if err != nil {
		return false
	}

	if err := proc.Signal(syscall.Signal(0)); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}

	return verifyProcessIdentity(pid) == nil
}

// Daemonize re-launches the current binary in the background with the serve command.
func Daemonize(outLog, errLog, controlURL string) (int, error) {
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

	args := []string{"serve"}
	if controlURL != "" {
		args = append(args, "--control-url", controlURL)
	}

	cmd := execCommand(exe, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := startCmd(cmd); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, fmt.Errorf("start daemon: %w", err)
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

	if err := verifyProcessIdentity(pid); err != nil {
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
