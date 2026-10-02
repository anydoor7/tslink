//go:build !windows

package cmd

import "context"

func serveShutdownContext(parent context.Context) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(parent)
	return ctx, cancel, nil
}
