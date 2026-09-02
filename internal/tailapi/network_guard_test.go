package tailapi

import (
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithNonLoopbackDialGuard(m.Run, "internal/tailapi"))
}
