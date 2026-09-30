package registry

import (
	"strings"
	"testing"
)

// TestValidateNameRefusesWindowsReservedDeviceNames: a service name is also a
// directory and file name (nodes/<name>, node-identities/<name>.json), and
// these names are Windows devices, so a registry written on macOS or Linux
// with one of them fails on Windows. They are refused on every platform.
func TestValidateNameRefusesWindowsReservedDeviceNames(t *testing.T) {
	reserved := []string{"con", "prn", "aux", "nul"}
	for digit := '1'; digit <= '9'; digit++ {
		reserved = append(reserved, "com"+string(digit), "lpt"+string(digit))
	}
	for _, name := range reserved {
		err := ValidateName(name)
		if code, _ := ErrorCode(err); code != CodeInvalidServiceName {
			t.Fatalf("ValidateName(%q) = %v, want %s", name, err, CodeInvalidServiceName)
		}
		if !strings.Contains(err.Error(), "Windows") {
			t.Fatalf("ValidateName(%q) = %v, want the reason named", name, err)
		}
	}
	// Names that only contain or extend a device name are ordinary files.
	for _, name := range []string{"console", "com10", "com0", "lpt", "auxiliary", "nul-svc", "my-aux", "prn2"} {
		if err := ValidateName(name); err != nil {
			t.Fatalf("ValidateName(%q) = %v, want nil", name, err)
		}
	}
}
