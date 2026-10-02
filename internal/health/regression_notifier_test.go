//go:build darwin || linux

package health

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The notifier re-execs this test binary and starts /bin/sleep directly; no shell or network.
func TestReviewNotifierProcessTree(t *testing.T) {
	switch os.Getenv("TSLINK_REVIEW_NOTIFIER_PHASE") {
	case "parent":
		child := exec.Command("/bin/sleep", "12")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "control":
		os.Exit(0)
	case "active-parent":
		child := exec.Command("/bin/sleep", "2")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(2)
		}
		if os.WriteFile(os.Getenv("TSLINK_HEALTH_GROUP_PID"), []byte(fmt.Sprint(child.Process.Pid)), 0600) != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	t.Setenv("TSLINK_REVIEW_NOTIFIER_PHASE", "control")
	c := NotifierConfig{Command: []string{os.Args[0], "-test.run=^TestReviewNotifierProcessTree$"}}
	start := time.Now()
	if err := Notify(context.Background(), c, Event{Kind: "app_down"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("control duration=%s", time.Since(start))
	t.Setenv("TSLINK_REVIEW_NOTIFIER_PHASE", "parent")
	start = time.Now()
	err := Notify(context.Background(), c, Event{Kind: "app_down"})
	elapsed := time.Since(start)
	t.Logf("descendant-held pipes duration=%s err=%v", elapsed, err)
	if elapsed > 11*time.Second {
		t.Errorf("10s notifier timeout exceeded; returned after %s", elapsed)
	}
}

func TestNotifierCancellationStopsUnixProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("TSLINK_HEALTH_GROUP_PID", pidFile)
	t.Setenv("TSLINK_REVIEW_NOTIFIER_PHASE", "active-parent")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Notify(ctx, NotifierConfig{Command: []string{os.Args[0], "-test.run=^TestReviewNotifierProcessTree$"}}, Event{Kind: "app_down"})
	}()
	deadline := time.After(3 * time.Second)
	var pid int
	for {
		if b, err := os.ReadFile(pidFile); err == nil && len(b) > 0 {
			pid, err = strconv.Atoi(string(b))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("descendant helper never started")
		case <-time.After(time.Millisecond):
		}
	}
	t.Cleanup(func() {
		if child, err := os.FindProcess(pid); err == nil {
			_ = child.Kill()
		}
	})
	// The positive control checks that ps actually sees this live descendant.
	state, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	if err != nil || strings.TrimSpace(string(state)) == "" || strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
		t.Fatalf("process-state control failed: %q %v", state, err)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "alert_command_failed" {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("descendant-held pipes blocked cancellation")
	}
	deadline = time.After(time.Second)
	for {
		state, err = exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
		if strings.TrimSpace(string(state)) == "" {
			if _, ok := err.(*exec.ExitError); ok {
				break
			}
		}
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			break
		}
		if err != nil {
			t.Fatal("ps probe failed", err)
		}
		select {
		case <-deadline:
			t.Fatal("notifier descendant survived cancellation", string(state))
		case <-time.After(time.Millisecond):
		}
	}
}
