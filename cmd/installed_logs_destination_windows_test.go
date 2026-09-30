//go:build windows

package cmd

import (
	"strings"
	"testing"
)

func TestWindowsRenderedLogDestinationReachesCLIReader(t *testing.T) {
	script := windowsStartupScript(`C:\TSLink\tslink.exe`, false)
	want := `shell.Environment("Process")("TSLINK_MANAGED_LOGS") = "1"`
	envPosition := strings.Index(script, want)
	runPosition := strings.Index(script, ".Run ")
	if envPosition < 0 || runPosition <= envPosition {
		t.Fatalf("rendered Startup script must enable managed logs before launch: %q", script)
	}
	t.Setenv("TSLINK_MANAGED_LOGS", "1")
	assertManagedDaemonLogsReachCLIReader(t)
}
