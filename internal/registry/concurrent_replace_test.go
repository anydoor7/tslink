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
	readers := []struct {
		name string
		read func() error
	}{
		{"Load", func() error { _, err := Load(path); return err }},
		{"LoadForRuntime", func() error { _, _, err := LoadForRuntime(path); return err }},
		{"LoadWithFileState", func() error { _, _, err := LoadWithFileState(path); return err }},
		{"LoadForDiagnostics", func() error { _, _, err := LoadForDiagnostics(path); return err }},
		{"Preflight", func() error { _, _, err := Preflight(path); return err }},
		{"loadForMutation", func() error { _, err := loadForMutation(path); return err }},
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
					if errors.As(err, &errno) {
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
