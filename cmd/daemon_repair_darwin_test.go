//go:build darwin

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func setupRepairManager(t *testing.T, gate func()) {
	t.Helper()
	isolateBootstrap(t)
	resetRootJSONFlag(t)
	stubDarwinLaunchAgentVerificationNoWait(t)
	oldLaunch, oldConflict, oldArtifact := launchctlCombinedOutput, installDaemonConflictFn, installDaemonArtifactConflictFn
	t.Cleanup(func() {
		launchctlCombinedOutput = oldLaunch
		installDaemonConflictFn = oldConflict
		installDaemonArtifactConflictFn = oldArtifact
	})
	installDaemonConflictFn = func(context.Context) error { return nil }
	installDaemonArtifactConflictFn = func(context.Context) error { return nil }
	var running atomic.Bool
	isRunningFn = func(string) bool { return running.Load() }
	installDaemonFn = installDaemonLocked
	detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
		return Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true}
	}
	launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		gate()
		switch args[0] {
		case "bootout":
			running.Store(false)
			return nil, nil
		case "bootstrap":
			running.Store(true)
			return nil, nil
		case "print":
			if running.Load() {
				return []byte("state = running\npid = 4242\n"), nil
			}
			return []byte("Could not find service"), errors.New("absent")
		default:
			return nil, errors.New("unexpected launchctl")
		}
	}
}

// The same legal SIGTERMed sequence covers both rc=0 and operation-in-progress.
// No bootstrap is allowed while the service is still visible.
func TestRepairBootoutWaitsForAbsence(t *testing.T) {
	for _, op := range []string{"success", "in_progress"} {
		t.Run(op, func(t *testing.T) {
			isolateBootstrap(t)
			stubDarwinLaunchAgentVerificationNoWait(t)
			old, ot, oi := launchctlCombinedOutput, launchAgentBootoutTimeout, launchAgentBootoutPollInterval
			t.Cleanup(func() {
				launchctlCombinedOutput = old
				launchAgentBootoutTimeout = ot
				launchAgentBootoutPollInterval = oi
			})
			launchAgentBootoutTimeout = 20 * time.Millisecond
			launchAgentBootoutPollInterval = 0
			prints := 0
			bootstrapped := false
			early := false
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "bootout":
					if args[1] == launchctlServiceTargetForDomain(launchctlUserDomain()) {
						return []byte("Could not find service"), errors.New("absent")
					}
					if op == "in_progress" {
						return []byte("Operation now in progress"), errors.New("in progress")
					}
					return nil, nil
				case "print":
					if bootstrapped {
						return []byte("state = running\npid = 4242\n"), nil
					}
					prints++
					if prints < 3 {
						return []byte("state = SIGTERMed\npid = 4242\n"), nil
					}
					return []byte("Could not find service"), errors.New("absent")
				case "bootstrap":
					early = prints < 3
					bootstrapped = true
					return nil, nil
				}
				return nil, errors.New("unexpected command")
			}
			result := reinstallLaunchAgent(context.Background(), "/fixture.plist")
			if result.Err != nil || early || prints != 3 || !bootstrapped {
				t.Fatalf("absence barrier failed: err=%v early=%t prints=%d bootstrapped=%t", result.Err, early, prints, bootstrapped)
			}
		})
	}
}

func TestRepairUninstallWaitsAndKeepsDefinitionOnTimeout(t *testing.T) {
	for _, op := range []string{"success", "in_progress", "timeout"} {
		t.Run(op, func(t *testing.T) {
			setupRepairManager(t, func() {})
			if err := repairOperation(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			path, _ := supervisorPath()
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			old, ot, oi := launchctlCombinedOutput, launchAgentBootoutTimeout, launchAgentBootoutPollInterval
			t.Cleanup(func() {
				launchctlCombinedOutput = old
				launchAgentBootoutTimeout = ot
				launchAgentBootoutPollInterval = oi
			})
			launchAgentBootoutTimeout = 2 * time.Millisecond
			launchAgentBootoutPollInterval = 0
			prints := 0
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[1] == launchctlServiceTargetForDomain(launchctlUserDomain()) {
					return []byte("Could not find service"), errors.New("absent")
				}
				if args[0] == "bootout" {
					if op == "in_progress" {
						return []byte("Operation now in progress"), errors.New("pending")
					}
					return nil, nil
				}
				if args[0] == "print" {
					if _, err := os.Stat(path); err != nil {
						t.Error("plist removed before absence")
					}
					prints++
					if op == "timeout" || prints < 3 {
						return []byte("state = SIGTERMed\npid = 4242\n"), nil
					}
					return []byte("Could not find service"), errors.New("absent")
				}
				return nil, errors.New("unexpected command")
			}
			err = uninstallCmd.RunE(repairCommand(context.Background()), nil)
			if op == "timeout" {
				after, _ := os.ReadFile(path)
				if err == nil || string(after) != string(original) {
					t.Fatalf("timeout lost definition: %v", err)
				}
			} else {
				if err != nil || prints != 3 {
					t.Fatalf("uninstall barrier: err=%v prints=%d", err, prints)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("definition remains")
				}
			}
		})
	}
}

