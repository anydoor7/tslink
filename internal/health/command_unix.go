//go:build unix

package health

import (
	"os"
	"os/exec"
	"syscall"
)

func configureNotifierCommand(cmd *exec.Cmd) func() {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = kill
	// Also reap descendants left by an already-exited direct command. A
	// descendant deliberately moving to another process group can escape.
	return func() { _ = kill() }
}
