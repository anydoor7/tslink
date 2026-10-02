//go:build windows

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func shutdownEventName(pid int) (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	started, err := defaultProcessStartTime(pid)
	if err != nil {
		return "", err
	}
	// Bind to the real process instance, not a reusable PID. Global events need
	// no SeCreateGlobalPrivilege (that requirement is for file mappings/symlinks).
	return fmt.Sprintf(`Global\TSLink-stop-%s-%d-%d`, u.User.Sid.String(), pid, started.UnixNano()), nil
}

// ShutdownContext turns a user-restricted named event into context cancellation.
// It shares the normal tsnet cleanup path with SIGINT/SIGTERM and needs no
// console attachment, so detached and Task Scheduler processes both work.
func ShutdownContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	name, err := shutdownEventName(os.Getpid())
	if err != nil {
		return nil, nil, err
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + u.User.Sid.String() + ")")
	if err != nil {
		return nil, nil, err
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, nil, err
	}
	handle, err := windows.CreateEvent(&attrs, 1, 0, wide)
	if err != nil {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
		return nil, nil, fmt.Errorf("create shutdown event: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = windows.WaitForSingleObject(handle, windows.INFINITE)
		cancel()
	}()
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			_ = windows.SetEvent(handle)
			<-done
			_ = windows.CloseHandle(handle)
		})
	}
	return ctx, cleanup, nil
}

func requestGracefulWindowsStop(pid int) error {
	name, err := shutdownEventName(pid)
	if err != nil {
		return err
	}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	handle, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, wide)
	// The PID is published just before serve installs its shutdown listener.
	// Bound that startup race without treating an older daemon as killable.
	deadline := time.Now().Add(500 * time.Millisecond)
	for errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		handle, err = windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, wide)
	}
	if err != nil {
		return fmt.Errorf("open shutdown event (older daemons must be stopped separately before upgrade): %w", err)
	}
	defer windows.CloseHandle(handle)
	return windows.SetEvent(handle)
}
