package registry

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
)

// The same workload is injected unchanged into the baseline and fixed trees.
func TestRegistryConcurrentReplacementReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	reg := &Registry{SchemaVersion: CurrentRegistrySchemaVersion, Services: []Service{{Name: "photos", Type: TypeProxy, Target: "http://localhost:3000"}}}
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	// A successful load must still contain the one saved service: a nil error
	// with an empty or "missing" registry is the silent failure mode of a
	// reader that briefly cannot see the file mid-replace.
	errFalseEmpty := errors.New("successful load without the saved service")
	check := func(got *Registry, err error) error {
		if err != nil {
			return err
		}
		if got == nil || len(got.Services) != 1 || got.Services[0].Name != "photos" {
			return errFalseEmpty
		}
		return nil
	}
	readers := []struct {
		name string
		read func() error
	}{
		{"Load", func() error { return check(Load(path)) }},
		{"LoadForRuntime", func() error { got, _, err := LoadForRuntime(path); return check(got, err) }},
		{"LoadWithFileState", func() error {
			got, state, err := LoadWithFileState(path)
			if err == nil && state != RegistryFileValid {
				return errFalseEmpty
			}
			return check(got, err)
		}},
		{"LoadForDiagnostics", func() error { got, _, err := LoadForDiagnostics(path); return check(got, err) }},
		{"Preflight", func() error { got, _, err := Preflight(path); return check(got, err) }},
		{"loadForMutation", func() error { return check(loadForMutation(path)) }},
	}
	type result struct {
		name            string
		calls, failures int
		codes           map[string]int
	}
	results := make(chan result, len(readers))
	start, stop := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	for _, reader := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := result{name: reader.name, codes: make(map[string]int)}
			<-start
			for {
				select {
				case <-stop:
					results <- r
					return
				default:
				}
				r.calls++
				if err := reader.read(); err != nil {
					r.failures++
					var errno syscall.Errno
					code := "non-errno"
					if errors.Is(err, errFalseEmpty) {
						code = "false-empty"
					} else if errors.As(err, &errno) {
						code = fmt.Sprint(uintptr(errno))
					}
					r.codes[code]++
				}
			}
		}()
	}
	close(start)
	writes, writeErrors := 500, 0
	writeCodes := make(map[string]int)
	for i := range writes {
		reg.Services[0].Target = fmt.Sprintf("http://localhost:%d", 3000+i%2)
		if err := save(path, reg); err != nil {
			writeErrors++
			var errno syscall.Errno
			code := "non-errno"
			if errors.As(err, &errno) {
				code = fmt.Sprint(uintptr(errno))
			}
			writeCodes[code]++
		}
	}
	close(stop)
	wg.Wait()
	close(results)
	readCalls, readErrors := 0, 0
	for r := range results {
		t.Logf("reader=%s calls=%d errors=%d errno=%v", r.name, r.calls, r.failures, r.codes)
		readCalls += r.calls
		readErrors += r.failures
		if r.calls == 0 {
			t.Errorf("reader %s did not run", r.name)
		}
	}
	t.Logf("toolchain=%s os=%s arch=%s read_calls=%d read_errors=%d writes=%d write_errors=%d write_errno=%v", runtime.Version(), runtime.GOOS, runtime.GOARCH, readCalls, readErrors, writes, writeErrors, writeCodes)
	if readErrors != 0 || writeErrors != 0 {
		t.Fatalf("concurrent registry replacement: %d reader errors, %d writer errors", readErrors, writeErrors)
	}
}
