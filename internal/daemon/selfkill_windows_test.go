//go:build windows

package daemon

import (
	"context"
	"os"
)

func testHelperShutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel, err := ShutdownContext(context.Background())
	if err != nil {
		os.Exit(2)
	}
	return ctx, cancel
}

// selfKill hard-kills the current process to simulate an abnormal daemon exit
// during startup. Windows has no syscall.Kill/SIGKILL; os.Process.Kill maps to
// TerminateProcess, the closest abnormal-termination equivalent.
func selfKill() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Kill()
	}
	os.Exit(137) // fallback if Kill returns
}
