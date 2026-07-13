//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
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
	return IsProcessRunning(pid)
}

// IsProcessRunning reports whether pid is alive and matches the current executable.
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return verifyProcessIdentity(pid) == nil
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

	const createNewProcessGroup = 0x00000200
	const createNoWindow = 0x08000000

	args := daemonServeArgs(controlURL, manageACL)

	cmd := execCommand(exe, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}

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

// StopDaemon terminates the daemon process and waits up to 5 seconds for exit.
func StopDaemon(pidPath string) error {
	pid, err := ReadPID(pidPath)
	if err != nil {
		return fmt.Errorf("read PID: %w", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find process: %w", err)
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			RemovePID(pidPath)
			return nil
		}
		return fmt.Errorf("inspect process %d: %w", pid, err)
	}
	_ = windows.CloseHandle(handle)

	if err := verifyProcessIdentity(pid); err != nil {
		return fmt.Errorf("refusing to stop process from PID file: %w", err)
	}

	// On Windows there is no SIGTERM; use Kill (TerminateProcess).
	if err := proc.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			RemovePID(pidPath)
			return nil
		}
		return fmt.Errorf("terminate process %d: %w", pid, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if openErr != nil {
			// Process no longer exists.
			RemovePID(pidPath)
			return nil
		}
		_ = windows.CloseHandle(handle)
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("process %d did not exit after termination", pid)
}

func defaultProcessExecutable(pid int) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buffer[:size]), nil
}
