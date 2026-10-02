//go:build windows

package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestReviewAssignmentWindowFixture(t *testing.T) {
	switch os.Getenv("TSLINK_CREATION_ROLE") {
	case "child", "delayed-listener":
		if os.Getenv("TSLINK_CREATION_ROLE") == "delayed-listener" {
			time.Sleep(200 * time.Millisecond)
		}
		ctx, cleanup, err := ShutdownContext(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if err := os.WriteFile(os.Getenv("TSLINK_CREATION_READY"), []byte("ready"), 0600); err != nil {
			t.Fatal(err)
		}
		<-ctx.Done()
	case "parent":
		job, err := NewChildJob()
		if err != nil {
			t.Fatal(err)
		}
		defer job.Close()
		command := creationFixtureCommand("child", os.Getenv("TSLINK_CREATION_READY"))
		_, err = job.start(command, func(pid uint32) {
			if err := os.WriteFile(os.Getenv("TSLINK_CREATION_GATE"), []byte(fmt.Sprint(pid)), 0600); err != nil {
				t.Fatal(err)
			}
			// Pause at the first instruction after CreateProcess, before the Go
			// process wrapper exists. The test kills this supervisor here.
			for {
				time.Sleep(time.Second)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func creationFixtureCommand(role, ready string) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestReviewAssignmentWindowFixture$")
	command.Env = append(os.Environ(), "TSLINK_CREATION_ROLE="+role, "TSLINK_CREATION_READY="+ready)
	return command
}

func awaitCreationFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("missing creation-boundary positive control: %s", path)
	return nil
}

func TestReviewSupervisorDeathBeforeAssignDoesNotOrphan(t *testing.T) {
	root := t.TempDir()
	gate, ready := filepath.Join(root, "created.pid"), filepath.Join(root, "ready")
	parent := creationFixtureCommand("parent", ready)
	parent.Env = append(parent.Env, "TSLINK_CREATION_GATE="+gate)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Process.Kill()
	pid, err := strconv.Atoi(string(awaitCreationFile(t, gate)))
	if err != nil {
		t.Fatal(err)
	}
	child, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Release()
	defer child.Kill()
	awaitCreationFile(t, ready)
	if inspectProcessLiveness(pid) != processLivenessAlive {
		t.Fatal("child not alive before parent death")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	if inspectProcessLiveness(parent.Process.Pid) != processLivenessAbsent {
		t.Fatal("parent death not confirmed")
	}
	if err := waitForProcessExit(child, 5*time.Second); err != nil {
		t.Fatalf("parent exited but creation-boundary child PID %d remains alive: %v", pid, err)
	}
}

func TestWindowsChildStopPinsHandleAcrossConcurrentWait(t *testing.T) {
	job, err := NewChildJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	ready := filepath.Join(t.TempDir(), "ready")
	command := creationFixtureCommand("child", ready)
	child, err := job.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	awaitCreationFile(t, ready)
	started, err := defaultProcessStartTime(child.PID)
	if err != nil {
		t.Fatal(err)
	}
	hookRan := false
	waitDone := make(chan struct{})
	err = stopJobProcess(command.Process, waitDone, func(handle windows.Handle) {
		hookRan = true
		if err := command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		// This is the review's interleaving: Wait releases its ownership while
		// Stop is paused. Stop must still retain the original kernel object.
		_ = child.Wait()
		close(waitDone)
		got, err := processStartTimeFromHandle(handle)
		if err != nil || !got.Equal(started) {
			t.Fatalf("Wait released Stop's pinned identity: %v, %v", got, err)
		}
	})
	if err != nil || !hookRan {
		t.Fatalf("pinned stop=%v hook=%t", err, hookRan)
	}
	if err := child.Stop(); err != nil {
		t.Fatalf("stop after Wait tried to reopen the PID: %v", err)
	}
}

func TestWindowsChildStopWaitsForInitializingListener(t *testing.T) {
	job, err := NewChildJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	command := creationFixtureCommand("delayed-listener", filepath.Join(t.TempDir(), "ready"))
	child, err := job.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	if err := child.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-waited; err != nil {
		t.Fatalf("child did not exit gracefully: %v", err)
	}
}

func supervisorIdentityFixture(t *testing.T) (*exec.Cmd, SupervisorInstance) {
	t.Helper()
	root := t.TempDir()
	source, binary := filepath.Join(root, "main.go"), filepath.Join(root, "identity.exe")
	if err := os.WriteFile(source, []byte("package main\nimport \"time\"\nfunc main(){for {time.Sleep(time.Second)}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build identity fixture: %v, %s", err, out)
	}
	// Only the module provenance is supplied. Creation time, executable,
	// command-line inspection, lifetime and event signaling are native.
	oldInfo := readExecutableBuildInfo
	readExecutableBuildInfo = func(string) (*debug.BuildInfo, error) {
		return &debug.BuildInfo{Main: debug.Module{Path: processProductID}}, nil
	}
	t.Cleanup(func() { readExecutableBuildInfo = oldInfo })
	command := exec.Command(binary, "supervise")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	started, err := defaultProcessStartTime(command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return command, SupervisorInstance{command.Process.Pid, started.UnixNano(), binary}
}

func createInstanceEvent(t *testing.T, pid int, started int64) windows.Handle {
	t.Helper()
	name, err := shutdownEventNameForInstance(pid, started)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateEvent(nil, 1, 0, wide)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	return handle
}

func TestWindowsSupervisorStopPinsVerifiedInstanceThroughSignal(t *testing.T) {
	command, instance := supervisorIdentityFixture(t)
	oldEvent := createInstanceEvent(t, instance.PID, instance.StartUnixNano)
	otherEvent := createInstanceEvent(t, instance.PID, instance.StartUnixNano+100)
	hookRan := false
	err := stopSupervisor(instance, func(handle windows.Handle) {
		hookRan = true
		if err := command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = command.Wait()
		started, err := processStartTimeFromHandle(handle)
		if err != nil || started.UnixNano() != instance.StartUnixNano {
			t.Fatalf("verified process object lost across parent Wait: %v, %v", started, err)
		}
	})
	if err != nil || !hookRan {
		t.Fatalf("stop=%v verified hook=%t", err, hookRan)
	}
	if result, err := windows.WaitForSingleObject(oldEvent, 0); err != nil || result != windows.WAIT_OBJECT_0 {
		t.Fatalf("verified instance event not signaled: %d, %v", result, err)
	}
	if result, err := windows.WaitForSingleObject(otherEvent, 0); err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("different instance event signaled: %d, %v", result, err)
	}
}

func TestWindowsSupervisorStopRejectsReusedIdentityBeforeSignal(t *testing.T) {
	_, instance := supervisorIdentityFixture(t)
	instance.StartUnixNano += 100
	event := createInstanceEvent(t, instance.PID, instance.StartUnixNano)
	if err := StopSupervisor(instance); err == nil || !strings.Contains(err.Error(), "start identity unverified") {
		t.Fatalf("reused identity not rejected before signaling: %v", err)
	}
	if result, err := windows.WaitForSingleObject(event, 0); err != nil || result != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("unverified instance event signaled: %d, %v", result, err)
	}
}

func TestWindowsJobCreationRejectsInvalidJobBeforeChildRuns(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	command := creationFixtureCommand("child", ready)
	job := &ChildJob{handle: windows.InvalidHandle}
	if _, err := job.Start(command); err == nil || !strings.Contains(err.Error(), "create child in job") {
		t.Fatalf("invalid job must fail at creation: %v", err)
	}
	if command.Process != nil {
		t.Fatal("invalid job created a runnable child")
	}
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("invalid job child ran: %v", err)
	}
}

func TestWindowsJobCreationPreservesEnvironmentDirectoryAndHandles(t *testing.T) {
	if os.Getenv("TSLINK_CREATION_IO_HELPER") == "1" {
		dir, _ := os.Getwd()
		input := make([]byte, 5)
		_, _ = os.Stdin.Read(input)
		fmt.Fprintf(os.Stdout, "cwd=%s env=%s input=%s arg=%s\n", dir, os.Getenv("TSLINK_CREATION_VALUE"), input, os.Args[len(os.Args)-1])
		fmt.Fprintln(os.Stderr, "stderr-control")
		return
	}
	root := t.TempDir()
	stdin, err := os.Create(filepath.Join(root, "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	_, _ = stdin.WriteString("input")
	_, _ = stdin.Seek(0, 0)
	stdout, err := os.Create(filepath.Join(root, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(root, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsJobCreationPreservesEnvironmentDirectoryAndHandles$", "with space 雪")
	command.Dir = root
	command.Env = append(os.Environ(), "TSLINK_CREATION_IO_HELPER=1", "TSLINK_CREATION_VALUE=雪")
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	job, err := NewChildJob()
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	child, err := job.Start(command)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(stdout.Name())
	errout, _ := os.ReadFile(stderr.Name())
	if !strings.Contains(string(out), "cwd="+root+" env=雪 input=input arg=with space 雪") || !bytes.Contains(errout, []byte("stderr-control")) {
		t.Fatalf("stdio/env/cwd/quoting contract lost: stdout=%q stderr=%q", out, errout)
	}
}
