package cmd

import (
	"strings"
	"testing"
)

func TestStopHelpDocumentsWindowsForcedTermination(t *testing.T) {
	stopCmd, _, err := rootCmd.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("find stop command: %v", err)
	}

	help := stopCmd.Long
	if strings.Contains(help, "Windows). The daemon shuts down all") {
		t.Fatalf("stop help still claims graceful shutdown on Windows: %s", help)
	}
	if !strings.Contains(help, "Windows") || !strings.Contains(help, "not graceful") {
		t.Fatalf("stop help = %q, want explicit Windows non-graceful caveat", help)
	}
}
