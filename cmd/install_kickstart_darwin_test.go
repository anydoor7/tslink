//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This fake models a bootstrapped job that stays pending until explicitly
// started. All manager calls, including verification, use the existing seam.
func stubPendingLaunchAgent(t *testing.T, domain, plistPath string, kickstartFails, staysPending bool, prefix [][]string) *[][]string {
	t.Helper()
	stubDarwinLaunchAgentVerificationNoWait(t)
	if staysPending {
		launchAgentVerifyTimeout = 20 * time.Millisecond
		launchAgentVerifyPollInterval = 5 * time.Millisecond
	}
	oldLaunchctl := launchctlCombinedOutput
	oldUID := userUIDFn
	t.Cleanup(func() {
		launchctlCombinedOutput = oldLaunchctl
		userUIDFn = oldUID
	})
	userUIDFn = func() int { return 501 }
	target := domain + "/" + plistLabel
	var calls [][]string
	bootstrapped, startRequested := false, false
	printCalls := 0
	launchctlCombinedOutput = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if len(calls) <= len(prefix) {
			want := prefix[len(calls)-1]
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("handoff call = %q, want %q", args, want)
			}
			if args[0] == "bootstrap" {
				return []byte("Could not find domain for: gui/501"), errors.New("exit status 112")
			}
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		}
		switch args[0] {
		case "bootstrap":
			if !reflect.DeepEqual(args, []string{"bootstrap", domain, plistPath}) || bootstrapped {
				t.Fatalf("bootstrap = %q, want one bootstrap in %s", args, domain)
			}
			bootstrapped = true
			return nil, nil
		case "kickstart":
			// Exact arguments reject -k, the wrong domain and extra flags.
			if !bootstrapped || startRequested || !reflect.DeepEqual(args, []string{"kickstart", target}) {
				t.Fatalf("kickstart = %q, want one non-killing start after bootstrap for %s", args, target)
			}
			startRequested = true
			if kickstartFails {
				return []byte("kickstart denied by fixture"), errors.New("exit status 7")
			}
			return nil, nil
		case "print":
			if !startRequested {
				t.Fatal("running-state verification began before kickstart")
			}
			if !reflect.DeepEqual(args, []string{"print", target}) {
				t.Fatalf("verification = %q, want exact bootstrapped target %s", args, target)
			}
			printCalls++
			if staysPending || (kickstartFails && printCalls == 1) {
				return []byte("state = not running\nruns = 0\npended nondemand spawn = speculative\n"), nil
			}
			return runningLaunchAgentState(), nil
		default:
			t.Fatalf("unexpected launchctl call: %q", args)
			return nil, nil
		}
	}
	return &calls
}

func assertKickstartVerification(t *testing.T, err error, domain string, staysPending bool, calls, prefix [][]string, plistPath string, kickstartFails bool) {
	t.Helper()
	target := domain + "/" + plistLabel
	if staysPending {
		if err == nil {
			t.Fatal("pending job reported success")
		}
		for _, want := range []string{"did not reach running state", "launchctl kickstart " + target, "exit status 7", "kickstart denied by fixture"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, missing %q", err, want)
			}
		}
	} else if err != nil {
		t.Fatalf("job reached running but reported failure: %v", err)
	}
	wantCalls := append(append([][]string(nil), prefix...), []string{"bootstrap", domain, plistPath}, []string{"kickstart", target})
	prints := 2 // Initial running evidence, then a stable PID observation.
	if staysPending {
		prints = len(calls) - len(wantCalls)
		if prints < 1 {
			t.Fatal("pending job was not checked after kickstart failed")
		}
	} else if kickstartFails {
		prints++ // A failed start still polls through transient pending state.
	}
	for i := 0; i < prints; i++ {
		wantCalls = append(wantCalls, []string{"print", target})
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("launchctl calls = %q, want %q", calls, wantCalls)
	}
}

func TestLoadLaunchAgentKickstartsBeforeVerification(t *testing.T) {
	for _, domain := range []string{"gui/501", "user/501"} {
		for _, scenario := range []string{"start-pending-job", "kickstart-error-then-running", "kickstart-error-still-pending"} {
			t.Run(domain+"/"+scenario, func(t *testing.T) {
				plistPath := filepath.Join(t.TempDir(), plistLabel+".plist")
				kickstartFails := scenario != "start-pending-job"
				staysPending := scenario == "kickstart-error-still-pending"
				prefix := [][]string{{"bootout", "gui/501/" + plistLabel}, {"bootout", "user/501/" + plistLabel}}
				if domain == "user/501" {
					prefix = append(prefix, []string{"bootstrap", "gui/501", plistPath})
				}
				calls := stubPendingLaunchAgent(t, domain, plistPath, kickstartFails, staysPending, prefix)
				result := loadLaunchAgent(context.Background(), plistPath, false)
				assertKickstartVerification(t, result.Err, domain, staysPending, *calls, prefix, plistPath, kickstartFails)
				if !result.Bootstrapped || result.Domain != domain || result.Target != domain+"/"+plistLabel {
					t.Fatalf("load result = %+v, want bootstrapped %s", result, domain)
				}
				if staysPending && !strings.Contains(result.Output, "kickstart denied by fixture") {
					t.Fatalf("load output lost kickstart diagnostics: %q", result.Output)
				}
			})
		}
	}
}

func TestRestorePreviousLaunchAgentKickstartsBeforeVerification(t *testing.T) {
	for _, domain := range []string{"gui/501", "user/501"} {
		for _, scenario := range []string{"start-pending-job", "kickstart-error-then-running", "kickstart-error-still-pending"} {
			t.Run(domain+"/"+scenario, func(t *testing.T) {
				plistPath := filepath.Join(t.TempDir(), plistLabel+".plist")
				if err := os.WriteFile(plistPath, []byte("new plist"), 0o600); err != nil {
					t.Fatal(err)
				}
				kickstartFails := scenario != "start-pending-job"
				staysPending := scenario == "kickstart-error-still-pending"
				// Restore the captured domain even if the failed replacement used another domain.
				prefix := [][]string{{"bootout", "gui/501/" + plistLabel}}
				calls := stubPendingLaunchAgent(t, domain, plistPath, kickstartFails, staysPending, prefix)
				oldPlist := []byte("old plist")
				result, err := restorePreviousLaunchAgent(context.Background(),
					launchAgentPreviousState{Existed: true, Plist: oldPlist, Mode: 0o600, Domain: domain, Target: domain + "/" + plistLabel},
					launchctlLoadResult{Bootstrapped: true, Target: "gui/501/" + plistLabel}, plistPath)
				assertKickstartVerification(t, err, domain, staysPending, *calls, prefix, plistPath, kickstartFails)
				if !result.PlistRestored || result.Reloaded == staysPending {
					t.Fatalf("restore result = %+v, want restored plist and Reloaded=%v", result, !staysPending)
				}
				got, readErr := os.ReadFile(plistPath)
				if readErr != nil || !bytes.Equal(got, oldPlist) {
					t.Fatalf("restored plist = %q, %v", got, readErr)
				}
			})
		}
	}
}
