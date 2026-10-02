//go:build windows

package cmd

import "github.com/anydoor7/tslink/internal/testenv"

// The COM transport executes absolute PowerShell paths, so a PATH shim cannot
// guard it. Every test must explicitly provide a scheduler fake.
func osServiceManagerSeams() []testenv.ServiceManagerSeam {
	windowsSchedulerFn = func(operation string, _ string, _ []byte) (windowsSchedulerStatus, error) {
		if operation == "query" {
			return windowsSchedulerStatus{}, nil
		}
		return windowsSchedulerStatus{}, testenv.UnfakedHostSeam("cmd.windowsSchedulerFn")
	}
	return nil
}
