package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSetHealthContextTransaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	svc := Service{Name: "app", Type: TypeProxy, Target: "http://localhost:9", Tags: []string{"tag:web"}, AllowedUsers: []string{"bob@example.com"}}
	if _, err := Add(path, svc); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p := "/health"
	got, changed, err := SetHealthContext(context.Background(), path, "app", HealthUpdate{Path: &p}, nil)
	if err != nil || !changed || got.Health == nil || got.Health.Path != "/health" || len(got.AllowedUsers) != 1 || got.Tags[0] != "tag:web" {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	// The authorizer sees the stored service and can refuse before any write.
	refused := errors.New("refused")
	written, _ := os.ReadFile(path)
	max := 401
	_, _, err = SetHealthContext(context.Background(), path, "app", HealthUpdate{StatusMax: &max}, func(reg *Registry, current Service) error {
		if current.Health == nil || current.Health.Path != "/health" || len(reg.Services) != 1 {
			t.Errorf("authorizer saw %+v", current)
		}
		return refused
	})
	if !errors.Is(err, refused) {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, written) {
		t.Fatal("refused change was written")
	}
	if _, _, err := SetHealthContext(context.Background(), path, "missing", HealthUpdate{Path: &p}, nil); func() string { c, _ := ErrorCode(err); return c }() != "not_found" {
		t.Fatal(err)
	}
	got, changed, err = SetHealthContext(context.Background(), path, "app", HealthUpdate{Reset: true}, nil)
	if err != nil || !changed || got.Health != nil {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, original) {
		t.Fatalf("reset is not byte-identical:\n%s\n%s", original, after)
	}
	if _, changed, err := SetHealthContext(context.Background(), path, "app", HealthUpdate{Reset: true}, nil); err != nil || changed {
		t.Fatal(changed, err)
	}
}

func TestSetHealthContextRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	for _, svc := range []Service{
		{Name: "app", Type: TypeProxy, Target: "http://localhost:9"},
		{Name: "db", Type: TypeTCP, Target: "localhost:5432"},
		{Name: "files", Type: TypeFile, Path: t.TempDir()},
	} {
		if _, err := Add(path, svc); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := func(err error) string { c, _ := ErrorCode(err); return c }
	p, body, min, max, timeout, interval := "/health", "ok", 500, 200, "31s", "2m"
	for _, tc := range []struct {
		app  string
		u    HealthUpdate
		want string
	}{
		{"db", HealthUpdate{Path: &p}, CodeHealthOptionUnsupported},
		{"files", HealthUpdate{BodyContains: &body}, CodeHealthOptionUnsupported},
		{"files", HealthUpdate{StatusMin: &min}, CodeHealthOptionUnsupported},
		{"db", HealthUpdate{StatusMax: &max}, CodeHealthOptionUnsupported},
		{"app", HealthUpdate{StatusMin: &min, StatusMax: &max}, "usage_error"},
		{"app", HealthUpdate{Timeout: &timeout}, "usage_error"},
	} {
		if _, changed, err := SetHealthContext(context.Background(), path, tc.app, tc.u, nil); code(err) != tc.want || changed {
			t.Fatalf("%s %+v: %v changed=%v, want %s", tc.app, tc.u, err, changed, tc.want)
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("refused changes were written")
	}
	// Timeout and interval apply to every service type.
	got, changed, err := SetHealthContext(context.Background(), path, "db", HealthUpdate{Interval: &interval}, nil)
	if err != nil || !changed || got.Health == nil || got.Health.Interval != "2m" {
		t.Fatalf("%+v %v %v", got, changed, err)
	}
	// A registry that cannot be read is never replaced.
	if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := SetHealthContext(context.Background(), path, "db", HealthUpdate{Interval: &interval}, nil); err == nil || changed {
		t.Fatalf("corrupt registry accepted: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != "broken" {
		t.Fatal("corrupt registry was rewritten")
	}
}

func TestHealthUpdateApply(t *testing.T) {
	empty, zero, path, body := "", 0, "/ready", "ok"
	current := &HealthConfig{Path: "/old", StatusMin: 204, BodyContains: "x", Interval: "2m"}
	got := HealthUpdate{Path: &path, BodyContains: &empty, StatusMin: &zero}.Apply(current)
	if *got != (HealthConfig{Path: "/ready", Interval: "2m"}) || current.Path != "/old" {
		t.Fatalf("%+v %+v", got, current)
	}
	if got := (HealthUpdate{Reset: true, BodyContains: &body}).Apply(current); *got != (HealthConfig{BodyContains: "ok"}) {
		t.Fatalf("%+v", got)
	}
	if got := (HealthUpdate{Path: &empty}).Apply(&HealthConfig{Path: "/x"}); got != nil {
		t.Fatalf("an all-default block must be absent: %+v", got)
	}
}
