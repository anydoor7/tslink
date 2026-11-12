//go:build windows

package cmd

import (
	"context"
	"testing"
)

// fakeSupervisorHoldsProcess answers the owner doctor's supervisor scope probe
// through the Task Scheduler seam: a held process is a running task (state 4)
// with an engine; otherwise the task does not exist.
func fakeSupervisorHoldsProcess(t *testing.T, holds bool) {
	t.Helper()
	old := windowsSchedulerFn
	t.Cleanup(func() { windowsSchedulerFn = old })
	windowsSchedulerFn = func(context.Context, string, string, []byte) (windowsSchedulerStatus, error) {
		if holds {
			return windowsSchedulerStatus{Exists: true, Enabled: true, State: 4, Engines: []int{777}}, nil
		}
		return windowsSchedulerStatus{}, nil
	}
}
