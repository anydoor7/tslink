package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/duration"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const (
	defaultURLWait  = 30 * time.Second
	urlPollInterval = 100 * time.Millisecond
)

var (
	urlPIDPathFn             = config.PIDPath
	urlRegistryPathFn        = config.RegistryPath
	urlRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
)

// URLResult is the minimal exact endpoint payload for agent consumers.
type URLResult struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	State string `json:"state"`
}

type serviceURLResolution struct {
	Result   URLResult
	Endpoint inspect.EndpointView
}

func exactServiceURL(result StatusURLsResult, name string) (serviceURLResolution, bool, bool) {
	for _, svc := range result.Services {
		if svc.Name != name {
			continue
		}
		ready := svc.Endpoint.State == inspect.EndpointStateExact &&
			svc.Endpoint.Display != "" &&
			!strings.Contains(svc.Endpoint.Display, "<tailnet>")
		if ready {
			return serviceURLResolution{
				Result:   URLResult{Name: name, URL: svc.Endpoint.Display, State: inspect.EndpointStateExact},
				Endpoint: svc.Endpoint,
			}, true, true
		}
		return serviceURLResolution{Result: URLResult{Name: name}, Endpoint: svc.Endpoint}, false, true
	}
	return serviceURLResolution{Result: URLResult{Name: name}}, false, false
}

func resolveServiceEndpointOnce(pidPath, regPath, snapshotPath, name string) (serviceURLResolution, error) {
	if err := registry.ValidateName(name); err != nil {
		return serviceURLResolution{}, err
	}
	status, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		return serviceURLResolution{}, err
	}
	result, ready, found := exactServiceURL(status, name)
	if !found {
		return serviceURLResolution{}, output.ErrNotFound(fmt.Sprintf("service not found: %s", name))
	}
	if !ready {
		if handoff, ok := validAuthHandoffForService(pidPath, name); ok {
			return result, registry.CodedError{Code: registry.CodeEnrollmentRequired, Message: "Authorize TSLink before waiting for a service URL", Next: []string{"Open " + handoff.AuthURL, "tslink status --json"}}
		}
		return result, registry.URLNotReadyError(name)
	}
	return result, nil
}

func resolveServiceEndpoint(ctx context.Context, pidPath, regPath, snapshotPath, name string, wait time.Duration) (serviceURLResolution, error) {
	result, err := resolveServiceEndpointOnce(pidPath, regPath, snapshotPath, name)
	if err == nil || wait <= 0 {
		return result, err
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
		return serviceURLResolution{}, err
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(urlPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return serviceURLResolution{}, ctx.Err()
		case <-timer.C:
			return serviceURLResolution{}, registry.URLNotReadyError(name)
		case <-ticker.C:
			result, err = resolveServiceEndpointOnce(pidPath, regPath, snapshotPath, name)
			if err == nil {
				return result, nil
			}
			if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
				return serviceURLResolution{}, err
			}
		}
	}
}

func resolveServiceURL(ctx context.Context, pidPath, regPath, snapshotPath, name string, wait time.Duration) (URLResult, error) {
	resolution, err := resolveServiceEndpoint(ctx, pidPath, regPath, snapshotPath, name, wait)
	return resolution.Result, err
}

func init() {
	urlCmd := &cobra.Command{
		Use:   "url <name>",
		Short: "Print one service's exact runtime URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := cmd.Flags().GetBool("raw")
			if err != nil {
				return err
			}
			if raw && jsonOutput(cmd) {
				return output.ErrUsage("--raw conflicts with --json")
			}
			wait, err := cmd.Flags().GetDuration("wait")
			if err != nil {
				return err
			}
			pidPath, err := urlPIDPathFn()
			if err != nil {
				return err
			}
			regPath, err := urlRegistryPathFn()
			if err != nil {
				return err
			}
			snapshotPath, err := urlRuntimeSnapshotPathFn()
			if err != nil {
				return err
			}
			if !isRunningFn(pidPath) {
				return daemonNotRunningError()
			}
			result, err := resolveServiceURL(cmd.Context(), pidPath, regPath, snapshotPath, args[0], wait)
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("url", result)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), result.URL)
			return nil
		},
	}
	urlCmd.Flags().Var(duration.NewValue(0), "wait", "Wait for an exact runtime URL only when supplied (bare --wait means 30s)")
	urlCmd.Flags().Lookup("wait").NoOptDefVal = defaultURLWait.String()
	urlCmd.Flags().Bool("raw", false, "Print only the URL and one trailing newline")
	rootCmd.AddCommand(urlCmd)
}
