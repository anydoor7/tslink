//go:build linux

package cmd

import (
	"strings"
	"testing"
)

func TestSystemdRenderedLogDestinationReachesCLIReader(t *testing.T) {
	unit := systemdServiceContents("/tmp/tslink", false)
	for _, line := range strings.Split(unit, "\n") {
		if value, ok := strings.CutPrefix(line, "Environment=TSLINK_MANAGED_LOGS="); ok {
			if value != "1" {
				t.Fatalf("rendered unit must enable managed logs, got %q", value)
			}
			t.Setenv("TSLINK_MANAGED_LOGS", value)
			assertManagedDaemonLogsReachCLIReader(t)
			return
		}
	}
	t.Fatal("rendered systemd unit must select the logs reader destination")
}
