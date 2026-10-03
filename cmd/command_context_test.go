package cmd

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// Direct RunE calls bypass Cobra's Execute context initialization. Give each
// fixture its own live context and restore the exact prior context (even nil),
// so a global command cannot make a later test pass by accident.
func setCommandTestContext(t *testing.T, command *cobra.Command) {
	t.Helper()
	previous := command.Context()
	command.SetContext(t.Context())
	t.Cleanup(func() { command.SetContext(previous) })
}

func TestCommandTestContextIsolation(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancelDeadline()
	type key struct{}
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{"fresh", nil},
		{"background", context.Background()},
		{"canceled", canceled},
		{"expired", deadline},
		{"valued", context.WithValue(context.Background(), key{}, "previous caller")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := &cobra.Command{}
			command.SetContext(tc.ctx)
			t.Run("invoke", func(t *testing.T) {
				setCommandTestContext(t, command)
				command.RunE = func(command *cobra.Command, _ []string) error {
					if command.Context() != t.Context() {
						t.Fatal("handler did not receive the current test context")
					}
					return command.Context().Err()
				}
				if err := command.RunE(command, nil); err != nil {
					t.Fatalf("fresh handler inherited cancellation: %v", err)
				}
			})
			if command.Context() != tc.ctx {
				t.Fatal("command context leaked across fixture cleanup")
			}
		})
	}
}
