package cmd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const (
	shareStatusReady       = "ready"
	maxShareNameCandidates = 100
)

type ShareResult struct {
	URL     string `json:"url,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status"`
	AuthURL string `json:"auth_url,omitempty"`
	// FunnelExpiresAt is the deadline of the Funnel share this call returned.
	// A reused share keeps its own, which can be sooner than the one asked
	// for, so it is reported rather than implied by the request.
	FunnelExpiresAt *time.Time `json:"funnel_expires_at,omitempty"`
	// FunnelRearmed is true when this call re-armed the expired Funnel
	// deadline of the share it reused.
	FunnelRearmed bool `json:"funnel_rearmed,omitempty"`
	// Exposure is the same view add returns. With FunnelExpiresAt it tells
	// tailnet-only (kind tailnet or tailnet_allow) from public until a
	// deadline (public_funnel with funnel_expires_at) from public with no
	// deadline (public_funnel without it).
	Exposure    inspect.ExposureView `json:"exposure"`
	serviceName string
}

type shareTargetSpec struct {
	Service  registry.Service
	NameBase string
}

// shareRequest is the complete one-shot share intent. Target, Name and
// Ephemeral are what `tslink share` itself accepts; the exposure fields below
// carry the same meaning as the corresponding `tslink add` flags and exist so
// an MCP client can create a share with an allow-list instead of one readable
// by every tailnet member. Every exposure field is enforced by the domain
// layer: the service this request builds goes through registry.AddIfMissing,
// which runs registry.ValidateService and therefore
// registry.ValidateFunnelGuardrails.
type shareRequest struct {
	Target          string
	Name            string
	Ephemeral       bool
	Allow           []string
	Tags            []string
	Funnel          bool
	PublicAck       bool
	FunnelTTL       string
	FunnelTTLSet    bool
	NoDaemonInstall bool
}

