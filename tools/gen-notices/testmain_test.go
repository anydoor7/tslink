package main

import (
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m, nil))
}
