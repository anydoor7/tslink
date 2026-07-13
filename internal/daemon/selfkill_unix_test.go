//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// selfKill hard-kills the current process to simulate an abnormal daemon exit
// during startup. On Unix this is SIGKILL to self.
func selfKill() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
}
