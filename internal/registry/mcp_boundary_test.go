package registry

import (
	"context"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/mcpscope"
)

func TestMCPRegistryWritersRecheckAfterLockWait(t *testing.T) {
	writers := map[string]func(context.Context, string) error{
		"add": func(ctx context.Context, path string) error {
			_, err := AddWithOutcome(path, Service{Name: "new-app", Type: TypeProxy, Target: "http://localhost:3000"}, AddOptions{Context: ctx})
			return err
		},
		"share": func(ctx context.Context, path string) error {
			_, err := AddTentativeContext(ctx, path, Service{Name: "new-app", Type: TypeProxy, Target: "http://localhost:3000"})
			return err
		},
		"template": func(ctx context.Context, path string) error {
			_, err := AddIfMissingContext(ctx, path, Service{Name: "new-app", Type: TypeProxy, Target: "http://localhost:3000"})
			return err
		},
		"rearm": func(ctx context.Context, path string) error {
			_, err := MutateServiceTentativeContext(ctx, path, "photos", func(s Service) (Service, error) { s.Tags = []string{"tag:updated"}; return s, nil })
			return err
		},
		"adopt": func(ctx context.Context, path string) error {
			reg, err := Load(path)
			if err != nil {
				return err
			}
			_, err = KeepIfUnchangedContext(ctx, path, reg.Services[0])
			return err
		},
		"person-create": func(ctx context.Context, path string) error {
			_, err := ChangePersonContext(ctx, path, "new-person", []string{"photos"}, nil, false, false)
			return err
		},
		"person-remove": func(ctx context.Context, path string) error {
			_, err := RemovePersonContext(ctx, path, "alice")
			return err
		},
		"person-grant": func(ctx context.Context, path string) error {
			s, _ := mcpscope.FromContext(ctx)
			_, err := ChangePersonApp(path, s, "alice", "photos", "1h", false, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }, ctx)
			return err
		},
		"restart": func(ctx context.Context, path string) error {
			s, _ := mcpscope.FromContext(ctx)
			_, err := RequestAppRestart(path, s, "photos", time.Now, ctx)
			return err
		},
		"person-invite": func(ctx context.Context, path string) error {
			return SavePersonInviteWithOptions(path, "alice", PersonInvite{App: "photos", Hostname: "photos", NodeID: "node", State: PersonInvitePending}, PersonInviteSaveOptions{Context: ctx, Now: time.Now()})
		},
		"compensate-create": func(ctx context.Context, path string) error {
			reg, err := Load(path)
			if err != nil {
				return err
			}
			_, err = RemoveIfUnchangedContext(ctx, path, reg.Services[0])
			return err
		},
		"compensate-rearm": func(ctx context.Context, path string) error {
			reg, err := Load(path)
			if err != nil {
				return err
			}
			_, err = RestoreTentativeIfUnchangedContext(ctx, path, reg.Services[0], reg.Services[0])
			return err
		},
		"remove": func(ctx context.Context, path string) error {
			_, _, err := RemoveAndReturnWithinContext(ctx, path, "photos", func(_ Service, commit func() error) error { return commit() })
			return err
		},
	}
	for name, write := range writers {
		for _, state := range []string{"active", "expired", "cancelled"} {
			t.Run(name+"/"+state, func(t *testing.T) {
				path := peopleFixture(t)
				if _, err := ChangePerson(path, "alice", []string{"photos"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := filelock.Lock(lock); err != nil {
					t.Fatal(err)
				}
				defer filelock.Unlock(lock)
				entered := make(chan struct{})
				previous := lockFn
				lockFn = func(f *os.File) error { close(entered); return filelock.Lock(f) }
				defer func() { lockFn = previous }()
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				expiry := now.Add(2 * time.Hour)
				var expired atomic.Bool
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, mcpscope.Session{Who: "owner", Scope: mcpscope.Scope{Role: "owner"}, ExpiresAt: &expiry}), func() time.Time {
					if expired.Load() {
						return expiry
					}
					return now
				})
				done := make(chan error, 1)
				go func() { done <- write(ctx, path) }()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("writer did not reach registry lock")
				}
				if state == "expired" {
					expired.Store(true)
				}
				if state == "cancelled" {
					cancel()
				}
				if err := filelock.Unlock(lock); err != nil {
					t.Fatal(err)
				}
				var writeErr error
				select {
				case writeErr = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("writer did not finish")
				}
				if state == "active" {
					if writeErr != nil {
						t.Fatal(writeErr)
					}
				} else {
					if code, _ := ErrorCode(writeErr); code != mcpscope.DeniedCode {
						t.Fatalf("code=%q err=%v", code, writeErr)
					}
					after, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) {
						t.Error("inactive session changed registry")
					}
				}
			})
		}
	}
}

func TestMCPPersonCreationRequiresOwner(t *testing.T) {
	for _, role := range []string{"people-manager", "app-operator", "owner"} {
		for _, revoke := range []bool{false, true} {
			t.Run(role+map[bool]string{false: "/grant", true: "/revoke"}[revoke], func(t *testing.T) {
				path := peopleFixture(t)
				s := mcpscope.Session{Scope: mcpscope.Scope{Role: role, Apps: []string{"photos"}, MaxDuration: "1h"}}
				now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
				before, _ := os.ReadFile(path)
				p, err := ChangePersonApp(path, s, "alice", "photos", "1h", revoke, func() time.Time { return now })
				if !revoke && role != "owner" {
					if code, _ := ErrorCode(err); code != "mcp_person_owner_required" {
						t.Fatalf("code=%q err=%v", code, err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				reg, err := Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if role == "owner" && !revoke {
					if len(reg.People) != 1 || p.Login != "alice" || !PersonGrantActiveAt(p, "photos", now) {
						t.Fatalf("person=%+v registry=%+v", p, reg)
					}
				} else {
					after, _ := os.ReadFile(path)
					if len(reg.People) != 0 || !reflect.DeepEqual(before, after) {
						t.Fatal("unknown login altered people or app state")
					}
				}
			})
		}
	}
}

func TestMCPRemovalCommitRechecksSession(t *testing.T) {
	path := peopleFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(mcpscope.WithSession(context.Background(), mcpscope.Session{Scope: mcpscope.Scope{Role: "owner"}}))
	defer cancel()
	_, removed, err := RemoveAndReturnWithinContext(ctx, path, "photos", func(_ Service, commit func() error) error { cancel(); return commit() })
	if code, _ := ErrorCode(err); code != mcpscope.DeniedCode || removed {
		t.Fatalf("removed=%v code=%q err=%v", removed, code, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cancelled commit changed registry")
	}
}

func TestReplaceIfUnchangedPreservesConcurrentReplacement(t *testing.T) {
	path := t.TempDir() + "/registry.json"
	_, err := Add(path, Service{Name: "photos", Type: TypeProxy, Target: "http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	original := stored.Services[0]
	replacement := original
	replacement.Tags = []string{"tag:updated"}
	if replaced, err := ReplaceIfUnchanged(path, original, replacement); err != nil || !replaced {
		t.Fatalf("replace=%v err=%v", replaced, err)
	}
	if replaced, err := ReplaceIfUnchanged(path, original, original); err != nil || replaced {
		t.Fatalf("stale replace=%v err=%v", replaced, err)
	}
	reg, err := Load(path)
	if err != nil || !reflect.DeepEqual(reg.Services[0], replacement) {
		t.Fatalf("persisted=%+v err=%v", reg, err)
	}
}
