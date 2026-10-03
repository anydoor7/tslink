package registry

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
)

func TestGuestRevokeCrashTransaction(t *testing.T) {
	if mode := os.Getenv("GUEST_TEST_CRASH_CHILD"); mode != "" {
		path := os.Getenv("GUEST_TEST_REGISTRY")
		ready := os.Getenv("GUEST_TEST_READY")
		id := os.Getenv("GUEST_TEST_GRANT")
		if mode == "before" || mode == "after" {
			if _, reason := CheckGuest(path, "photos", id, guestTestNow, true, false); reason != "allowed" {
				os.Exit(34)
			}
		}
		original := marshalFn
		if mode == "before" {
			marshalFn = func(v any, prefix, indent string) ([]byte, error) {
				raw, e := original(v, prefix, indent)
				if e == nil {
					if err := os.WriteFile(ready, []byte("before_publish"), 0600); err != nil {
						os.Exit(31)
					}
					for {
						time.Sleep(time.Second)
					}
				}
				return raw, e
			}
		}
		if _, e := RevokeGuest(path, id, time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)); e != nil {
			os.Exit(32)
		}
		if mode == "after" {
			if e := os.WriteFile(ready, []byte("after_publish"), 0600); e != nil {
				os.Exit(33)
			}
			for {
				time.Sleep(time.Second)
			}
		}
		return
	}
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			path := filepath.Join(dir, "registry.json")
			now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
			if _, e := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://127.0.0.1:9999"}); e != nil {
				t.Fatal(e)
			}
			view, _, e := CreateGuest(path, CreateGuestOptions{App: "photos", Value: "2h", PublicAck: true, Now: now, Policy: duration.Policy{}})
			if e != nil {
				t.Fatal(e)
			}
			before, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			ready := filepath.Join(dir, "ready")
			child := exec.Command(os.Args[0], "-test.run=^TestGuestRevokeCrashTransaction$")
			child.Env = append(os.Environ(), "GUEST_TEST_CRASH_CHILD="+mode, "GUEST_TEST_REGISTRY="+path, "GUEST_TEST_READY="+ready, "GUEST_TEST_GRANT="+view.ID)
			if e = child.Start(); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() {
				if child.ProcessState == nil {
					child.Process.Kill()
					child.Wait()
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, e := os.Stat(ready); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child did not reach transaction point")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if e = child.Process.Kill(); e != nil {
				t.Fatal(e)
			}
			if e = child.Wait(); e == nil {
				t.Fatal("child should be killed")
			}
			raw, e := os.ReadFile(path)
			if e != nil || !json.Valid(raw) {
				t.Fatal("complete registry expected", e)
			}
			current, e := ShowGuest(path, view.ID, now)
			if e != nil {
				t.Fatal(e)
			}
			if current.Revoked != (mode == "after") {
				t.Fatal("wrong persisted revoke state")
			}
			wantUses := uint64(0)
			if mode == "after" {
				wantUses = 1
			}
			if current.Uses != wantUses {
				t.Fatalf("counter crash boundary: uses=%d want=%d", current.Uses, wantUses)
			}
			if mode == "before" && string(raw) != string(before) {
				t.Fatal("prepublish changed bytes")
			}
			// A fresh process must acquire the formerly held lock and make progress.
			next := exec.Command(os.Args[0], "-test.run=^TestGuestRevokeCrashTransaction$")
			next.Env = append(os.Environ(), "GUEST_TEST_CRASH_CHILD=restart", "GUEST_TEST_REGISTRY="+path, "GUEST_TEST_GRANT="+view.ID)
			out, e := next.CombinedOutput()
			if e != nil {
				t.Fatal("restart write", e, strings.TrimSpace(string(out)))
			}
			_, reason := CheckGuest(path, "photos", view.ID, now, true, false)
			if reason != "revoked" {
				t.Fatal("restart auth", reason)
			}
			t.Logf("killed=%s registry_complete=true before_bytes_preserved=%t persisted_revoked=%t fresh_process_write=true final_auth=%s", mode, mode == "before", current.Revoked, reason)
		})
	}
}
