//go:build !windows

package daemon

import (
	"context"
	"os"
	"syscall"
)

func testHelperShutdownContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// selfKill hard-kills the current process to simulate an abnormal daemon exit
// during startup. On Unix this is SIGKILL to self.
func selfKill() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}
