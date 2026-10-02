//go:build !linux && !darwin && !windows

package recipes

import (
	"context"
	"fmt"
)

func listeningTCP(context.Context) ([]Listener, error) {
	return nil, fmt.Errorf("app discovery supports macOS, Linux and Windows")
}
