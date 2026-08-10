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
	"unsafe"

	"golang.org/x/sys/windows"
)

// Seams for testing – overridden in tests to inject errors.
var (
	findProcess = os.FindProcess
	executable  = os.Executable
	execCommand = exec.Command
	startCmd    = func(cmd *exec.Cmd) error { return cmd.Start() }

	processExecutable = defaultProcessExecutable
	processStartTime  = defaultProcessStartTime
	processArguments  = defaultProcessArguments
)

const windowsStopTimeout = 5 * time.Second

var errProcessWaitTimeout = errors.New("process wait timed out")

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
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return processLivenessAbsent
		}
		// ERROR_ACCESS_DENIED (and other inconclusive inspection failures) means
		// the process may be alive; callers must fail closed.
		return processLivenessUnknown
	}
	_ = windows.CloseHandle(handle)
	return processLivenessAlive
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
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			RemovePID(pidPath)
			return nil
		}
		return fmt.Errorf("find process: %w", err)
	}
	defer proc.Release()

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			RemovePID(pidPath)
			return nil
		}
		return fmt.Errorf("inspect process %d: %w", pid, err)
	}
	_ = windows.CloseHandle(handle)

	if err := verifyProcessIdentity(pidPath, pid); err != nil {
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

	if err := waitForProcessExit(proc, windowsStopTimeout); err != nil {
		if errors.Is(err, errProcessWaitTimeout) {
			return fmt.Errorf("process %d did not exit after termination", pid)
		}
		return fmt.Errorf("confirm process %d exit: %w", pid, err)
	}

	RemovePID(pidPath)
	return nil
}

func waitForProcessExit(proc *os.Process, timeout time.Duration) error {
	var (
		result  uint32
		waitErr error
	)
	if err := proc.WithHandle(func(handle uintptr) {
		result, waitErr = windows.WaitForSingleObject(windows.Handle(handle), uint32(timeout/time.Millisecond))
	}); err != nil {
		return err
	}
	if waitErr != nil {
		return waitErr
	}
	switch result {
	case windows.WAIT_OBJECT_0:
		return nil
	case uint32(windows.WAIT_TIMEOUT):
		return errProcessWaitTimeout
	default:
		return fmt.Errorf("WaitForSingleObject returned unexpected result %d", result)
	}
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

func defaultProcessStartTime(pid int) (time.Time, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(handle)

	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, err
	}
	return time.Unix(0, creation.Nanoseconds()), nil
}

func defaultProcessArguments(pid int) ([]string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)

	var size uint32
	_ = windows.NtQueryInformationProcess(handle, windows.ProcessCommandLineInformation, nil, 0, &size)
	if size < uint32(unsafe.Sizeof(windows.NTUnicodeString{})) {
		return nil, fmt.Errorf("query process %d command line size returned %d bytes", pid, size)
	}
	buffer := make([]byte, size)
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessCommandLineInformation, unsafe.Pointer(&buffer[0]), size, &size); err != nil {
		return nil, err
	}
	commandLine := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	if commandLine.Buffer == nil || commandLine.Length == 0 || commandLine.Length%2 != 0 {
		return nil, fmt.Errorf("process %d returned an invalid command line", pid)
	}
	units := unsafe.Slice(commandLine.Buffer, int(commandLine.Length/2))
	return windows.DecomposeCommandLine(windows.UTF16ToString(units))
}
