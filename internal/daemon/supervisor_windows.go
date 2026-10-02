//go:build windows

package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
	if instance.PID <= 0 || instance.StartUnixNano <= 0 || instance.Executable == "" {
		return false, fmt.Errorf("invalid supervisor identity")
	}
	if inspectProcessLiveness(instance.PID) == processLivenessAbsent {
		return false, nil
	}
	started, err := defaultProcessStartTime(instance.PID)
	if err != nil || started.UnixNano() != instance.StartUnixNano {
		return false, fmt.Errorf("supervisor start identity unverified")
	}
	exe, err := defaultProcessExecutable(instance.PID)
	if err != nil || !strings.EqualFold(exe, instance.Executable) {
		return false, fmt.Errorf("supervisor executable unverified")
	}
	info, err := readExecutableBuildInfo(exe)
	if err != nil || info.Main.Path != processProductID {
		return false, fmt.Errorf("supervisor product unverified")
	}
	args, err := defaultProcessArguments(instance.PID)
	if err != nil || len(args) < 2 || args[1] != "supervise" ||
		(len(args) != 2 && (len(args) != 3 || args[2] != "--no-auto-provision")) {
		return false, fmt.Errorf("supervisor command unverified")
	}
	return true, nil
}

func StopSupervisor(instance SupervisorInstance) error {
	alive, err := SupervisorAlive(instance)
	if err != nil || !alive {
		return err
	}
	proc, err := os.FindProcess(instance.PID)
	if err != nil {
		return err
	}
	defer proc.Release()
	if err := requestGracefulWindowsStop(instance.PID); err != nil {
		return err
	}
	return waitForProcessExit(proc, 10*time.Second)
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
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := command.Start(); err != nil {
		return SupervisorChild{}, err
	}
	var assignErr error
	err := command.Process.WithHandle(func(h uintptr) {
		assignErr = windows.AssignProcessToJobObject(job.handle, windows.Handle(h))
	})
	if err != nil || assignErr != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return SupervisorChild{}, fmt.Errorf("assign child job: %w", errors.Join(err, assignErr))
	}
	pid := command.Process.Pid
	return SupervisorChild{PID: pid, Wait: command.Wait, Stop: func() error {
		// The child may still be initializing before its shutdown event exists.
		// Hold its real process handle throughout; no PID-file or PID-reuse race.
		deadline := time.Now().Add(5 * time.Second)
		for {
			if inspectProcessLiveness(pid) == processLivenessAbsent {
				return nil
			}
			err := requestGracefulWindowsStop(pid)
			if err == nil {
				waitErr := waitForProcessExit(command.Process, windowsStopTimeout)
				if errors.Is(waitErr, os.ErrProcessDone) {
					return nil
				}
				return waitErr
			}
			if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || time.Now().After(deadline) {
				return fmt.Errorf("direct child graceful stop unavailable")
			}
		}
	}}, nil
}
