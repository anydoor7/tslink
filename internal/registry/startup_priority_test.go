package registry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestStartupPriorityRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	for _, name := range []string{"alpha", "beta"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:9"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadStartupPriorities(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v, want no priorities", got, err)
	}
	ctx := context.Background()
	if err := SetStartupPriority(ctx, path, "beta", 10); err != nil {
		t.Fatal(err)
	}
	if err := SetStartupPriority(ctx, path, "alpha", -3); err != nil {
		t.Fatal(err)
	}
	got, err = LoadStartupPriorities(path)
	if err != nil || len(got) != 2 || got["beta"] != 10 || got["alpha"] != -3 {
		t.Fatalf("priorities = %v %v, want beta=10 alpha=-3", got, err)
	}
	info, err := os.Stat(StartupPriorityPath(path))
	if err != nil {
		t.Fatalf("stat hint file: %v", err)
	}
	// Windows has no POSIX permission bits (it reports 0666 for a writable file);
	// the 0600 contract is a Unix property.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("hint file mode = %v, want 0600", info.Mode().Perm())
	}

	// add is incremental: replacing a service keeps its priority.
	if _, err := Add(path, Service{Name: "beta", Type: TypeProxy, Target: "http://localhost:10"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadStartupPriorities(path); got["beta"] != 10 {
		t.Fatalf("priority after add = %v, want beta=10 kept", got)
	}

	if err := SetStartupPriority(ctx, path, "alpha", 0); err != nil {
		t.Fatal(err)
	}
	got, err = LoadStartupPriorities(path)
	if _, kept := got["alpha"]; err != nil || kept || got["beta"] != 10 {
		t.Fatalf("after clearing alpha: %v %v", got, err)
	}
}

func TestSetStartupPriorityRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "alpha", Type: TypeProxy, Target: "http://localhost:9"}); err != nil {
		t.Fatal(err)
	}
	err := SetStartupPriority(context.Background(), path, "ghost", 5)
	if code, _ := ErrorCode(err); code != "not_found" {
		t.Fatalf("unregistered service: %v, want not_found", err)
	}
	if _, err := os.Stat(StartupPriorityPath(path)); !os.IsNotExist(err) {
		t.Fatalf("refused set wrote the hint file: %v", err)
	}
	// Clearing is allowed for a name that is gone, so a stale entry can be removed.
	if err := SetStartupPriority(context.Background(), path, "ghost", 0); err != nil {
		t.Fatalf("clearing an unregistered name: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SetStartupPriority(ctx, path, "alpha", 5); err == nil {
		t.Fatal("cancelled context still wrote")
	}
}

func TestLoadStartupPrioritiesIsStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	for _, tc := range []struct{ body, want string }{
		{`{"schema_version":1,"services":{"a":1},"extra":true}`, `unknown field "extra"`},
		{`{"schema_version":2,"services":{}}`, "unsupported schema_version 2"},
		{`{"schema_version":1,"services":{"a":"high"}}`, "cannot unmarshal"},
		{`{"schema_version":1}{}`, "trailing"},
	} {
		if err := os.WriteFile(StartupPriorityPath(path), []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadStartupPriorities(path); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.body, err, tc.want)
		}
	}
	if err := os.WriteFile(StartupPriorityPath(path), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadStartupPriorities(path); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no services key: %v %v, want an empty map", got, err)
	}
}

// Rollback safety: setting a priority never touches registry.json, so a
// build that predates priorities (and decodes registry.json strictly) reads
// exactly what it read before.
func TestStartupPriorityLeavesRegistryUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "alpha", Type: TypeProxy, Target: "http://localhost:9"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetStartupPriority(context.Background(), path, "alpha", 7); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("registry.json changed:\n%s\n%s", before, after)
	}
	if _, issues, err := LoadForRuntime(path); err != nil || len(issues) != 0 {
		t.Fatalf("strict load after set: %v %v", issues, err)
	}
	// The key that would have broken older builds is not in the registry.
	if bytes.Contains(after, []byte("priority")) {
		t.Fatalf("registry.json carries a priority key:\n%s", after)
	}
}

func TestStartupOrder(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	got := StartupOrder(names, map[string]int{"d": 10, "b": 10, "e": -1, "gone": 99})
	if want := []string{"b", "d", "a", "c", "e"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (higher first, registry order on ties, negatives last)", got, want)
	}
	if !slices.Equal(names, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("input reordered in place: %v", names)
	}
	if got := StartupOrder(names, nil); !slices.Equal(got, names) {
		t.Fatalf("no priorities: %v, want registry order", got)
	}
}

func TestStartupPriorityUnreadableState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if _, err := Add(path, Service{Name: "alpha", Type: TypeProxy, Target: "http://localhost:9"}); err != nil {
		t.Fatal(err)
	}
	hint := StartupPriorityPath(path)
	ctx := context.Background()

	// A hint file this build cannot read is refused, never overwritten.
	malformed := []byte(`{"schema_version":1,"services":{"alpha":1},"typo":1}`)
	if err := os.WriteFile(hint, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetStartupPriority(ctx, path, "alpha", 5); err == nil || !strings.Contains(err.Error(), `unknown field "typo"`) {
		t.Fatalf("set over a malformed hint file: %v", err)
	}
	if got, _ := os.ReadFile(hint); !bytes.Equal(got, malformed) {
		t.Fatalf("malformed hint file was rewritten:\n%s", got)
	}

	if err := os.Remove(hint); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(hint, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStartupPriorities(path); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("hint path is a directory: %v", err)
	}
	if err := os.Remove(hint); err != nil {
		t.Fatal(err)
	}

	old := readRegistryFile
	t.Cleanup(func() { readRegistryFile = old })
	denied := errors.New("synthetic read failure")
	readRegistryFile = func(p string) ([]byte, error) {
		if p == hint {
			return nil, denied
		}
		return old(p)
	}
	if err := os.WriteFile(hint, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStartupPriorities(path); !errors.Is(err, denied) {
		t.Fatalf("read failure: %v, want it reported", err)
	}
	readRegistryFile = old

	if err := os.WriteFile(path, []byte(`{"schema_version":2,"services":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetStartupPriority(ctx, path, "alpha", 5); err == nil || !strings.Contains(err.Error(), "load registry") {
		t.Fatalf("set over a malformed registry: %v", err)
	}
}

// A service isolated by strict load (here: a key from a newer build) is still
// registered, so its priority can be set.
func TestSetStartupPriorityAcceptsIsolatedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	raw := `{"schema_version":2,"services":[{"name":"alpha","type":"proxy","target":"http://localhost:9","created_at":"2026-11-10T00:00:00Z","future_key":true}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetStartupPriority(context.Background(), path, "alpha", 4); err != nil {
		t.Fatalf("set on an isolated entry: %v", err)
	}
	if got, _ := LoadStartupPriorities(path); got["alpha"] != 4 {
		t.Fatalf("priorities = %v, want alpha=4", got)
	}
}
