//go:build linux

package cmd

import "testing"

// TestSystemctlSeamIsBoundedAgainstABlockedManager pins the systemctl seam's
// default implementation to runBoundedManagerCommand. A seam reverted to an
// unbounded CombinedOutput call keeps every fixture-driven test green,
// because those tests never reach the default; this one does.
func TestSystemctlSeamIsBoundedAgainstABlockedManager(t *testing.T) {
	installBlockingManagerShim(t, "systemctl")
	requireSeamReturnsWithinBudget(t, "systemctl", func() ([]byte, error) {
		return systemctlCombinedOutput("--user", "show", "tslink-bounded-seam-probe.invalid.service")
	})
}
