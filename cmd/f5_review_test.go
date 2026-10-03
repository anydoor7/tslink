package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/server"
)

type portalHandoffRunner struct {
	*mockInteractiveServer
	ready func() error
	check func(context.Context, *portalHandoffRunner) error
}

func (m *portalHandoffRunner) SetReadyFunc(fn func() error)  { m.ready = fn }
func (m *portalHandoffRunner) Run(ctx context.Context) error { return m.check(ctx, m) }

// Reflection keeps this regression executable on the reviewed base as well.
func portalHandoffEvent(serviceName, state string) server.AuthHandoff {
	event := server.AuthHandoff{Service: serviceName, AuthURL: "https://login.example.invalid/fixture"}
	field := reflect.ValueOf(&event).Elem().FieldByName("State")
	if field.IsValid() {
		field.SetString(state)
	}
	return event
}

func TestF5ReviewReadyPreservesPendingPortalHandoff(t *testing.T) {
	for _, terminal := range []string{"complete", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			path := filepath.Join(dir, "auth-handoff.json")
			serveSaveAuthHandoffFn, serveLoadAuthHandoffFn, serveRemoveAuthHandoffFn = saveAuthHandoff, loadAuthHandoff, removeAuthHandoff
			serveWriteReadyFn = func(path string, pid int) error { return os.WriteFile(path, []byte(fmt.Sprint(pid)), 0600) }
			m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
			m.check = func(ctx context.Context, m *portalHandoffRunner) error {
				if err := m.authHandoff(ctx, portalHandoffEvent("home", "pending")); err != nil {
					return err
				}
				if _, err := loadAuthHandoff(path); err != nil {
					return fmt.Errorf("pending positive control: %w", err)
				}
				if err := m.ready(); err != nil {
					return err
				}
				if _, err := loadAuthHandoff(path); err != nil {
					return fmt.Errorf("readiness erased pending portal handoff: %w", err)
				}
				// Completion/cancellation of home must not erase another node's offer.
				if err := m.authHandoff(ctx, portalHandoffEvent("photos", "pending")); err != nil {
					return err
				}
				if err := m.authHandoff(ctx, portalHandoffEvent("home", terminal)); err != nil {
					return err
				}
				record, err := loadAuthHandoff(path)
				if err != nil || record.Service != "photos" {
					return fmt.Errorf("unrelated pending handoff lost: %+v %v", record, err)
				}
				if err := m.authHandoff(ctx, portalHandoffEvent("photos", terminal)); err != nil {
					return err
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					return fmt.Errorf("terminal %s handoff remains: %v", terminal, err)
				}
				return nil
			}
			serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
			if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path, ReadyPath: filepath.Join(dir, "ready")}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestF5ReviewCompletedPortalHandoff(t *testing.T) {
	paths := peopleTestPaths(t)
	paths.AuthHandoff = filepath.Join(filepath.Dir(paths.Registry), "auth-handoff.json")
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := tsRuntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	states := []tsRuntime.ServiceState{}
	for _, svc := range reg.Services {
		states = append(states, tsRuntime.ServiceState{Service: svc, RuntimeHost: svc.Name + ".tailnet.ts.net", RuntimeState: tsRuntime.ServiceRuntimeRunning})
	}
	snapshot := tsRuntime.NewSnapshot(42, now.Add(-time.Minute), fp, now, states)
	snapshot.Portal = tsRuntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net"}
	if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 42, now.Add(-time.Minute))
	for _, name := range []string{"photos", "home"} {
		t.Run(name, func(t *testing.T) {
			// Use the production handoff encoder and loader on actual isolated files.
			if err := saveAuthHandoff(paths.AuthHandoff, newAuthHandoffRecord(name, "https://login.example.invalid/fixture", 42)); err != nil {
				t.Fatal(err)
			}
			result, err := readOnlyStatus.getPollableStatus(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
			if err != nil {
				t.Fatal(err)
			}
			if result.Portal.State != "running" || result.Portal.URL == "" || result.AuthorizedServiceCount != 2 {
				t.Fatalf("invalid positive control: %+v", result)
			}
			t.Logf("completed node=%s portal=%+v auth_status=%s auth_url=%s app_count=%d", name, result.Portal, result.AuthStatus, result.AuthURL, result.AuthorizedServiceCount)
			if result.AuthStatus == authStatusNeedsLogin || result.AuthURL != "" {
				t.Error("completed portal handoff still tells user to log in")
			}
		})
	}
}

func TestPortalHandoffCleanupFailures(t *testing.T) {
	for _, scenario := range []string{"missing", "partial", "directory", "oversized", "expired", "stale-pid", "new-url", "remove-error", "no-path"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			path := filepath.Join(dir, "auth-handoff.json")
			serveSaveAuthHandoffFn, serveLoadAuthHandoffFn, serveRemoveAuthHandoffFn = saveAuthHandoff, loadAuthHandoff, removeAuthHandoff
			wantErr := errors.New("fixture cleanup unavailable")
			cleanupActive := false
			m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
			m.check = func(ctx context.Context, m *portalHandoffRunner) error {
				record := newAuthHandoffRecord("home", "https://login.example.invalid/fixture", os.Getpid())
				switch scenario {
				case "partial":
					if err := os.WriteFile(path, []byte(`{"status":`), 0600); err != nil {
						return err
					}
				case "oversized":
					if err := os.WriteFile(path, make([]byte, (64<<10)+1), 0600); err != nil {
						return err
					}
				case "directory":
					if err := os.Mkdir(path, 0700); err != nil {
						return err
					}
				case "expired":
					record.ExpiresAt = time.Now().Add(-time.Hour)
				case "stale-pid":
					record.DaemonPID++
				case "new-url":
					record.AuthURL += "-new"
				}
				if scenario != "partial" && scenario != "directory" && scenario != "oversized" && scenario != "missing" && scenario != "no-path" {
					if err := saveAuthHandoff(path, record); err != nil {
						return err
					}
				}
				cleanupActive = true
				err := m.authHandoff(ctx, portalHandoffEvent("home", "cancelled"))
				if scenario == "remove-error" {
					if !errors.Is(err, wantErr) {
						return fmt.Errorf("cleanup error=%v want sentinel", err)
					}
				} else if scenario == "partial" || scenario == "directory" || scenario == "oversized" {
					if err == nil {
						return fmt.Errorf("%s malformed record was silently removed", scenario)
					}
				} else if err != nil {
					return err
				}
				_, statErr := os.Stat(path)
				wantFile := scenario == "stale-pid" || scenario == "new-url" || scenario == "partial" || scenario == "directory" || scenario == "oversized" || scenario == "remove-error"
				if wantFile != (statErr == nil) {
					return fmt.Errorf("cleanup %s exists=%t want=%t", scenario, statErr == nil, wantFile)
				}
				return nil
			}
			if scenario == "remove-error" {
				serveRemoveAuthHandoffFn = func(path string) error {
					if cleanupActive {
						return wantErr
					}
					return removeAuthHandoff(path)
				}
			}
			serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
			options := foregroundOptions{AuthHandoffPath: path}
			if scenario == "no-path" {
				options.AuthHandoffPath = ""
			}
			if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", options); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestF5ReviewPortalOnlyStatus(t *testing.T) {
	paths := peopleTestPaths(t)
	paths.AuthHandoff = filepath.Join(filepath.Dir(paths.Registry), "auth-handoff.json")
	reg := &registry.Registry{SchemaVersion: 2, Services: []registry.Service{}, Portal: &registry.PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Registry, data, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	withStatusURLSeams(t, true, 42, now.Add(-time.Minute))
	fp, err := tsRuntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"starting", "running", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			snapshot := tsRuntime.NewSnapshot(42, now.Add(-time.Minute), fp, now, nil)
			snapshot.Portal = tsRuntime.PortalState{Enabled: true, Hostname: "home", State: state}
			if state == "running" {
				snapshot.Portal.URL = "https://home.tailnet.ts.net"
			}
			if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
				t.Fatal(err)
			}
			if state == "cancelled" {
				if err := removeAuthHandoff(paths.AuthHandoff); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := saveAuthHandoff(paths.AuthHandoff, newAuthHandoffRecord("home", "https://login.example.invalid/fixture", 42)); err != nil {
					t.Fatal(err)
				}
			}
			result, err := readOnlyStatus.getPollableStatus(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
			if err != nil {
				t.Fatal(err)
			}
			if result.AuthorizedServiceCount != 0 {
				t.Fatal("portal inflated app count")
			}
			if state == "running" && (!result.Authenticated || !result.NodeAuthorized || result.AuthStatus != authStatusAuthenticated || result.AuthURL != "") {
				t.Fatalf("completed portal-only status=%+v", result)
			}
			if state == "starting" && (result.AuthStatus != authStatusNeedsLogin || result.AuthURL == "") {
				t.Fatal("pending enrollment offer missing")
			}
			if state == "cancelled" && (result.AuthStatus == authStatusNeedsLogin || result.AuthURL != "") {
				t.Fatal("cancelled enrollment offer remains")
			}
		})
	}
}

