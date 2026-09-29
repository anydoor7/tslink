//go:build !windows

package cmd

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// fakeE2EProcessTable serves listing and records every pid whose executable
// is read.
func fakeE2EProcessTable(listing []e2eProcess, commandMax int, read *[]int) e2eProcessTable {
	return e2eProcessTable{
		list: func() ([]e2eProcess, error) { return listing, nil },
		executable: func(pid int) (string, error) {
			*read = append(*read, pid)
			return fmt.Sprintf("/tmp/tslink-bin-run/%d/tslink", pid), nil
		},
		commandMax: commandMax,
	}
}

// TestE2EProcessDiscoveryReadsOnlyProcessesStartedAfterThisOne pins what
// e2eLivePIDsForBinary may read on a contributor's machine. Next to this test
// process the listing holds the contributor's own tslink daemon (same name,
// started earlier), a same-named process from the same clock tick as this
// one, a process this test could have started from its binary, and a later
// process with another name. Only the candidate may be path-read, and it must
// be found.
func TestE2EProcessDiscoveryReadsOnlyProcessesStartedAfterThisOne(t *testing.T) {
	self := os.Getpid()
	const cutoff = 1_000_000
	olderDaemon, sameTick, candidate, otherName := self+1, self+2, self+3, self+4
	listing := []e2eProcess{
		{PID: olderDaemon, Command: "tslink", Start: cutoff - 1},
		{PID: sameTick, Command: "tslink", Start: cutoff},
		{PID: self, Command: "cmd.test", Start: cutoff},
		{PID: candidate, Command: "tslink", Start: cutoff + 1},
		{PID: otherName, Command: "zsh", Start: cutoff + 2},
	}
	var read []int

	got, err := e2eCandidateExecutables(fakeE2EProcessTable(listing, 15, &read), "tslink")
	if err != nil {
		t.Fatalf("e2eCandidateExecutables() error = %v", err)
	}
	if !slices.Equal(read, []int{candidate}) {
		t.Fatalf("executables read for pids %v, want only the candidate %d; the daemon that started before this process (%d) and the process from its clock tick (%d) must never be read, nor a process with another name (%d)",
			read, candidate, olderDaemon, sameTick, otherName)
	}
	want := map[int]string{candidate: fmt.Sprintf("/tmp/tslink-bin-run/%d/tslink", candidate)}
	if !maps.Equal(got, want) {
		t.Fatalf("e2eCandidateExecutables() = %v, want %v", got, want)
	}
}

// TestE2EProcessDiscoveryMatchesTheCommandNameAsTheOSCutsIt: the kernel keeps
// only the first commandMax bytes of a command name, so a binary with a longer
// base name is matched by that prefix, and a shorter name is not a match.
func TestE2EProcessDiscoveryMatchesTheCommandNameAsTheOSCutsIt(t *testing.T) {
	self := os.Getpid()
	const cutoff = 1_000_000
	longName, shortName := self+1, self+2
	listing := []e2eProcess{
		{PID: self, Command: "cmd.test", Start: cutoff},
		{PID: longName, Command: "tslink-secondar", Start: cutoff + 1},
		{PID: shortName, Command: "tslink", Start: cutoff + 1},
	}
	var read []int

	got, err := e2eCandidateExecutables(fakeE2EProcessTable(listing, 15, &read), "tslink-secondary-install")
	if err != nil {
		t.Fatalf("e2eCandidateExecutables() error = %v", err)
	}
	if !slices.Equal(read, []int{longName}) || len(got) != 1 {
		t.Fatalf("executables read for pids %v (found %v), want only %d, whose command name is the binary's name cut to 15 bytes", read, got, longName)
	}
}

// TestE2EProcessDiscoveryRefusesAListingWithoutThisProcess: a listing that
// does not even hold this process is not trusted to hold the ones the test
// started, and nothing in it is read.
func TestE2EProcessDiscoveryRefusesAListingWithoutThisProcess(t *testing.T) {
	self := os.Getpid()
	listing := []e2eProcess{{PID: self + 1, Command: "tslink", Start: 2}}
	var read []int

	got, err := e2eCandidateExecutables(fakeE2EProcessTable(listing, 15, &read), "tslink")
	if err == nil || len(read) != 0 {
		t.Fatalf("e2eCandidateExecutables() = %v, %v after reading pids %v; want an error and no read", got, err, read)
	}
}

// TestE2EProcessDiscoveryFindsAChildWhoseNameTheOSCuts runs the real listing
// of this platform against a child this test starts from a binary whose name
// is longer than any kernel command name: it must be found by its start time
// and cut name, with the path it was started from.
func TestE2EProcessDiscoveryFindsAChildWhoseNameTheOSCuts(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("no sleep on PATH to copy into a long-named child binary: %v", err)
	}
	image, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatalf("read %s: %v", sleep, err)
	}
	binary := filepath.Join(t.TempDir(), "tslink-e2e-process-discovery-probe")
	if err := os.WriteFile(binary, image, 0o700); err != nil {
		t.Fatalf("write %s: %v", binary, err)
	}
	child := exec.Command(binary, "60")
	if err := child.Start(); err != nil {
		t.Fatalf("start %s: %v", binary, err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	resolved := e2eRequireTempPath(t, binary)

	got, err := e2eCandidateExecutables(e2eHostProcessTable, filepath.Base(binary))
	if err != nil {
		t.Fatalf("e2eCandidateExecutables() error = %v", err)
	}
	if path, ok := got[child.Process.Pid]; !ok || (path != binary && path != resolved) {
		t.Fatalf("e2eCandidateExecutables() = %v, want pid %d started from %s", got, child.Process.Pid, binary)
	}
}
