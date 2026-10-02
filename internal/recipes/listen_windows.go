//go:build windows

package recipes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

var netstatOutputFn = func(ctx context.Context) ([]byte, error) {
	// Omitting -p keeps both IPv4 and IPv6; parseNetstat discards UDP rows.
	return exec.CommandContext(ctx, filepath.Join(os.Getenv("SystemRoot"), "System32", "netstat.exe"), "-ano").Output()
}

func listeningTCP(ctx context.Context) ([]Listener, error) {
	b, err := netstatOutputFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("enumerate TCP listeners with netstat: %w", err)
	}
	return parseNetstat(string(b)), nil
}
