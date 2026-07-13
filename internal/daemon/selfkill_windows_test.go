//go:build windows

package daemon

import "os"

// selfKill hard-kills the current process to simulate an abnormal daemon exit
// during startup. Windows has no syscall.Kill/SIGKILL; os.Process.Kill maps to
// TerminateProcess, the closest abnormal-termination equivalent.
func selfKill() {
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Kill()
	}
	os.Exit(137) // fallback if Kill returns
}
