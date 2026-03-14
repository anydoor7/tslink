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

// IsRunning reports whether the process referenced by path is alive.
func IsRunning(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}

// Daemonize re-launches the current binary in the background with the serve command.
func Daemonize(outLog, errLog string) (int, error) {
	exe, err := os.Executable()
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

	cmd := exec.Command(exe, "serve")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup | createNoWindow,
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, fmt.Errorf("start daemon: %w", err)
	}

	pid := cmd.Process.Pid

	time.Sleep(200 * time.Millisecond)

	// On Windows, check if the process is still alive by opening its handle.
	handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if openErr != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return 0, fmt.Errorf("daemon exited during startup: %w", openErr)
	}
	_ = windows.CloseHandle(handle)

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
