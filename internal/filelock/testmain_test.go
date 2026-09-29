package filelock_test

import (
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/testenv"
)

// TestMain is in the external test package because testenv locks its roots
// with this package.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m, nil))
}