type sharePaths struct {
	Registry    string
	Ownership   string
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
	shareOwnershipPathFn       = config.NodeOwnershipPath
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
	ownershipPath, err := shareOwnershipPathFn()
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
	return sharePaths{Registry: regPath, Ownership: ownershipPath, PID: pidPath, Snapshot: snapshotPath, AuthHandoff: authPath}, nil
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
		absolute, err = filepath.EvalSymlinks(absolute)
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
			// Path is the parent directory because that is the only thing a
			// file open can be anchored at, and File is what keeps the share
			// to the one file the user named. The served file name lives in
			// the registry rather than only in this spec: the daemon reads the
			// registry, so a narrowing recorded anywhere else is a narrowing
			// the daemon never applies.
			return shareTargetSpec{
				Service: registry.Service{
					Type:      registry.TypeFile,
					Path:      filepath.Dir(absolute),
					File:      filepath.Base(absolute),
					Ephemeral: ephemeral,
				},
				NameBase: filepath.Base(absolute),
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
		host = strings.ToLower(host)
	}
	if strings.HasPrefix(portText, "+") || strings.HasPrefix(portText, "-") {
		return shareTargetSpec{}, output.ErrUsage("share target must be an existing path, a port from 1 to 65535, or host:port")
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

// applyShareExposure folds a share request's exposure fields onto the inferred
// target spec. It reuses the exact `tslink add` helpers (parseAllowedUsers,
// parseTags, resolveFunnelExpiry, funnelOptionRequiresFunnel), so allow-list
// normalization, tag validation and Funnel TTL semantics cannot drift between
// the two surfaces. It deliberately does not re-check the Funnel guardrails:
// registry.ValidateService is the enforcement point and runs on the write.
func applyShareExposure(spec shareTargetSpec, req shareRequest) (shareTargetSpec, error) {
	if err := funnelOptionRequiresFunnel("public_ack", "funnel", req.PublicAck, req.Funnel); err != nil {
		return shareTargetSpec{}, err
	}
	if err := funnelOptionRequiresFunnel("funnel_ttl", "funnel", req.FunnelTTLSet, req.Funnel); err != nil {
		return shareTargetSpec{}, err
	}
	allowedUsers, err := parseAllowedUsers(strings.Join(req.Allow, ","))
	if err != nil {
		return shareTargetSpec{}, err
	}
	var tags []string
	if len(req.Tags) > 0 {
		tags, err = parseTags(strings.Join(req.Tags, ","))
		if err != nil {
			return shareTargetSpec{}, err
		}
	}
	funnelExpiresAt, err := resolveFunnelExpiry(req.Funnel, req.FunnelTTL, req.FunnelTTLSet, time.Time{})
	if err != nil {
		return shareTargetSpec{}, err
	}
	spec.Service.AllowedUsers = allowedUsers
	spec.Service.Tags = tags
	spec.Service.Funnel = req.Funnel
	spec.Service.PublicAck = req.PublicAck
	spec.Service.FunnelExpiresAt = funnelExpiresAt
	return spec, nil
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
		name = fallbackShareName(value)
	}
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

func fallbackShareName(value string) string {
	for _, r := range value {
		if r > 127 {
			sum := sha256.Sum256([]byte(value))
			return fmt.Sprintf("share-%x", sum[:4])
		}
	}
	return "share"
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

// sameShareBackend decides whether an existing service already serves this
// share's target, so a repeated share reuses it instead of creating a suffixed
// node. File is part of the comparison: two regular files in one directory
// share a Path, and without File `tslink share ./b.html` would reuse the
// service registered for ./a.html and then return a URL for /b.html that that
// service answers 404 for.
func sameShareBackend(existing, candidate registry.Service) bool {
	return existing.Type == candidate.Type &&
		existing.Path == candidate.Path &&
		existing.File == candidate.File &&
		existing.Target == candidate.Target &&
		existing.Ephemeral == candidate.Ephemeral
}

func sameShareTarget(existing, candidate registry.Service) bool {
	return sameShareBackend(existing, candidate) &&
		existing.Funnel == candidate.Funnel &&
		existing.PublicAck == candidate.PublicAck &&
		slices.Equal(existing.AllowedUsers, candidate.AllowedUsers) &&
		sameShareTags(existing.Tags, candidate.Tags) &&
		existing.Domain == candidate.Domain
}

func sameShareTags(a, b []string) bool {
	first := make(map[string]struct{}, len(a))
	second := make(map[string]struct{}, len(b))
	for _, tag := range a {
		first[tag] = struct{}{}
	}
	for _, tag := range b {
		second[tag] = struct{}{}
	}
	if len(first) != len(second) {
		return false
	}
	for tag := range first {
		if _, ok := second[tag]; !ok {
			return false
		}
	}
	return true
}

func shareFunnelDeadlineCompatible(existing registry.Service, spec shareTargetSpec) bool {
	if !existing.Funnel {
		return true
	}
	requested := spec.Service.FunnelExpiresAt
	current := existing.FunnelExpiresAt
	if requested == nil {
		return current == nil
	}
	return current != nil && current.After(time.Now()) && !current.After(*requested)
}

func shareFunnelDeadlineDescription(deadline *time.Time) string {
	if deadline == nil {
		return "never"
	}
	return deadline.UTC().Format(time.RFC3339Nano)
}

func shareExposurePosture(svc registry.Service) string {
	return fmt.Sprintf("funnel=%t, funnel_deadline=%s, public_ack=%t, allowed_users=%d, tags=%v, domain=%q",
		svc.Funnel, shareFunnelDeadlineDescription(svc.FunnelExpiresAt), svc.PublicAck, len(svc.AllowedUsers), svc.Tags, svc.Domain)
}

// shareRequestedExposure undoes the daemon's Funnel expiry for the reuse
// match. Once a deadline passes, reconcile turns the service tailnet-only but
// keeps its deadline and public_ack, so the record an identical Funnel share
// created no longer says funnel. Read as the Funnel share it was, it matches
// the retry, whose expired deadline is then re-armed rather than refused as a
// different posture.
//
// Only a Funnel request reads it that way, and only a record that kept
// public_ack. `tslink add` without --funnel also leaves an expired deadline on
// a tailnet-only record, but clears public_ack: that record is the posture a
// plain share asks for, and a share does not turn its Funnel back on.
func shareRequestedExposure(existing registry.Service, spec shareTargetSpec, now time.Time) registry.Service {
	if spec.Service.Funnel && !existing.Funnel && existing.PublicAck && existing.FunnelExpiresAt != nil && !existing.FunnelExpiresAt.After(now) {
		existing.Funnel = true
	}
	return existing
}

// shareFunnelRearmable reports whether a deadline the reuse check refused is
// one a retry re-arms, as `add` re-arms an expired preserved deadline: the
// existing deadline has passed and the request asks for a bounded Funnel. A
// request for a Funnel that never expires is a different posture and stays a
// conflict.
func shareFunnelRearmable(existing registry.Service, spec shareTargetSpec, now time.Time) bool {
	return existing.Funnel && existing.FunnelExpiresAt != nil && !existing.FunnelExpiresAt.After(now) &&
		spec.Service.FunnelExpiresAt != nil
}

// rearmShareFunnel gives existing the requested Funnel deadline, turning
// Funnel back on if the daemon had downgraded it, provided the entry is still
// exactly what was read. ok is false when it changed or disappeared meanwhile,
// and the caller reads the registry again.
func rearmShareFunnel(regPath string, existing registry.Service, deadline time.Time) (rearmed registry.Service, ok bool, err error) {
	errChanged := errors.New("share changed since it was read")
	rearmed, err = registry.MutateService(regPath, existing.Name, func(stored registry.Service) (registry.Service, error) {
		if !reflect.DeepEqual(stored, existing) {
			return registry.Service{}, errChanged
		}
		ok = true
		stored.Funnel = true
		stored.FunnelExpiresAt = &deadline
		return stored, nil
	})
	if !ok {
		return registry.Service{}, false, nil
	}
	return rearmed, true, err
}

// shareRegistration is registerShareWithOutcome's result.
type shareRegistration struct {
	Service       registry.Service
	Created       bool
	FunnelRearmed bool
}

func registerShare(regPath string, spec shareTargetSpec, requestedName string) (registry.Service, bool, error) {
	registration, err := registerShareWithOutcome(regPath, spec, requestedName)
	return registration.Service, registration.Created, err
}

func registerShareWithOutcome(regPath string, spec shareTargetSpec, requestedName string) (shareRegistration, error) {
	// Tags take part in reuse only when the caller asked for them. `tslink
	// share` has no way to, so matching its request against the default tag
	// would turn a retry into a conflict as soon as the default tag, or the
	// share's own tags, changed. The default fills in for a new service only.
	tagsRequested := len(spec.Service.Tags) > 0
	base := sanitizeShareName(spec.NameBase)
	if requestedName != "" {
		if err := registry.ValidateName(requestedName); err != nil {
			return shareRegistration{}, err
		}
		base = requestedName
	}
retries:
	for retry := 0; retry < maxShareNameCandidates; retry++ {
		reg, err := registry.Load(regPath)
		if err != nil {
			return shareRegistration{}, err
		}
		now := time.Now()
		usedNames := make(map[string]struct{}, len(reg.Services))
		for _, existing := range reg.Services {
			candidate := spec.Service
			if !tagsRequested {
				candidate.Tags = existing.Tags
			}
			current := shareRequestedExposure(existing, spec, now)
			if sameShareTarget(current, candidate) {
				if requestedName != "" && existing.Name != requestedName {
					return shareRegistration{}, output.ErrConflict(fmt.Sprintf(
						"cannot apply requested name %q: target is already shared as %q; re-run without an explicit name to reuse it, or remove the existing service before retrying with the requested name",
						requestedName, existing.Name))
				}
				if !shareFunnelDeadlineCompatible(current, spec) {
					if !shareFunnelRearmable(current, spec, now) {
						return shareRegistration{}, output.ErrConflict(fmt.Sprintf(
							"cannot reuse service %q for this funnel deadline: existing deadline is %s, requested deadline is %s; remove or reconfigure the existing service before retrying",
							existing.Name, shareFunnelDeadlineDescription(existing.FunnelExpiresAt), shareFunnelDeadlineDescription(spec.Service.FunnelExpiresAt)))
					}
					rearmed, ok, err := rearmShareFunnel(regPath, existing, *spec.Service.FunnelExpiresAt)
					if err != nil {
						return shareRegistration{}, err
					}
					if !ok {
						continue retries
					}
					return shareRegistration{Service: rearmed, FunnelRearmed: true}, nil
				}
				return shareRegistration{Service: existing}, nil
			}
			if sameShareBackend(existing, spec.Service) {
				return shareRegistration{}, output.ErrConflict(fmt.Sprintf(
					"cannot reuse service %q for this share target: its exposure posture is %s, but this share requires %s; remove or reconfigure the existing service, or share a different target",
					existing.Name, shareExposurePosture(existing), shareExposurePosture(spec.Service)))
			}
			usedNames[existing.Name] = struct{}{}
		}

		name := ""
		for attempt := 1; attempt <= maxShareNameCandidates; attempt++ {
			candidate := suffixedShareName(base, attempt)
			if _, exists := usedNames[candidate]; !exists {
				name = candidate
				break
			}
		}
		if name == "" {
			return shareRegistration{}, fmt.Errorf("could not allocate share name %q after %d candidates", base, maxShareNameCandidates)
		}

		svc := spec.Service
		if !tagsRequested {
			svc.Tags = []string{config.GetDefaultTag()}
		}
		svc.Name = name
		svc.CreatedAt = time.Now().UTC()
		created, err := shareAddIfMissingFn(regPath, svc)
		if err != nil {
			return shareRegistration{}, err
		}
		if created {
			return shareRegistration{Service: svc, Created: true}, nil
		}
	}
	return shareRegistration{}, fmt.Errorf("could not register share %q after concurrent registry updates", base)
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

// shareEnrollmentPendingCode reports whether a service endpoint resolution
// failure is one of the enrollment-pending signals that share turns into a
// successful needs_login result. resolveServiceEndpointOnce returns
// enrollment_required when a valid but global auth-handoff exists, and
// url_not_ready when the service is simply not up yet.
func shareEnrollmentPendingCode(code string) bool {
	return code == registry.CodeURLNotReady || code == registry.CodeEnrollmentRequired
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
	if code, ok := registry.ErrorCode(err); !ok || !shareEnrollmentPendingCode(code) {
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

func startShareDaemon(ctx context.Context, errOut io.Writer) (shareDaemonStart, error) {
	if err := ensureDaemonFn(ctx, errOut, false); err != nil {
		return shareDaemonStart{}, err
	}
	return shareDaemonStart{}, nil
}

func executeShare(ctx context.Context, paths sharePaths, req shareRequest, wait time.Duration, errOut io.Writer) (result ShareResult, err error) {
	spec, err := inferShareTarget(req.Target, req.Ephemeral)
	if err != nil {
		return ShareResult{}, err
	}
	spec, err = applyShareExposure(spec, req)
	if err != nil {
		return ShareResult{}, err
	}
	if req.NoDaemonInstall && !shareIsRunningFn(paths.PID) {
		return ShareResult{}, daemonNotRunningError()
	}
	registration, err := registerShareWithOutcome(paths.Registry, spec, req.Name)
	if err != nil {
		return ShareResult{}, err
	}
	svc, created := registration.Service, registration.Created
	defer func() {
		if err == nil || !created {
			return
		}
		if _, rollbackErr := registry.RemoveIfUnchanged(paths.Registry, svc); rollbackErr != nil {
			err = errors.Join(err, fmt.Errorf("roll back share %q: %w", svc.Name, rollbackErr))
		}
	}()
	if !shareIsRunningFn(paths.PID) {
		startup, err := shareStartDaemonFn(ctx, errOut)
		if err != nil {
			return ShareResult{}, err
		}
		if startup.Status == authStatusNeedsLogin && startup.AuthURL != "" {
			return withShareFunnelState(ShareResult{Status: authStatusNeedsLogin, AuthURL: startup.AuthURL, serviceName: svc.Name}, registration), nil
		}
	}
	// svc, not spec: when registerShare reused an existing service, the URL has
	// to describe what that service actually serves.
	result, err = waitForShareOutcome(ctx, paths, svc.Name, svc.File, wait)
	if err != nil {
		return result, err
	}
	return withShareFunnelState(result, registration), nil
}

// withShareFunnelState reports the exposure and Funnel deadline of the
// service the share actually uses, and whether this call re-armed it.
func withShareFunnelState(result ShareResult, registration shareRegistration) ShareResult {
	if registration.Service.Funnel {
		result.FunnelExpiresAt = cloneTimePointer(registration.Service.FunnelExpiresAt)
	}
	result.FunnelRearmed = registration.FunnelRearmed
	result.Exposure = inspect.ServiceViewFor(registry.EffectiveServiceAt(registration.Service, time.Now())).Exposure
	return result
}

func init() {
	shareCmd := &cobra.Command{
		Use:   "share <path|port|host:port>",
		Short: "Share a local path or web port and print its tailnet URL",
		Long: `Register a one-shot file or proxy service, start the daemon if needed,
and wait for an exact tailnet URL. An existing directory becomes a file service
serving that whole directory, browsable. An existing regular file serves only
that one file: its URL is the file, the service root redirects to it, and every
other path is 404. Ports and host:port targets become HTTP proxies. Shares are
ephemeral by default.

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
			noDaemonInstall, _ := cmd.Flags().GetBool("no-daemon-install")
			result, err := executeShare(cmd.Context(), paths, shareRequest{
				Target:          args[0],
				Name:            name,
				Ephemeral:       ephemeral,
				NoDaemonInstall: noDaemonInstall,
			}, wait, cmd.ErrOrStderr())
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
	shareCmd.Flags().String("name", "", "Requested service name (DNS label); a matching target must already use it, while unrelated name collisions receive a numeric suffix")
	shareCmd.Flags().Bool("ephemeral", true, "Use an ephemeral tailnet node (set --ephemeral=false for durable state)")
	shareCmd.Flags().Bool("no-daemon-install", false, "Require an already running background service; do not install one")
	shareCmd.Flags().Duration("wait", defaultURLWait, "Wait for an exact runtime URL (share waits 30s by default; unlike url, no flag is required)")
	shareCmd.Flags().Lookup("wait").NoOptDefVal = defaultURLWait.String()
	rootCmd.AddCommand(shareCmd)
}
