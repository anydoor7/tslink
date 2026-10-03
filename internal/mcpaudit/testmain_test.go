package mcpaudit_test

import (
	"os"
	"testing"

	"github.com/anydoor7/tslink/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m, nil)) }
