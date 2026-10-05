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

	"github.com/anydoor7/tslink/internal/testwait"
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
	// The descendant outlives Notify's own 10s deadline (sleep 12). Waiting on
	// it would expire that deadline and report alert_command_failed, so success
	// is the assertion on the product's inner deadline; testwait only bounds a
	// Notify that ignores its deadline entirely.
	notified := make(chan error, 1)
	go func() { notified <- Notify(context.Background(), c, Event{Kind: "app_down"}) }()
	err := testwait.Recv(t, notified, "Notify returned while a descendant held its output")
	t.Logf("descendant-held pipes duration=%s err=%v", time.Since(start), err)
	if err != nil {
		t.Errorf("Notify waited for the descendant past its 10s deadline: %v", err)
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
	var pidText []byte
	testwait.Until(t, "descendant helper started", func() bool {
		b, err := os.ReadFile(pidFile)
		pidText = b
		return err == nil && len(b) > 0
	})
	pid, err := strconv.Atoi(string(pidText))
	if err != nil {
		t.Fatal(err)
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
	if err := testwait.Recv(t, done, "canceled Notify returned despite descendant-held pipes"); err == nil || err.Error() != "alert_command_failed" {
		t.Fatal(err)
	}
	testwait.Until(t, "notifier descendant exited after cancellation", func() bool {
		state, err = exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
		if strings.TrimSpace(string(state)) == "" {
			if _, ok := err.(*exec.ExitError); ok {
				return true
			}
		}
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			return true
		}
		if err != nil {
			t.Fatal("ps probe failed", err)
		}
		return false
	})
}
