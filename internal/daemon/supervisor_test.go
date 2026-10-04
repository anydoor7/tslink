package daemon

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

type supervisorTestClock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newSupervisorTestClock() *supervisorTestClock {
	return &supervisorTestClock{now: time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)}
}
func (c *supervisorTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
func (c *supervisorTestClock) clock() SupervisorClock {
	return SupervisorClock{Now: func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }, Sleep: func(ctx context.Context, d time.Duration) error {
		c.mu.Lock()
		c.delays = append(c.delays, d)
		c.now = c.now.Add(d)
		c.mu.Unlock()
		return ctx.Err()
	}}
}

func TestSupervisorBackoffBreakerAndClock(t *testing.T) {
	c := newSupervisorTestClock()
	starts := 0
	var states []SupervisorState
	err := RunSupervisor(context.Background(), c.clock(), func() (SupervisorChild, error) {
		starts++
		return SupervisorChild{PID: starts, Wait: func() error { c.advance(time.Second); return errors.New("secret must never enter state") }, Stop: func() error { t.Error("crashed child stopped"); return nil }}, nil
	}, func(s SupervisorState) error { states = append(states, s); return nil })
	if err != nil || starts != 8 {
		t.Fatalf("starts=%d err=%v", starts, err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}
	if !reflect.DeepEqual(c.delays, want) {
		t.Fatalf("backoff=%v want=%v", c.delays, want)
	}
	last := states[len(states)-1]
	if last.State != "circuit_open" || last.Reason != "eight_consecutive_unstable_runs" || last.Failures != 8 || last.UpdatedAt.Year() != 2030 {
		t.Fatalf("breaker=%+v", last)
	}
	for _, s := range states {
		if s.State == "restarting" && (!s.NextStart.Equal(s.UpdatedAt.Add(s.Backoff)) || s.Reason != "child_exited") {
			t.Fatalf("restart state=%+v", s)
		}
	}
}

func TestSupervisorPIDFailureAccounting(t *testing.T) {
	for _, publicationFailure := range []bool{false, true} {
		t.Run(strconv.FormatBool(publicationFailure), func(t *testing.T) {
			c := newSupervisorTestClock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			blocker := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(blocker, nil, 0600); err != nil {
				t.Fatal(err)
			}
			starts, crashes := 0, 1
			if publicationFailure {
				crashes++
			}
			var running []int
			done := make(chan struct{})
			err := RunSupervisor(ctx, c.clock(), func() (SupervisorChild, error) {
				starts++
				n := starts
				return SupervisorChild{PID: n, Wait: func() error {
					if publicationFailure && n == 1 {
						err := WritePIDForProcess(filepath.Join(blocker, "tslink.pid"), 123)
						if err == nil {
							t.Error("PID failure control did not fail")
						}
						return err
					}
					if n <= crashes {
						return errors.New("deliberate crash")
					}
					<-done
					return nil
				}, Stop: func() error { close(done); return nil }}, nil
			}, func(s SupervisorState) error {
				if s.State == "running" {
					running = append(running, s.Failures)
					if len(running) == crashes+1 {
						cancel()
					}
				}
				return nil
			})
			want := []int{0, 1}
			if publicationFailure {
				want = []int{0, 1, 2}
			}
			if err != nil || !reflect.DeepEqual(running, want) {
				t.Fatalf("running failures=%v want=%v err=%v", running, want, err)
			}
		})
	}
}

func TestSupervisorStableRunResetsBackoff(t *testing.T) {
	c := newSupervisorTestClock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starts := 0
	err := RunSupervisor(ctx, c.clock(), func() (SupervisorChild, error) {
		starts++
		n := starts
		return SupervisorChild{PID: n, Wait: func() error {
			if n == 3 {
				c.advance(5 * time.Minute)
			}
			if n == 5 {
				cancel()
			}
			return nil // An unexpected zero exit also requires recovery.
		}, Stop: func() error { return nil }}, nil
	}, func(SupervisorState) error { return nil })
	if err != nil || starts != 5 || !reflect.DeepEqual(c.delays, []time.Duration{time.Second, 2 * time.Second, time.Second, 2 * time.Second}) {
		t.Fatalf("starts=%d delays=%v err=%v", starts, c.delays, err)
	}
}

func TestSupervisorIntentionalStop(t *testing.T) {
	for _, phase := range []string{"before_start", "running", "backoff", "exit_boundary"} {
		t.Run(phase, func(t *testing.T) {
			c := newSupervisorTestClock()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			starts, stops := 0, 0
			done := make(chan struct{})
			var last SupervisorState
			if phase == "before_start" {
				cancel()
			}
			clock := c.clock()
			if phase == "backoff" {
				clock.Sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
			}
			err := RunSupervisor(ctx, clock, func() (SupervisorChild, error) {
				starts++
				return SupervisorChild{PID: 1, Wait: func() error {
					if phase == "exit_boundary" {
						cancel()
					}
					if phase == "running" {
						<-done
					}
					return nil
				}, Stop: func() error { stops++; close(done); return nil }}, nil
			}, func(s SupervisorState) error {
				last = s
				if phase == "running" && s.State == "running" {
					cancel()
				}
				return nil
			})
			wantStarts := 1
			if phase == "before_start" {
				wantStarts = 0
			}
			wantStops := 0
			if phase == "running" {
				wantStops = 1
			}
			// Cancellation and a completed Wait are both ready at this boundary.
			// Either branch may win; both must finish without another start.
			if phase == "exit_boundary" {
				wantStops = stops
			}
			if err != nil || starts != wantStarts || stops != wantStops || last.State != "stopped" || last.Reason != "intentional_stop" {
				t.Fatalf("starts=%d stops=%d last=%+v err=%v", starts, stops, last, err)
			}
		})
	}
}

func TestSupervisorStartAndRecordFailures(t *testing.T) {
	t.Run("start_failure_breaker", func(t *testing.T) {
		c := newSupervisorTestClock()
		starts := 0
		var last SupervisorState
		err := RunSupervisor(context.Background(), c.clock(), func() (SupervisorChild, error) {
			starts++
			return SupervisorChild{}, errors.New("sensitive start error")
		}, func(s SupervisorState) error { last = s; return nil })
		if err != nil || starts != 8 || last.State != "circuit_open" {
			t.Fatalf("starts=%d last=%+v err=%v", starts, last, err)
		}
	})
	t.Run("state_write_failure_reaps_child", func(t *testing.T) {
		c := newSupervisorTestClock()
		done := make(chan struct{})
		reaped := make(chan struct{})
		stopped := false
		want := errors.New("disk full")
		err := RunSupervisor(context.Background(), c.clock(), func() (SupervisorChild, error) {
			return SupervisorChild{PID: 1, Wait: func() error { <-done; close(reaped); return nil }, Stop: func() error { stopped = true; close(done); return nil }}, nil
		}, func(s SupervisorState) error {
			if s.State == "running" {
				return want
			}
			return nil
		})
		select {
		case <-reaped:
		default:
			t.Fatal("child not reaped")
		}
		if !errors.Is(err, want) || !stopped {
			t.Fatalf("stopped=%t err=%v", stopped, err)
		}
	})
	t.Run("stop_failure_visible", func(t *testing.T) {
		c := newSupervisorTestClock()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var last SupervisorState
		done := make(chan struct{})
		reaped := make(chan struct{})
		want := errors.New("stop failed")
		err := RunSupervisor(ctx, c.clock(), func() (SupervisorChild, error) {
			return SupervisorChild{PID: 1, Wait: func() error { <-done; close(reaped); return nil }, Stop: func() error { close(done); return want }}, nil
		}, func(s SupervisorState) error {
			last = s
			if s.State == "running" {
				cancel()
			}
			return nil
		})
		<-reaped
		if !errors.Is(err, want) || last.State != "failed" || last.Reason != "child_stop_failed" {
			t.Fatalf("last=%+v err=%v", last, err)
		}
	})
}

func TestSupervisorClockCapturedBeforeWork(t *testing.T) {
	old := supervisorNowFn
	t.Cleanup(func() { supervisorNowFn = old })
	want := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	supervisorNowFn = func() time.Time { return want }
	clock := NewSupervisorClock()
	supervisorNowFn = func() time.Time { t.Fatal("seam read after capture"); return time.Time{} }
	if !clock.Now().Equal(want) {
		t.Fatal("captured clock ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(clock.Sleep(ctx, time.Hour), context.Canceled) {
		t.Fatal("sleep did not cancel")
	}
}

func TestSupervisorSingleInstanceRealLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "supervisor.lock")
	if err := WithSupervisorLock(path, func() error {
		if err := WithSupervisorLock(path, func() error { t.Error("second supervisor entered"); return nil }); !errors.Is(err, ErrSupervisorAlreadyRunning) {
			t.Fatalf("second lock=%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := WithSupervisorLock(path, func() error { return nil }); err != nil {
		t.Fatalf("released lock=%v", err)
	}
	if err := WithSupervisorLock(t.TempDir(), func() error { return nil }); err == nil {
		t.Fatal("directory accepted as lock")
	}
}

func TestSupervisorProcessFixture(t *testing.T) {
	if os.Getenv("TSLINK_SUPERVISOR_HELPER") != "1" {
		return
	}
	root := os.Getenv("TSLINK_SUPERVISOR_HELPER_DIR")
	countPath := filepath.Join(root, "count")
	data, _ := os.ReadFile(countPath)
	n, _ := strconv.Atoi(string(data))
	n++
	if err := os.WriteFile(countPath, []byte(strconv.Itoa(n)), 0600); err != nil {
		os.Exit(9)
	}
	if n <= 2 {
		os.Exit(7)
	}
	if err := os.WriteFile(filepath.Join(root, "stable"), []byte("stable"), 0600); err != nil {
		os.Exit(9)
	}
	for {
		time.Sleep(time.Second)
	}
}

func TestSupervisorRealChildCrashesTwiceThenStabilizes(t *testing.T) {
	root := t.TempDir()
	c := newSupervisorTestClock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		defer cancel()
		for ctx.Err() == nil {
			data, _ := os.ReadFile(filepath.Join(root, "stable"))
			if string(data) == "stable" {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var pids []int
	stops := 0
	err := RunSupervisor(ctx, c.clock(), func() (SupervisorChild, error) {
		command := exec.Command(os.Args[0], "-test.run=^TestSupervisorProcessFixture$")
		command.Env = append(os.Environ(), "TSLINK_SUPERVISOR_HELPER=1", "TSLINK_SUPERVISOR_HELPER_DIR="+root)
		if err := command.Start(); err != nil {
			return SupervisorChild{}, err
		}
		pids = append(pids, command.Process.Pid)
		return SupervisorChild{PID: command.Process.Pid, Wait: command.Wait, Stop: func() error { stops++; return command.Process.Kill() }}, nil
	}, func(SupervisorState) error { return nil })
	<-joined
	data, _ := os.ReadFile(filepath.Join(root, "count"))
	if err != nil || string(data) != "3" || len(pids) != 3 || stops != 1 || !reflect.DeepEqual(c.delays, []time.Duration{time.Second, 2 * time.Second}) {
		t.Fatalf("count=%s pids=%v stops=%d delays=%v err=%v", data, pids, stops, c.delays, err)
	}
}
