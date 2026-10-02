//go:build windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys does not yet name this Windows 10+ attribute (JobList, number 13).
const procThreadAttributeJobList = 0x0002000d

// startJobProcess implements the supervisor's file-only stdio contract. Job
// membership is established by CreateProcess, before the child can execute.
// Only duplicated standard handles are inheritable; the job never is. The
// returned os.Process owns a separate handle before the creation handle closes.
// Its caller waits with Process.Wait, not Cmd.Wait (Cmd.Start was not called).
func startJobProcess(job windows.Handle, command *exec.Cmd, afterCreated func(uint32)) (*os.Process, error) {
	if command.Err != nil {
		return nil, command.Err
	}
	if command.Process != nil || command.Cancel != nil || command.SysProcAttr != nil {
		return nil, fmt.Errorf("unsupported supervisor process attributes or already started command")
	}
	path := command.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(command.Dir, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	application, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	line, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		return nil, err
	}
	var dir *uint16
	if command.Dir != "" {
		dir, err = windows.UTF16PtrFromString(command.Dir)
		if err != nil {
			return nil, err
		}
	}
	// Environ applies exec.Cmd's inherited-environment and case-insensitive
	// duplicate-key semantics, including the mandatory Windows SYSTEMROOT.
	env := command.Environ()
	var block []uint16
	for _, entry := range env {
		wide, err := windows.UTF16FromString(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid supervisor environment entry")
		}
		block = append(block, wide...)
	}
	block = append(block, 0)
	if len(block) == 1 {
		block = append(block, 0)
	}

	var inherited [3]windows.Handle
	defer func() {
		for _, handle := range inherited {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
		}
	}()
	for i, stream := range []any{command.Stdin, command.Stdout, command.Stderr} {
		file, ok := stream.(*os.File)
		if stream != nil && !ok {
			return nil, fmt.Errorf("supervisor stdio must be files")
		}
		if stream == nil {
			flag := os.O_WRONLY
			if i == 0 {
				flag = os.O_RDONLY
			}
			file, err = os.OpenFile(os.DevNull, flag, 0)
			if err != nil {
				return nil, err
			}
			defer file.Close()
		}
		if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(file.Fd()), windows.CurrentProcess(), &inherited[i], 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
	}
	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	if err := attrs.Update(procThreadAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job)); err != nil {
		return nil, fmt.Errorf("create child job attribute: %w", err)
	}
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherited[0]), unsafe.Sizeof(inherited)); err != nil {
		return nil, err
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = inherited[0], inherited[1], inherited[2]
	var info windows.ProcessInformation
	flags := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := windows.CreateProcess(application, line, nil, nil, true, flags, &block[0], dir, &startup.StartupInfo, &info); err != nil {
		return nil, fmt.Errorf("create child in job: %w", err)
	}
	// Keep file and attribute backing storage alive until CreateProcess returns.
	runtime.KeepAlive(command)
	runtime.KeepAlive(attrs)
	defer windows.CloseHandle(info.Thread)
	defer windows.CloseHandle(info.Process)
	if afterCreated != nil {
		afterCreated(info.ProcessId)
	}
	proc, err := os.FindProcess(int(info.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(info.Process, 1)
		_ = waitForWindowsHandleExit(info.Process, windowsStopTimeout)
		return nil, err
	}
	command.Process = proc
	return proc, nil
}
