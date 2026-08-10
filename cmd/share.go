package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const shareStatusReady = "ready"

type ShareResult struct {
	URL         string `json:"url,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`
	AuthURL     string `json:"auth_url,omitempty"`
	serviceName string
}

type shareTargetSpec struct {
	Service  registry.Service
	NameBase string
	FileName string
}

type sharePaths struct {
	Registry    string
	PID         string
	Snapshot    string
	AuthHandoff string
}

type shareDaemonStart struct {
	Status  string `json:"status,omitempty"`
	AuthURL string `json:"auth_url,omitempty"`
}

var (
	shareEnsureDirFn           = config.EnsureDir
	shareRegistryPathFn        = config.RegistryPath
	sharePIDPathFn             = config.PIDPath
	shareSnapshotPathFn        = config.RuntimeSnapshotPath
	shareAuthHandoffPathFn     = config.AuthHandoffPath
	shareIsRunningFn           = daemon.IsRunning
	shareStartDaemonFn         = startShareDaemon
	shareResolveEndpointOnceFn = resolveServiceEndpointOnce
	sharePollableStatusFn      = getPollableStatus
	shareAddIfMissingFn        = registry.AddIfMissing
)

func resolveSharePaths() (sharePaths, error) {
	regPath, err := shareRegistryPathFn()
	if err != nil {
		return sharePaths{}, err
	}
	pidPath, err := sharePIDPathFn()
	if err != nil {
		return sharePaths{}, err
	}
	snapshotPath, err := shareSnapshotPathFn()
	if err != nil {
		return sharePaths{}, err
	}
	authPath, err := shareAuthHandoffPathFn()
	if err != nil {
		return sharePaths{}, err
	}
	return sharePaths{Registry: regPath, PID: pidPath, Snapshot: snapshotPath, AuthHandoff: authPath}, nil
}

