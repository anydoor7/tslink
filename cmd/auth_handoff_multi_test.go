package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestAuthHandoffCollectionMigrationAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-handoff.json")
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	oldNow, oldPID := authHandoffNowFn, readPIDFn
	authHandoffNowFn = func() time.Time { return now }
	readPIDFn = func(string) (int, error) { return 42, nil }
	t.Cleanup(func() { authHandoffNowFn, readPIDFn = oldNow, oldPID })
	home := newAuthHandoffRecordAt("home", "https://login.example.invalid/home", 42, now)
	legacy, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := loadAuthHandoff(path)
	if err != nil || got != home {
		t.Fatalf("legacy positive control: %+v %v", got, err)
	}
	photos := newAuthHandoffRecordAt("photos", "https://login.example.invalid/photos", 42, now.Add(time.Minute))
	if err := saveAuthHandoff(path, photos); err != nil {
		t.Fatal(err)
	}
	for _, want := range []authHandoffRecord{home, photos} {
		got, ok := validAuthHandoffForService(filepath.Join(filepath.Dir(path), "tslink.pid"), want.Service)
		if !ok || got != want {
			t.Fatalf("node lookup: %+v %t want %+v", got, ok, want)
		}
	}
	if _, ok := validAuthHandoffForService(filepath.Join(filepath.Dir(path), "tslink.pid"), "docs"); ok {
		t.Fatal("unrelated node received another login")
	}
	got, err = loadAuthHandoff(path)
	if err != nil || got != home {
		t.Fatalf("oldest compatibility entry: %+v %v", got, err)
	}
	home.AuthURL += "-rotated"
	if err := saveAuthHandoff(path, home); err != nil {
		t.Fatal(err)
	}
	entries, err := loadAuthHandoffs(path)
	if err != nil || !reflect.DeepEqual(entries, []authHandoffRecord{photos, home}) {
		t.Fatalf("rotation did not preserve other node: %+v %v", entries, err)
	}
	now = photos.ExpiresAt
	if _, ok := validAuthHandoffForService(filepath.Join(filepath.Dir(path), "tslink.pid"), "photos"); ok {
		t.Fatal("expired login accepted")
	}
	// A new daemon cannot inherit the previous process's pending offers.
	home.DaemonPID = 43
	if err := saveAuthHandoff(path, home); err != nil {
		t.Fatal(err)
	}
	entries, err = loadAuthHandoffs(path)
	if err != nil || !reflect.DeepEqual(entries, []authHandoffRecord{home}) {
		t.Fatalf("PID replacement: %+v %v", entries, err)
	}
	if _, ok := validAuthHandoffForService(filepath.Join(filepath.Dir(path), "tslink.pid"), "home"); ok {
		t.Fatal("stale PID accepted")
	}
	if _, ok := validAuthHandoffForService(filepath.Join(t.TempDir(), "tslink.pid"), "home"); ok {
		t.Fatal("missing handoff accepted")
	}
}

func TestAuthHandoffCollectionFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-handoff.json")
	record := newAuthHandoffRecord("home", "https://login.example.invalid/home", 42)
	for _, tc := range []struct{ name, data, message string }{
		{"partial", `{"schema_version":2,"entries":[`, "decode"},
		{"unknown", `{"schema_version":99}`, "unsupported"},
		{"empty", `{"schema_version":2,"entries":[]}`, "empty"},
		{"invalid", `{"schema_version":2,"entries":[{"schema_version":1,"status":"needs_login","auth_url":"","daemon_pid":42}]}`, "invalid"},
		{"legacy-time", `{"schema_version":1,"expires_at":7}`, "decode legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			if err := saveAuthHandoff(path, record); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("malformed document was replaced: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != tc.data {
				t.Fatalf("failed update changed previous file: %q %v", got, err)
			}
		})
	}
	if err := writeAuthHandoffs(path, []authHandoffRecord{record, record}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAuthHandoffs(path); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("duplicate nodes accepted: %v", err)
	}
	record.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeAuthHandoffs(path, []authHandoffRecord{record}); err == nil || !strings.Contains(err.Error(), "encode") {
		t.Fatalf("invalid timestamp: %v", err)
	}
	record = newAuthHandoffRecord("home", strings.Repeat("x", maxHandoffBytes), 42)
	if err := writeAuthHandoffs(path, []authHandoffRecord{record}); err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized write: %v", err)
	}
	record = newAuthHandoffRecord("home", "https://login.example.invalid/home", 42)
	if err := writeAuthHandoffs(filepath.Join(path, "child"), []authHandoffRecord{record}); err == nil || !strings.Contains(err.Error(), "write auth handoff") {
		t.Fatalf("file used as parent: %v", err)
	}
}

func TestAuthHandoffCollectionConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth-handoff.json")
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- saveAuthHandoff(path, newAuthHandoffRecordAt(fmt.Sprintf("node-%d", i), "https://login.example.invalid/fixture", 42, time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := loadAuthHandoffs(path)
	if err != nil || len(entries) != 20 {
		t.Fatalf("concurrent update lost nodes: %d %v", len(entries), err)
	}
}

func TestAuthHandoffCollectionExactTerminalAndWriteFailure(t *testing.T) {
	dir := t.TempDir()
	mockServeDefaults(t, dir)
	path := filepath.Join(dir, "auth-handoff.json")
	serveSaveAuthHandoffFn = saveAuthHandoff
	m := &portalHandoffRunner{mockInteractiveServer: &mockInteractiveServer{}}
	m.check = func(ctx context.Context, m *portalHandoffRunner) error {
		for _, name := range []string{"home", "photos", "docs"} {
			if err := m.authHandoff(ctx, portalHandoffEvent(name, "pending")); err != nil {
				return err
			}
		}
		rotated := portalHandoffEvent("photos", "pending")
		rotated.AuthURL += "-new"
		if err := m.authHandoff(ctx, rotated); err != nil {
			return err
		}
		if err := m.authHandoff(ctx, portalHandoffEvent("photos", "cancelled")); err != nil {
			return err
		}
		entries, err := loadAuthHandoffs(path)
		if err != nil || len(entries) != 3 || entries[2].AuthURL != rotated.AuthURL {
			return fmt.Errorf("stale terminal removed rotated offer: %+v %v", entries, err)
		}
		rotated.State = "complete"
		if err := m.authHandoff(ctx, rotated); err != nil {
			return err
		}
		entries, err = loadAuthHandoffs(path)
		if err != nil || len(entries) != 2 || entries[0].Service != "home" || entries[1].Service != "docs" {
			return fmt.Errorf("exact terminal lost another node: %+v %v", entries, err)
		}
		return nil
	}
	serveNewServerFn = func(string, string) (serverRunner, error) { return m, nil }
	if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path}); err != nil {
		t.Fatal(err)
	}
	// The callback captures the writer before workers start. A real filesystem
	// failure in replacement must propagate while retaining both old entries.
	serveWriteAuthHandoffsFn = func(path string, entries []authHandoffRecord) error {
		return writeAuthHandoffs(filepath.Join(path, "child"), entries)
	}
	m.check = func(ctx context.Context, m *portalHandoffRunner) error {
		for _, name := range []string{"home", "photos"} {
			if err := m.authHandoff(ctx, portalHandoffEvent(name, "pending")); err != nil {
				return err
			}
		}
		if err := m.authHandoff(ctx, portalHandoffEvent("home", "complete")); err == nil || !strings.Contains(err.Error(), "write auth handoff") {
			return fmt.Errorf("rewrite failure not propagated: %v", err)
		}
		entries, err := loadAuthHandoffs(path)
		if err != nil || len(entries) != 2 {
			return fmt.Errorf("rewrite failure lost previous entries: %+v %v", entries, err)
		}
		return nil
	}
	if err := runForegroundWithOptions(filepath.Join(dir, "tslink.pid"), "", "", foregroundOptions{AuthHandoffPath: path}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusListsEveryPendingLogin(t *testing.T) {
	paths := peopleTestPaths(t)
	paths.AuthHandoff = filepath.Join(filepath.Dir(paths.Registry), "auth-handoff.json")
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 2, 12, 0, 0, 0, time.UTC)
	oldNow, oldAuthNow := statusNowFn, authHandoffNowFn
	statusNowFn, authHandoffNowFn = func() time.Time { return now }, func() time.Time { return now }
	t.Cleanup(func() { statusNowFn, authHandoffNowFn = oldNow, oldAuthNow })
	withStatusURLSeams(t, true, 42, now.Add(-time.Minute))
	for _, name := range []string{"home", "photos", "docs"} {
		if err := saveAuthHandoff(paths.AuthHandoff, newAuthHandoffRecordAt(name, "https://login.example.invalid/"+name, 42, now)); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(want []string) {
		t.Helper()
		result, err := readOnlyStatus.getPollableStatus(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		var human bytes.Buffer
		formatStatus(result, &human)
		for _, pending := range result.PendingLogins {
			names = append(names, pending.Node)
			if !strings.Contains(human.String(), "pending login for "+pending.Node+": "+pending.AuthURL) {
				t.Fatalf("human output lost node: %s", human.String())
			}
		}
		if !reflect.DeepEqual(names, want) || result.AuthURL != "https://login.example.invalid/"+want[0] || result.AuthStatus != authStatusNeedsLogin {
			t.Fatalf("pending list/oldest compatibility: %+v want %v", result, want)
		}
		urls, err := readOnlyStatus.getStatusURLsWithAuth(context.Background(), paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
		if err != nil || !reflect.DeepEqual(urls.PendingLogins, result.PendingLogins) {
			t.Fatalf("URL status lost pending entries: %+v %v", urls, err)
		}
		for _, value := range []any{result, urls} {
			data, err := json.Marshal(value)
			if err != nil || !bytes.Contains(data, []byte(`"pending_logins"`)) {
				t.Fatalf("JSON lost pending logins: %s %v", data, err)
			}
		}
	}
	assert([]string{"home", "photos", "docs"})
	// Completed oldest node must not hide still-pending entries behind it.
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := tsruntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tsruntime.NewSnapshot(42, now.Add(-time.Minute), fp, now, nil)
	snapshot.Portal = tsruntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net"}
	if err := tsruntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	assert([]string{"photos", "docs"})
	photos := newAuthHandoffRecordAt("photos", "https://login.example.invalid/photos", 42, now.Add(-authHandoffConservativeLifetime))
	if err := saveAuthHandoff(paths.AuthHandoff, photos); err != nil {
		t.Fatal(err)
	}
	assert([]string{"docs"})
}
