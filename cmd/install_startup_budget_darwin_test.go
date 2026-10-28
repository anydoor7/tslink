//go:build darwin

package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The elapsed-time regression also runs unchanged with the pre-kickstart
// installer: all service-manager operations are fakes, including the queries.
func TestLaunchAgentPostBootstrapBudget(t *testing.T) {
	for _, path := range []string{"gui", "user", "restore"} {
		for _, scenario := range []string{"slow-pending", "blocked-pending", "blocked-then-running"} {
			t.Run(path+"/"+scenario, func(t *testing.T) {
				stubDarwinLaunchAgentVerificationNoWait(t)
				const budget = 80 * time.Millisecond
				// Scheduling slack for -race on shared CI runners (163ms was seen
				// with 60ms). An unbounded kickstart still fails: it outlives the
				// verification window, so the final state check never happens.
				const allowance = 200 * time.Millisecond
				launchAgentVerifyTimeout = budget
				launchAgentVerifyPollInterval = time.Millisecond
				launchAgentSettleWindow = 5 * time.Millisecond
				oldLaunchctl, oldUID := launchctlCombinedOutput, userUIDFn
				t.Cleanup(func() { launchctlCombinedOutput, userUIDFn = oldLaunchctl, oldUID })
				userUIDFn = func() int { return 501 }
				domain := "gui/501"
				if path == "user" {
					domain = "user/501"
				}
				var bootstrappedAt, kickstartDeadline, verificationDeadline time.Time
				kickstarted, verified := false, false
				launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
					switch args[0] {
					case "bootout":
						return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
					case "bootstrap":
						if path == "user" && args[1] == "gui/501" {
							return []byte("Could not find domain for: gui/501"), errors.New("exit status 112")
						}
						if ctx.Err() != nil {
							t.Fatalf("bootstrap received canceled context: %v", ctx.Err())
						}
						bootstrappedAt = time.Now()
						return nil, nil
					case "kickstart":
						if !reflect.DeepEqual(args, []string{"kickstart", domain + "/" + plistLabel}) {
							t.Fatalf("unexpected kickstart arguments: %q", args)
						}
						kickstarted = true
						kickstartDeadline, _ = ctx.Deadline()
						// The blocked fixture's watchdog lets the old unbounded
						// implementation finish and fail by assertion, not test timeout.
						delay := 300 * time.Millisecond
						if strings.HasPrefix(scenario, "blocked") {
							delay = 600 * time.Millisecond
						}
						timer := time.NewTimer(delay)
						defer timer.Stop()
						select {
						case <-ctx.Done():
							return []byte("kickstart canceled by fixture"), ctx.Err()
						case <-timer.C:
							return []byte("slow kickstart fixture"), errors.New("exit status 7")
						}
					case "print":
						if !verified {
							verificationDeadline, _ = ctx.Deadline()
						}
						verified = true
						if scenario == "blocked-then-running" {
							return runningLaunchAgentState(), nil
						}
						return []byte("state = not running\nruns = 0\n"), nil
					default:
						t.Fatalf("unexpected manager call: %q", args)
						return nil, nil
					}
				}
				plistPath := filepath.Join(t.TempDir(), plistLabel+".plist")
				started := time.Now()
				var err error
				if path == "restore" {
					// Compensation must outlive this expired caller, then acquire
					// its own finite startup window after the old job is bootstrapped.
					ctx, cancel := context.WithDeadline(context.Background(), started.Add(-time.Second))
					defer cancel()
					_, err = restorePreviousLaunchAgent(ctx, launchAgentPreviousState{
						Existed: true, Plist: []byte("old plist"), Mode: 0o600,
						Domain: domain, Target: domain + "/" + plistLabel,
					}, launchctlLoadResult{}, plistPath)
				} else {
					err = loadLaunchAgent(context.Background(), plistPath, false).Err
				}
				elapsed := time.Since(bootstrappedAt)
				t.Logf("path=%s scenario=%s post_bootstrap=%s total=%s budget=%s allowance=%s kickstart=%v deadline=%v", path, scenario, elapsed, time.Since(started), budget, allowance, kickstarted, !kickstartDeadline.IsZero())
				if bootstrappedAt.IsZero() || !verified {
					t.Fatal("successful bootstrap was not followed by a final state check")
				}
				if elapsed > budget+allowance {
					t.Fatalf("post-bootstrap wait exceeded shared budget: %s > %s + %s", elapsed, budget, allowance)
				}
				if kickstarted {
					if verificationDeadline.IsZero() || verificationDeadline.After(bootstrappedAt.Add(budget+5*time.Millisecond)) {
						t.Fatalf("verification reset or lost the stage deadline: %s", verificationDeadline)
					}
					if kickstartDeadline.IsZero() || kickstartDeadline.After(bootstrappedAt.Add(budget/3)) || verificationDeadline.Sub(kickstartDeadline) < budget/2 {
						t.Fatalf("kickstart did not reserve verification time: kickstart=%s verification=%s", kickstartDeadline, verificationDeadline)
					}
				}
				if scenario == "blocked-then-running" {
					if err != nil {
						t.Fatalf("running job failed after kickstart timeout: %v", err)
					}
				} else {
					if err == nil || !strings.Contains(err.Error(), "did not reach running state") {
						t.Fatalf("pending job lost verification error: %v", err)
					}
					if kickstarted && (!strings.Contains(err.Error(), "kickstart") || !strings.Contains(err.Error(), "kickstart canceled by fixture") || !errors.Is(err, context.DeadlineExceeded)) {
						t.Fatalf("timed-out startup lost kickstart diagnostics: %v", err)
					}
				}
			})
		}
	}
}
