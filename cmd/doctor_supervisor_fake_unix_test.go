//go:build !windows

package cmd

import (
	"context"
	"testing"
)

// fakeSupervisorHoldsProcess answers the owner doctor's supervisor scope probe
// through the launchd/systemd manager seam: a held process reports pid 777 in
// both launchctl print and systemctl show forms.
func fakeSupervisorHoldsProcess(t *testing.T, holds bool) {
	t.Helper()
	old := managerOutputFn
	t.Cleanup(func() { managerOutputFn = old })
	managerOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		if holds {
			return []byte("pid = 777\nMainPID=777\n"), nil
		}
		return nil, nil
	}
}
