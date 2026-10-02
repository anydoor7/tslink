//go:build linux

package recipes

import (
	"context"
	"fmt"
	"os"
)

var readProcFn = os.ReadFile

func listeningTCP(ctx context.Context) ([]Listener, error) {
	var result []Listener
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := readProcFn(path)
		if os.IsNotExist(err) && path == "/proc/net/tcp6" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("enumerate TCP listeners: %w", err)
		}
		result = append(result, parseProcTCP(string(b))...)
	}
	return result, nil
}