// Restoration must cross the same barrier before reloading old bytes, and new
// install rollback must not remove the definition while the job is terminating.
func TestRepairRollbackWaitsForAbsence(t *testing.T) {
	for _, restore := range []bool{false, true} {
		name := "new_install"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			setupRepairManager(t, func() {})
			if err := repairOperation(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			path, _ := supervisorPath()
			old, ot, oi := launchctlCombinedOutput, launchAgentBootoutTimeout, launchAgentBootoutPollInterval
			t.Cleanup(func() {
				launchctlCombinedOutput = old
				launchAgentBootoutTimeout = ot
				launchAgentBootoutPollInterval = oi
			})
			launchAgentBootoutTimeout = 20 * time.Millisecond
			launchAgentBootoutPollInterval = 0
			prints := 0
			loaded := false
			early := false
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				switch args[0] {
				case "bootout":
					return nil, nil
				case "print":
					if loaded {
						return []byte("state = running\npid = 4242\n"), nil
					}
					if _, err := os.Stat(path); err != nil {
						t.Error("definition removed before absence")
					}
					prints++
					if prints < 3 {
						return []byte("state = SIGTERMed\n"), nil
					}
					return []byte("Could not find service"), errors.New("absent")
				case "bootstrap":
					early = prints < 3
					loaded = true
					return nil, nil
				}
				return nil, errors.New("unexpected")
			}
			target := launchctlServiceTargetForDomain(launchctlDomain())
			if restore {
				previous := launchAgentPreviousState{Existed: true, Plist: []byte("old definition"), Mode: 0600, Domain: launchctlDomain(), Target: target}
				_, err := restorePreviousLaunchAgent(previous, launchctlLoadResult{Bootstrapped: true, Target: target}, path)
				if err != nil || !loaded || early {
					t.Fatalf("restore: %v loaded=%t early=%t", err, loaded, early)
				}
			} else {
				if err := rollbackNewLaunchAgent(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if prints != 3 {
				t.Fatalf("absence samples=%d", prints)
			}
		})
	}
}

func TestRepairBootoutSuccessPolicy(t *testing.T) {
	for _, mode := range []string{"absent", "stuck", "domain", "denied"} {
		for _, allow := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/allow_%t", mode, allow), func(t *testing.T) {
				old, ot, oi := launchctlCombinedOutput, launchAgentBootoutTimeout, launchAgentBootoutPollInterval
				t.Cleanup(func() {
					launchctlCombinedOutput = old
					launchAgentBootoutTimeout = ot
					launchAgentBootoutPollInterval = oi
				})
				launchAgentBootoutTimeout = time.Millisecond
				launchAgentBootoutPollInterval = 0
				prints := 0
				launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
					if args[0] == "bootout" {
						return nil, nil
					}
					prints++
					switch mode {
					case "absent":
						return []byte("Could not find service"), errors.New("absent")
					case "domain":
						return []byte("Could not find domain for: gui/501"), errors.New("domain unavailable")
					case "denied":
						return []byte("permission denied"), errors.New("denied")
					default:
						return []byte("state = SIGTERMed"), nil
					}
				}
				err := bootoutLaunchAgentTargetWithPolicy(context.Background(), "gui/501/com.tslink.daemon", allow)
				wantOK := mode == "absent" || (mode == "domain" && allow)
				if (err == nil) != wantOK || prints == 0 {
					t.Fatalf("mode=%s allow=%t prints=%d err=%v", mode, allow, prints, err)
				}
				if mode == "domain" && !allow && !errors.Is(err, errLaunchctlDomainUnavailable) {
					t.Fatalf("domain policy lost: %v", err)
				}
			})
		}
	}
}

func TestRepairFailedInstallRollbackCannotUndoPeer(t *testing.T) {
	setupRepairManager(t, func() {})
	entered, release := make(chan struct{}), make(chan struct{})
	active := false
	fail := true
	bootouts := 0
	launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "bootout":
			bootouts++
			if bootouts == 3 {
				close(entered)
				<-release
			}
			active = false
			return nil, nil
		case "bootstrap":
			active = true
			return nil, nil
		case "print":
			if !active {
				return []byte("Could not find service"), errors.New("absent")
			}
			if fail {
				return []byte("state = exited\npid = 0\n"), nil
			}
			return []byte("state = running\npid = 4242\n"), nil
		}
		return nil, errors.New("unexpected")
	}
	first := make(chan error, 1)
	go func() { first <- repairOperation(context.Background(), "install") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("rollback never entered")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err := repairOperation(ctx, "install")
	cancel()
	close(release)
	firstErr := repairAwait(t, first)
	if !errors.Is(err, context.DeadlineExceeded) || firstErr == nil {
		t.Fatalf("rollback transaction: peer=%v first=%v", err, firstErr)
	}
	path, _ := supervisorPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("new-install rollback did not remove failed definition: %v", err)
	}
	fail = false
	if err := repairOperation(context.Background(), "install"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	dir, _ := absoluteConfigDir()
	if err != nil || !supervisorConfigMatches(data, dir) {
		t.Fatalf("successful peer lost final definition: %v", err)
	}
}

func TestRepairUninstallDomainLostAfterAcceptedBootout(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			setupRepairManager(t, func() {})
			if err := repairOperation(context.Background(), "install"); err != nil {
				t.Fatal(err)
			}
			path, _ := supervisorPath()
			launchctlCombinedOutput = func(ctx context.Context, args ...string) ([]byte, error) {
				if args[1] == launchctlServiceTargetForDomain(launchctlUserDomain()) {
					return []byte("Could not find service"), errors.New("absent")
				}
				if args[0] == "bootout" {
					return nil, nil
				}
				return []byte("Could not find domain for: gui/501"), errors.New("unavailable")
			}
			c := repairCommand(context.Background())
			if err := c.Flags().Set("force", fmt.Sprint(force)); err != nil {
				t.Fatal(err)
			}
			err := uninstallCmd.RunE(c, nil)
			if (err == nil) != force {
				t.Fatalf("force=%t err=%v", force, err)
			}
			_, statErr := os.Stat(path)
			if os.IsNotExist(statErr) != force {
				t.Fatalf("force=%t plist state=%v", force, statErr)
			}
		})
	}
}
