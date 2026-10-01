package cmd

import (
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// TestShareOfAReservedDeviceNameGetsAUsableName keeps `tslink share ./aux`
// working now that aux is not a valid service name.
func TestShareOfAReservedDeviceNameGetsAUsableName(t *testing.T) {
	for base, want := range map[string]string{"aux": "aux-share", "COM1": "com1-share", "console": "console"} {
		got := sanitizeShareName(base)
		if got != want || registry.ValidateName(got) != nil {
			t.Fatalf("sanitizeShareName(%q) = %q (valid: %v), want %q", base, got, registry.ValidateName(got), want)
		}
	}
}
