//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SupervisorInstance is separate from the daemon's serve-only PID identity.
type SupervisorInstance struct {
	PID           int    `json:"pid"`
	StartUnixNano int64  `json:"start_unix_nano"`
	Executable    string `json:"executable"`
}

func CurrentSupervisorInstance() (SupervisorInstance, error) {
	exe, err := os.Executable()
	if err != nil {
		return SupervisorInstance{}, err
	}
	started, err := defaultProcessStartTime(os.Getpid())
	return SupervisorInstance{os.Getpid(), started.UnixNano(), exe}, err
}

// SupervisorAlive never grants ownership on unavailable identity. PID reuse,
// another TSLink subcommand, and a different binary all fail closed.
func SupervisorAlive(instance SupervisorInstance) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(instance.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) && instance.PID > 0 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(handle)
	return supervisorAliveFromHandle(instance, handle)
}

func supervisorAliveFromHandle(instance SupervisorInstance, handle windows.Handle) (bool, error) {
	if instance.PID <= 0 || instance.StartUnixNano <= 0 || instance.Executable == "" {
		return false, fmt.Errorf("invalid supervisor identity")
	}
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, err
	}
	if result == windows.WAIT_OBJECT_0 {
		return false, nil
	}
	started, err := processStartTimeFromHandle(handle)
	if err != nil || started.UnixNano() != instance.StartUnixNano {
		return false, fmt.Errorf("supervisor start identity unverified")
	}
	exe, err := processExecutableFromHandle(handle)
	if err != nil || !strings.EqualFold(exe, instance.Executable) {
		return false, fmt.Errorf("supervisor executable unverified")
	}
	info, err := readExecutableBuildInfo(exe)
	if err != nil || info.Main.Path != processProductID {
		return false, fmt.Errorf("supervisor product unverified")
	}
	args, err := processArgumentsFromHandle(handle)
	if err != nil || len(args) < 2 || args[1] != "supervise" ||
		(len(args) != 2 && (len(args) != 3 || args[2] != "--no-auto-provision")) {
		return false, fmt.Errorf("supervisor command unverified")
	}
	return true, nil
}

func StopSupervisor(instance SupervisorInstance) error {
	return stopSupervisor(instance, nil)
}

// afterVerified is a scheduling seam, passed synchronously rather than read
// from a mutable global. The handle spans validation, signaling and waiting.
func stopSupervisor(instance SupervisorInstance, afterVerified func(windows.Handle)) error {
	if instance.PID <= 0 || instance.StartUnixNano <= 0 || instance.Executable == "" {
		return fmt.Errorf("invalid supervisor identity")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(instance.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	alive, err := supervisorAliveFromHandle(instance, handle)
	if err != nil || !alive {
		return err
	}
	if afterVerified != nil {
		afterVerified(handle)
	}
	if err := requestGracefulWindowsStopInstance(instance.PID, instance.StartUnixNano); err != nil {
		return err
	}
	return waitForWindowsHandleExit(handle, 10*time.Second)
}

// ChildJob prevents an unexpected supervisor exit from orphaning its children.
// It is unnamed, non-inheritable, and never includes a pre-existing process.
type ChildJob struct{ handle windows.Handle }

func NewChildJob() (*ChildJob, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(handle, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		windows.CloseHandle(handle)
		return nil, err
	}
	return &ChildJob{handle: handle}, nil
}

func (job *ChildJob) Close() { _ = windows.CloseHandle(job.handle) }

func (job *ChildJob) Start(command *exec.Cmd) (SupervisorChild, error) {
	return job.start(command, nil)
}

func (job *ChildJob) start(command *exec.Cmd, afterCreated func(uint32)) (SupervisorChild, error) {
	proc, err := startJobProcess(job.handle, command, afterCreated)
	if err != nil {
		return SupervisorChild{}, err
	}
	waitDone := make(chan struct{})
	return SupervisorChild{PID: proc.Pid, Wait: func() error {
		state, err := proc.Wait()
		close(waitDone)
		if err == nil && !state.Success() {
			err = &exec.ExitError{ProcessState: state}
		}
		return err
	}, Stop: func() error { return stopJobProcess(proc, waitDone, nil) }}, nil
}

func stopJobProcess(proc *os.Process, waitDone <-chan struct{}, afterPinned func(windows.Handle)) error {
	var stopErr error
	err := proc.WithHandle(func(h uintptr) {
		handle := windows.Handle(h)
		if afterPinned != nil {
			afterPinned(handle)
		}
		started, err := processStartTimeFromHandle(handle)
		if err != nil {
			stopErr = err
			return
		}
		// The child may still be initializing before its shutdown event exists.
		// Hold its real process handle throughout; no PID-file or PID-reuse race.
		deadline := time.Now().Add(5 * time.Second)
		for {
			result, err := windows.WaitForSingleObject(handle, 0)
			if err != nil || result == windows.WAIT_OBJECT_0 {
				stopErr = err
				return
			}
			err = requestGracefulWindowsStopInstance(proc.Pid, started.UnixNano())
			if err == nil {
				stopErr = waitForWindowsHandleExit(handle, windowsStopTimeout)
				return
			}
			if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || time.Now().After(deadline) {
				stopErr = fmt.Errorf("direct child graceful stop unavailable: %w", err)
				return
			}
		}
	})
	// Only our Wait owns release of this process. If WithHandle cannot acquire
	// it, Wait has released it; join the completion notification rather than
	// depending on Go's unexported, version-dependent "released" error. No PID
	// lookup is permitted in either ordering of Wait and Stop.
	if err != nil {
		<-waitDone
		return nil
	}
	return stopErr
}

func waitForWindowsHandleExit(handle windows.Handle, timeout time.Duration) error {
	result, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	if result == windows.WAIT_OBJECT_0 {
		return nil
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return errProcessWaitTimeout
	}
	return fmt.Errorf("WaitForSingleObject returned unexpected result %d", result)
}
