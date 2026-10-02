package cmd

import (
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

type extendArguments struct {
	Service  string  `json:"service"`
	Who      string  `json:"who,omitempty"`
	For      *string `json:"for,omitempty"`
	Until    *string `json:"until,omitempty"`
	Regrant  bool    `json:"regrant,omitempty"`
	AckNever bool    `json:"ack_never,omitempty"`
}

func lifetimeArgument(relative, until *string) (*string, error) {
	if relative != nil && until != nil {
		return nil, output.ErrUsage("--for and --until are mutually exclusive; valid examples: " + duration.Examples)
	}
	if until != nil {
		value := "until " + *until
		return &value, nil
	}
	return relative, nil
}

// Future access-log integration can observe this post-commit event. It must not
// undo a successful duration change. The hook is synchronous and has no worker.
var durationChangedFn = func(registry.DurationChange) {}

func extendLifetime(path string, args extendArguments, now time.Time) (registry.DurationChange, error) {
	value, err := lifetimeArgument(args.For, args.Until)
	if err != nil {
		return registry.DurationChange{}, err
	}
	if value == nil {
		return registry.DurationChange{}, output.ErrUsage("extend requires --for or --until; valid examples: " + duration.Examples)
	}
	policy, err := config.LoadLifetimePolicy()
	if err != nil {
		return registry.DurationChange{}, err
	}
	result, err := registry.ExtendDuration(path, registry.ExtendOptions{Service: args.Service, Who: args.Who, Value: *value, Regrant: args.Regrant, AckNever: args.AckNever, Policy: policy, Now: now})
	if err == nil {
		durationChangedFn(result)
	}
	return result, err
}

func newExtendCmd() *cobra.Command {
	var who, relative, until string
	var regrant, ackNever bool
	c := &cobra.Command{Use: "extend <service>", Short: "Set a person grant or Funnel TTL deadline; always returns JSON", Long: "Set a new deadline from the operation time, extending or shortening the current lifetime. Select a person with --person; otherwise select Funnel. Expired grants require --regrant, which does not undo a person's revocation. Presets: " + duration.Suggestions + ".", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		path, err := inviteRegistryPathFn()
		if err != nil {
			return err
		}
		args := extendArguments{Service: a[0], Who: who, Regrant: regrant, AckNever: ackNever}
		if c.Flags().Changed("person") && who == "" {
			return output.ErrUsage("--person must not be empty")
		}
		if c.Flags().Changed("for") {
			args.For = &relative
		}
		if c.Flags().Changed("until") {
			args.Until = &until
		}
		result, err := extendLifetime(path, args, durationNowFn())
		if err != nil {
			return err
		}
		output.Success("extend", result)
		return nil
	}}
	c.Flags().StringVar(&who, "person", "", "Person login whose grant for this service is changed; omit for Funnel")
	c.Flags().StringVar(&relative, "for", "", "New lifetime from now; presets "+duration.Suggestions+"; relative or 'until <date/time>'")
	c.Flags().StringVar(&until, "until", "", "Absolute deadline: RFC3339, YYYY-MM-DD or YYYY-MM-DDTHH:MM; local unless offset supplied")
	c.Flags().BoolVar(&regrant, "regrant", false, "Explicitly reactivate an expired grant or Funnel TTL; revoked people stay revoked")
	c.Flags().BoolVar(&ackNever, "ack-never", false, "Acknowledge permanent tailnet-member access; never refused for public/guest")
	return c
}

func init() { rootCmd.AddCommand(newExtendCmd()) }
