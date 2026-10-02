//go:build darwin

package recipes

import (
	"context"
	"fmt"
	"os/exec"
)

var lsofOutputFn = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fn").Output()
}

func listeningTCP(ctx context.Context) ([]Listener, error) {
	b, err := lsofOutputFn(ctx)
	// lsof returns 1 when no sockets match; only empty output is accepted.
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 1 && len(b) == 0 && len(e.Stderr) == 0 {
		return []Listener{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("enumerate TCP listeners with lsof: %w", err)
	}
	return parseLsof(string(b)), nil
}
