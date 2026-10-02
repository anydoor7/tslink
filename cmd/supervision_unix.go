//go:build darwin || linux

package cmd

import (
	"context"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

func managerOutputChecked(ctx context.Context, name string, args ...string) ([]byte, error) {
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return nil, err
	}
	return managerOutputFn(ctx, name, args...)
}

func runBoundedManagerCommand(ctx context.Context, name string, timeout time.Duration, args ...string) ([]byte, error) {
	return runBoundedManagerCommandContext(ctx, name, timeout, args...)
}
