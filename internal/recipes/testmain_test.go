package recipes

import (
	"github.com/anydoor7/tslink/internal/testenv"
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m, nil)) }
