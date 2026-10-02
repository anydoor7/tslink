//go:build windows

package cmd

import (
	"context"
	"github.com/anydoor7/tslink/internal/daemon"
)

func serveShutdownContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	return daemon.ShutdownContext(parent)
}
