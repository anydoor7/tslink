package filelock

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// lockHolderEnv makes a child run of this test binary hold an exclusive lock
// on the named file until its stdin closes. It is not a TSLINK_ variable, so
// testenv.Main passes it through.
const lockHolderEnv = "FILELOCK_TEST_LOCK_HOLDER"

const lockHolderReady = "filelock-test-holder-locked"

func openLockFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestTryLockReportsALockHeldByAnotherProcess: while a child process holds
// the lock, TryLock returns false without an error and without waiting; once
// the child has let go, TryLock takes the lock.
func TestTryLockReportsALockHeldByAnotherProcess(t *testing.T) {
	if path := os.Getenv(lockHolderEnv); path != "" {
		f := openLockFile(t, path)
		if err := Lock(f); err != nil {
			t.Fatalf("holder Lock: %v", err)
		}
		fmt.Println(lockHolderReady)
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := Unlock(f); err != nil {
			t.Fatalf("holder Unlock: %v", err)
		}
		return
	}

	path := filepath.Join(t.TempDir(), "held.lock")
	f := openLockFile(t, path)
	// Control: with no holder, TryLock takes the lock. Let go of it again for
	// the child.
	if ok, err := TryLock(f); !ok || err != nil {
		t.Fatalf("TryLock with no holder = %v, %v; want true, nil", ok, err)
	}
	if err := Unlock(f); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
	child.Env = append(os.Environ(), lockHolderEnv+"="+path)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatalf("start lock holder: %v", err)
	}
	ready := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		scanner := bufio.NewScanner(stdout)
		reported := false
		for scanner.Scan() {
			if !reported && strings.TrimSpace(scanner.Text()) == lockHolderReady {
				reported = true
				close(ready)
			}
		}
	}()
	// os/exec wants every read from the pipe done before Wait.
	waitChild := func() error {
		<-drained
		return child.Wait()
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = child.Process.Kill()
		_ = waitChild()
	})
	select {
	case <-ready:
	case <-drained:
		t.Fatalf("lock holder exited without reporting the lock (stderr %q)", stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("lock holder did not take the lock within 30s (stderr %q)", stderr.String())
	}

	start := time.Now()
	ok, err := TryLock(f)
	if ok || err != nil {
		t.Fatalf("TryLock while another process holds the lock = %v, %v; want false, nil", ok, err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("TryLock took %s while another process held the lock; it must not wait", waited)
	}

	_ = stdin.Close()
	if err := waitChild(); err != nil {
		t.Fatalf("lock holder: %v (stderr %q)", err, stderr.String())
	}
	if ok, err := TryLock(f); !ok || err != nil {
		t.Fatalf("TryLock after the holder let go = %v, %v; want true, nil", ok, err)
	}
	if err := Unlock(f); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

// TestTryLockReportsALockHeldThroughAnotherOpenFile: the lock belongs to the
// open file, so a second open of the same path in this process cannot take it
// either.
func TestTryLockReportsALockHeldThroughAnotherOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	first := openLockFile(t, path)
	second := openLockFile(t, path)

	if ok, err := TryLock(first); !ok || err != nil {
		t.Fatalf("TryLock(first) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := TryLock(second); ok || err != nil {
		t.Fatalf("TryLock(second) while first holds the lock = %v, %v; want false, nil", ok, err)
	}
	if err := Unlock(first); err != nil {
		t.Fatalf("Unlock(first): %v", err)
	}
	if ok, err := TryLock(second); !ok || err != nil {
		t.Fatalf("TryLock(second) after first let go = %v, %v; want true, nil", ok, err)
	}
	if err := Unlock(second); err != nil {
		t.Fatalf("Unlock(second): %v", err)
	}
}
