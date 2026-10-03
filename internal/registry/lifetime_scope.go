package registry

import (
	"context"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/mcpscope"
)

// Called under the registry lock after resolving the final audience/deadline.
// A launch scope supplements policy; it never relaxes the owner policy.
func authorizeLifetime(ctx context.Context, tool, app string, lifetime duration.Lifetime, now time.Time) error {
	ctx = mutationContext(ctx)
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return err
	}
	if session, ok := mcpscope.FromContext(ctx); ok {
		if err := session.Authorize(tool, []string{app}, now); err != nil {
			return err
		}
		return session.CheckLifetime(lifetime, now)
	}
	return nil
}