func TestPortalHandoffConcurrentPublisher(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	path := filepath.Join(dir, "auth-handoff.json")
	read, release := make(chan struct{}), make(chan struct{})
	serveSaveAuthHandoffFn, serveRemoveAuthHandoffFn = saveAuthHandoff, removeAuthHandoff
	serveLoadAuthHandoffsFn = func(path string) ([]authHandoffRecord, error) {
		record, err := loadAuthHandoffs(path)
		if len(record) > 0 && record[0].Service == "home" {
			close(read)
			<-release
		}
		return record, err
	}
	m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
	m.check = func(ctx context.Context, m *portalHandoffRunner) error {
		if err := m.authHandoff(ctx, portalHandoffEvent("home", "pending")); err != nil {
			return err
		}
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- m.authHandoff(ctx, portalHandoffEvent("home", "cancelled")) }()
		<-read
		go func() { second <- m.authHandoff(ctx, portalHandoffEvent("photos", "pending")) }()
		// Allow the attempted publisher to run while the terminal read
		// is held. The mutex must keep that writer out of this window.
		secondFinished := false
		var secondErr error
		select {
		case secondErr = <-second:
			secondFinished = true
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		if err := <-first; err != nil {
			return err
		}
		if !secondFinished {
			secondErr = <-second
		}
		if secondErr != nil {
			return secondErr
		}
		record, err := loadAuthHandoff(path)
		if err != nil || record.Service != "photos" {
			return fmt.Errorf("concurrent pending offer lost: %+v %v", record, err)
		}
		return nil
	}
	serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
	if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path}); err != nil {
		t.Fatal(err)
	}
}
