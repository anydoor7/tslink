//go:build !unix

package health

import "os/exec"

func configureNotifierCommand(_ *exec.Cmd) func() {
	// Windows has no Unix process groups. CommandContext kills the direct
	// process; null output handles and WaitDelay bound inherited-pipe waits.
	return func() {}
}