func inferShareTarget(target string, ephemeral bool) (shareTargetSpec, error) {
	if target == "" {
		return shareTargetSpec{}, output.ErrUsage("share target is required")
	}
	if info, err := os.Stat(target); err == nil {
		absolute, err := filepath.Abs(target)
		if err != nil {
			return shareTargetSpec{}, err
		}
		absolute = filepath.Clean(absolute)
		switch {
		case info.IsDir():
			return shareTargetSpec{
				Service:  registry.Service{Type: registry.TypeFile, Path: absolute, Ephemeral: ephemeral},
				NameBase: filepath.Base(absolute),
			}, nil
		case info.Mode().IsRegular():
			return shareTargetSpec{
				Service:  registry.Service{Type: registry.TypeFile, Path: filepath.Dir(absolute), Ephemeral: ephemeral},
				NameBase: filepath.Base(absolute),
				FileName: filepath.Base(absolute),
			}, nil
		default:
			return shareTargetSpec{}, output.ErrUsage(fmt.Sprintf("share target %q is not a directory or regular file", target))
		}
	} else if !os.IsNotExist(err) {
		return shareTargetSpec{}, err
	}

	portText := target
	host := "localhost"
	if strings.Contains(target, ":") {
		var err error
		host, portText, err = net.SplitHostPort(target)
		if err != nil || strings.TrimSpace(host) == "" {
			return shareTargetSpec{}, output.ErrUsage("proxy share target must be a port or host:port")
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return shareTargetSpec{}, output.ErrUsage("share target must be an existing path, a port from 1 to 65535, or host:port")
	}
	return shareTargetSpec{
		Service: registry.Service{
			Type:      registry.TypeProxy,
			Target:    "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
			Ephemeral: ephemeral,
		},
		NameBase: "port-" + strconv.Itoa(port),
	}, nil
}

func sanitizeShareName(value string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			lastHyphen = false
		} else if !lastHyphen && b.Len() > 0 {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "share"
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func suffixedShareName(base string, attempt int) string {
	if attempt <= 1 {
		return base
	}
	suffix := "-" + strconv.Itoa(attempt)
	limit := 63 - len(suffix)
	trimmed := strings.TrimRight(base[:min(len(base), limit)], "-")
	if trimmed == "" {
		trimmed = "share"
	}
	return trimmed + suffix
}

func registerShare(regPath string, spec shareTargetSpec, requestedName string) (registry.Service, error) {
	base := sanitizeShareName(spec.NameBase)
	if requestedName != "" {
		if err := registry.ValidateName(requestedName); err != nil {
			return registry.Service{}, err
		}
		base = requestedName
	}
	for attempt := 1; ; attempt++ {
		svc := spec.Service
		svc.Name = suffixedShareName(base, attempt)
		if len(svc.Tags) == 0 {
			svc.Tags = []string{config.GetDefaultTag()}
		}
		created, err := shareAddIfMissingFn(regPath, svc)
		if err != nil {
			return registry.Service{}, err
		}
		if created {
			return svc, nil
		}
	}
}

func directFileURL(base, fileName string) (string, error) {
	if fileName == "" {
		return base, nil
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + fileName
	return parsed.String(), nil
}

func shareOutcomeOnce(paths sharePaths, name, fileName string) (ShareResult, bool, error) {
	resolution, err := shareResolveEndpointOnceFn(paths.PID, paths.Registry, paths.Snapshot, name)
	if err == nil {
		endpoint, err := directFileURL(resolution.Result.URL, fileName)
		if err != nil {
			return ShareResult{}, false, err
		}
		return ShareResult{URL: endpoint, Name: name, Status: shareStatusReady}, true, nil
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
		return ShareResult{}, false, err
	}
	status, err := sharePollableStatusFn(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil {
		return ShareResult{}, false, err
	}
	if status.AuthStatus == authStatusNeedsLogin && status.AuthURL != "" {
		return ShareResult{Status: authStatusNeedsLogin, AuthURL: status.AuthURL, serviceName: name}, true, nil
	}
	return ShareResult{}, false, nil
}

func waitForShareOutcome(ctx context.Context, paths sharePaths, name, fileName string, wait time.Duration) (ShareResult, error) {
	result, done, err := shareOutcomeOnce(paths, name, fileName)
	if err != nil || done {
		return result, err
	}
	if wait <= 0 {
		return ShareResult{}, registry.URLNotReadyError(name)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	ticker := time.NewTicker(urlPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ShareResult{}, ctx.Err()
		case <-timer.C:
			return ShareResult{}, registry.URLNotReadyError(name)
		case <-ticker.C:
			result, done, err = shareOutcomeOnce(paths, name, fileName)
			if err != nil || done {
				return result, err
			}
		}
	}
}

func parseShareDaemonResult(encoded []byte, runErr error) (shareDaemonStart, error) {
	var envelope struct {
		OK    bool                `json:"ok"`
		Code  int                 `json:"code"`
		Data  shareDaemonStart    `json:"data"`
		Error *output.ErrorObject `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(encoded), &envelope); err != nil {
		if runErr != nil {
			return shareDaemonStart{}, fmt.Errorf("start tslink daemon: %w", runErr)
		}
		return shareDaemonStart{}, fmt.Errorf("start tslink daemon: invalid JSON response: %w", err)
	}
	if !envelope.OK {
		if envelope.Error != nil {
			return shareDaemonStart{}, fmt.Errorf("start tslink daemon: %s", envelope.Error.Message)
		}
		return shareDaemonStart{}, fmt.Errorf("start tslink daemon failed with exit code %d", envelope.Code)
	}
	return envelope.Data, nil
}

func startShareDaemon(ctx context.Context, errOut io.Writer) (shareDaemonStart, error) {
	executable, err := os.Executable()
	if err != nil {
		return shareDaemonStart{}, err
	}
	command := exec.CommandContext(ctx, executable, "serve", "--daemon", "--json", "--no-browser")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = errOut
	runErr := command.Run()
	return parseShareDaemonResult(stdout.Bytes(), runErr)
}

func executeShare(ctx context.Context, paths sharePaths, target, requestedName string, ephemeral bool, wait time.Duration, errOut io.Writer) (ShareResult, error) {
	spec, err := inferShareTarget(target, ephemeral)
	if err != nil {
		return ShareResult{}, err
	}
	svc, err := registerShare(paths.Registry, spec, requestedName)
	if err != nil {
		return ShareResult{}, err
	}
	if !shareIsRunningFn(paths.PID) {
		startup, err := shareStartDaemonFn(ctx, errOut)
		if err != nil {
			return ShareResult{}, err
		}
		if startup.Status == authStatusNeedsLogin && startup.AuthURL != "" {
			return ShareResult{Status: authStatusNeedsLogin, AuthURL: startup.AuthURL, serviceName: svc.Name}, nil
		}
	}
	return waitForShareOutcome(ctx, paths, svc.Name, spec.FileName, wait)
}

func init() {
	shareCmd := &cobra.Command{
		Use:   "share <path|port|host:port>",
		Short: "Share a local path or web port and print its tailnet URL",
		Long: `Register a one-shot file or proxy service, start the daemon if needed,
and wait for an exact tailnet URL. Existing directories become file services;
regular files share their parent directory and return a URL for that file;
ports and host:port targets become HTTP proxies. Shares are ephemeral by default.

If Tailscale authorization is required, the authorization URL is returned as a
successful needs_login result. Open it and then use "tslink url <name> --wait".

Examples:
  tslink share ./build
  tslink share ./report.html
  tslink share 3000
  tslink share localhost:8080 --name preview
  tslink share ./build --ephemeral=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := shareEnsureDirFn(); err != nil {
				return err
			}
			paths, err := resolveSharePaths()
			if err != nil {
				return err
			}
			name, _ := cmd.Flags().GetString("name")
			ephemeral, _ := cmd.Flags().GetBool("ephemeral")
			wait, _ := cmd.Flags().GetDuration("wait")
			result, err := executeShare(cmd.Context(), paths, args[0], name, ephemeral, wait, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("share", result)
				return nil
			}
			if result.Status == authStatusNeedsLogin {
				fmt.Fprintf(cmd.ErrOrStderr(), "Tailscale authorization is required. Open this URL, then run: tslink url %s --wait\n", result.serviceName)
				fmt.Fprintln(cmd.OutOrStdout(), result.AuthURL)
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), result.URL)
			return nil
		},
	}
	shareCmd.Flags().String("name", "", "Service name override (DNS label; collisions receive a numeric suffix)")
	shareCmd.Flags().Bool("ephemeral", true, "Use an ephemeral tailnet node (set --ephemeral=false for durable state)")
	shareCmd.Flags().Duration("wait", defaultURLWait, "Wait for an exact runtime URL (optional value; default 30s)")
	shareCmd.Flags().Lookup("wait").NoOptDefVal = defaultURLWait.String()
	rootCmd.AddCommand(shareCmd)
}
