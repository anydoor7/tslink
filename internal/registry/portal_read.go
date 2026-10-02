package registry

import (
	"fmt"
	"io"
)

// PortalPreflight is a bounded, read-only registry read. It rejects special
// files before reading and uses a nonblocking open on Unix to avoid FIFO stalls
// even if a concurrent writer replaces the path between checks.
func PortalPreflight(path string) (*Registry, []ServiceIssue, error) {
	f, err := openPortalRegistry(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	const maxRegistryBytes = 4 << 20
	if !info.Mode().IsRegular() || info.Size() > maxRegistryBytes {
		return nil, nil, fmt.Errorf("portal registry must be a regular file of at most 4 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxRegistryBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxRegistryBytes {
		return nil, nil, fmt.Errorf("portal registry exceeds 4 MiB")
	}
	return decodeForRuntime(data)
}
