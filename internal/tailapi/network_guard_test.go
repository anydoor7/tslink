package tailapi

import (
	"os"
	"testing"

	"github.com/anydoor7/tslink/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m, func() int {
		return testenv.RunWithNonLoopbackDialGuard(m.Run, "internal/tailapi")
	}))
}
