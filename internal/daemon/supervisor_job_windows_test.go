//go:build windows

package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestSupervisorWindowsJobKillsOwnedChildAndPreservesParent(t *testing.T) {
	job, err := NewChildJob()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSupervisorProcessFixture$")
	// Seed count so this helper stabilizes instead of exiting immediately.
	root := t.TempDir()
	if err := os.WriteFile(root+"/count", []byte("2"), 0600); err != nil {
		t.Fatal(err)
	}
	command.Env = append(os.Environ(), "TSLINK_SUPERVISOR_HELPER=1", "TSLINK_SUPERVISOR_HELPER_DIR="+root)
	child, err := job.Start(command)
	if err != nil {
		job.Close()
		t.Fatal(err)
	}
	defer command.Process.Kill()
	jobClosed := false
	defer func() {
		if !jobClosed {
			job.Close()
		}
	}()
	testwait.Until(t, "owned child stabilized", func() bool { _, err := os.Stat(root + "/stable"); return err == nil })
	job.Close()
	jobClosed = true
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	// Closing a Windows job can terminate a process with exit code zero.
	// Reaping the previously infinite helper proves termination.
	testwait.Recv(t, done, "job close terminated the owned child")
	if inspectProcessLiveness(os.Getpid()) != processLivenessAlive {
		t.Fatal("job affected parent")
	}
}

func TestSupervisorWindowsIdentityRejectsOtherCommand(t *testing.T) {
	instance, err := CurrentSupervisorInstance()
	if err != nil {
		t.Fatal(err)
	}
	if alive, err := SupervisorAlive(instance); alive || err == nil {
		t.Fatalf("test command accepted as supervisor: %t,%v", alive, err)
	}
	if alive, err := SupervisorAlive(SupervisorInstance{}); alive || err == nil {
		t.Fatalf("invalid instance accepted: %t,%v", alive, err)
	}
}

func TestSupervisorWindowsJobTreeFixture(t *testing.T) {
	role := os.Getenv("TSLINK_JOB_TREE_HELPER")
	if role == "" {
		return
	}
	root := os.Getenv("TSLINK_JOB_TREE_DIR")
	if role == "parent" {
		// Start descendants only after assignment to the supervisor's job.
		for {
			if _, err := os.Stat(filepath.Join(root, "assigned")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestSupervisorWindowsJobTreeFixture$")
		child.Env = append(os.Environ(), "TSLINK_JOB_TREE_HELPER=leaf")
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		child.Process.Release()
	} else {
		if err := os.WriteFile(filepath.Join(root, "leaf.pid"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(8)
		}
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestSupervisorWindowsJobReclaimsDescendants(t *testing.T) {
	root := t.TempDir()
	job, err := NewChildJob()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestSupervisorWindowsJobTreeFixture$")
	command.Env = append(os.Environ(), "TSLINK_JOB_TREE_HELPER=parent", "TSLINK_JOB_TREE_DIR="+root)
	child, err := job.Start(command)
	if err != nil {
		job.Close()
		t.Fatal(err)
	}
	defer command.Process.Kill()
	if err := os.WriteFile(filepath.Join(root, "assigned"), []byte("assigned"), 0600); err != nil {
		job.Close()
		t.Fatal(err)
	}
	leafPID := 0
	jobClosed := false
	defer func() {
		if !jobClosed {
			job.Close()
		}
	}()
	testwait.Until(t, "descendant positive control", func() bool {
		data, _ := os.ReadFile(filepath.Join(root, "leaf.pid"))
		leafPID, _ = strconv.Atoi(string(data))
		return leafPID > 0
	})
	leaf, err := os.FindProcess(leafPID)
	if err != nil {
		t.Fatal(err)
	}
	defer leaf.Release()
	defer leaf.Kill()
	if inspectProcessLiveness(leafPID) != processLivenessAlive {
		t.Fatal("descendant not alive before job close")
	}
	job.Close()
	jobClosed = true
	if err := waitForProcessExit(leaf, testwait.Budget(t)); err != nil {
		t.Fatalf("job left descendant: %v", err)
	}
	_ = child.Wait()
}
