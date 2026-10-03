//go:build windows

package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsShutdownEventCancelsContextAndRestrictsAccess(t *testing.T) {
	ctx, cleanup, err := ShutdownContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	name, err := shutdownEventName(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	wide, _ := windows.UTF16PtrFromString(name)
	h, err := windows.OpenEvent(windows.READ_CONTROL|windows.EVENT_MODIFY_STATE, false, wide)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;" + u.User.Sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	mapped, _ := windows.SecurityDescriptorFromString("D:P(A;;0x1f0003;;;SY)(A;;0x1f0003;;;" + u.User.Sid.String() + ")")
	if sd.String() != want.String() && sd.String() != mapped.String() {
		t.Fatalf("event DACL=%s,want current user and SYSTEM only", sd.String())
	}
	if err := windows.SetEvent(h); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown event did not cancel context")
	}
	cleanup()
	cleanup() // idempotence and join-before-close
}

func TestWindowsShutdownEventRejectsPreexistingEvent(t *testing.T) {
	_, cleanup, err := ShutdownContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	_, second, err := ShutdownContext(context.Background())
	if second != nil {
		second()
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		t.Fatalf("preexisting event not rejected: %v", err)
	}
}

func TestWindowsGracefulStopWithoutListenerPreservesProcessAndPID(t *testing.T) {
	// This is the test process itself, with identity supplied by the existing
	// test seams. No shutdown event is registered; a Kill mutation kills this
	// binary and turns the native Windows test run red.
	stubProcessExecutableForPID(t, os.Getpid())
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := StopDaemon(path)
	if err == nil || !strings.Contains(err.Error(), "open shutdown event") {
		t.Fatalf("missing listener error=%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("inconclusive stop discarded PID evidence")
	}
}
