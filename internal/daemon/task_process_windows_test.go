//go:build windows

package daemon

import (
	"os"
	"testing"
)

func TestWindowsTaskProcessRequiresExecutableAndEngineAncestry(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pid := os.Getpid()
	if !IsTaskOwnedProcess(pid, []int{pid}, exe) {
		t.Fatal("real process positive control rejected")
	}
	if IsTaskOwnedProcess(pid, []int{pid}, exe+".other") {
		t.Fatal("foreign executable accepted")
	}
	if IsTaskOwnedProcess(pid, nil, exe) {
		t.Fatal("unrelated task engine accepted")
	}
	if IsTaskOwnedProcess(0, []int{pid}, exe) {
		t.Fatal("invalid process accepted")
	}
}
