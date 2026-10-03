//go:build darwin

package cmd

import (
	"context"
	"testing"
)

// TestLaunchctlSeamIsBoundedAgainstABlockedManager pins the launchctl seam's
// default implementation to runBoundedManagerCommand. A seam reverted to an
// unbounded CombinedOutput call keeps every fixture-driven test green,
// because those tests never reach the default; this one does.
func TestLaunchctlSeamIsBoundedAgainstABlockedManager(t *testing.T) {
	installBlockingManagerShim(t, "launchctl")
	requireSeamReturnsWithinBudget(t, "launchctl", func() ([]byte, error) {
		return launchctlCombinedOutput(context.Background(), "print", launchctlDomain()+"/com.tslink.bounded-seam-probe.invalid")
	})
}
