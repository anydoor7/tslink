package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

const (
	doctorStatusOK      = "ok"
	doctorStatusWarning = "warning"
	doctorStatusError   = "error"

	doctorSeverityInfo     = "info"
	doctorSeverityWarning  = "warning"
	doctorSeverityError    = "error"
	doctorSeverityCritical = "critical"

	doctorCredentialNone              = "none"
	doctorCredentialAPIToken          = "api_token"
	doctorCredentialOAuthClientSecret = "oauth_client_secret"
	doctorCredentialLegacyAuthKey     = "legacy_authkey"
	doctorCredentialMixed             = "mixed"

	doctorProbeTimeout = 250 * time.Millisecond

	doctorRedactedEvidenceValue = "[redacted]"
	doctorRedactedURL           = "[redacted-url]"
)

var (
	doctorConfigDirFn           = config.Dir
	doctorRegistryPathFn        = config.RegistryPath
	doctorRuntimeSnapshotPathFn = config.RuntimeSnapshotPath
	doctorPIDPathFn             = config.PIDPath
	doctorAuthKeyPathFn         = config.AuthKeyPath
	doctorLoadGlobalConfigFn    = config.LoadGlobalConfig
	doctorGetAPIKeyFn           = credentials.GetAPIKey
	doctorGetClientSecretFn     = credentials.GetClientSecret
	doctorReadFileFn            = os.ReadFile
	doctorStatFn                = os.Stat
	doctorOpenPathFn            = func(path string) (io.Closer, error) { return os.Open(path) }
	doctorProbeTargetFn         = defaultDoctorProbeTarget
)

var (
	doctorCredentialTokenPattern = regexp.MustCompile(`(?i)\btskey-[A-Za-z0-9._~+/=-]+`)
	doctorURLPattern             = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>)]+`)
	doctorUserInfoPattern        = regexp.MustCompile(`[A-Za-z0-9._%+-]+:[^@\s/]+@`)
)

type doctorOptions struct {
	ProbeExternal bool
}

type DoctorResult struct {
	SchemaVersion   string                      `json:"schema_version"`
	Status          string                      `json:"status"`
	Counts          DoctorCounts                `json:"counts"`
	Paths           DoctorPaths                 `json:"paths"`
	CredentialMode  string                      `json:"credential_mode"`
	Daemon          DoctorDaemon                `json:"daemon"`
	RuntimeSnapshot StatusRuntimeSnapshotResult `json:"runtime_snapshot"`
	Findings        []DoctorFinding             `json:"findings"`
}

type DoctorCounts struct {
	Services int `json:"services"`
	Findings int `json:"findings"`
	Info     int `json:"info"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
	Critical int `json:"critical"`
}

type DoctorPaths struct {
	ConfigDir       string `json:"config_dir,omitempty"`
	Registry        string `json:"registry,omitempty"`
	RuntimeSnapshot string `json:"runtime_snapshot,omitempty"`
	PID             string `json:"pid,omitempty"`
}

type DoctorDaemon struct {
	Running bool `json:"running"`
	PID     int  `json:"pid,omitempty"`
}

type DoctorFinding struct {
	Code     string            `json:"code"`
	Severity string            `json:"severity"`
	Area     string            `json:"area"`
	Message  string            `json:"message"`
	Service  string            `json:"service,omitempty"`
	Evidence map[string]string `json:"evidence,omitempty"`
}

type doctorTarget struct {
	Host         string
	Port         string
	ProbeAddress string
	External     bool
}

func runDoctor(out io.Writer, opts doctorOptions, isJSON bool) error {
	result := buildDoctorResult(opts)
	if isJSON {
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(result); err != nil {
			return err
		}
	} else {
		formatDoctor(result, out)
	}
	return doctorExit(result)
}

func buildDoctorResult(opts doctorOptions) DoctorResult {
	result := DoctorResult{
		SchemaVersion:  inspect.SchemaVersion,
		Status:         doctorStatusOK,
		CredentialMode: doctorCredentialNone,
		Findings:       []DoctorFinding{},
		RuntimeSnapshot: StatusRuntimeSnapshotResult{
			Status: "unknown",
		},
	}

	pathsOK := discoverDoctorPaths(&result)
	diagnoseCredentials(&result)

	var cfg config.GlobalConfig
	cfgOK := false
	if pathsOK {
		loaded, err := doctorLoadGlobalConfigFn()
		if err != nil {
			result.addFinding(inspect.WarningCodeConfigLoadFailed, "", "config", "Global config could not be loaded.", evidenceError(err))
		} else {
			cfg = loaded
			cfgOK = true
			if err := registry.ValidateControlURL(cfg.ControlURL); err != nil {
				result.addFinding(inspect.WarningCodeControlURLInvalid, "", "config", "Global control_url is invalid.", evidenceControlURLInvalid())
			}
		}
	}

	diagnoseDaemon(&result)

	var reg *registry.Registry
	var fingerprint string
	if result.Paths.Registry != "" {
		loaded, err := registry.Load(result.Paths.Registry)
		if err != nil {
			result.addFinding(inspect.WarningCodeRegistryLoadFailed, "", "registry", "Registry could not be loaded.", evidenceError(err))
		} else {
			reg = loaded
			result.Counts.Services = len(reg.Services)
			if fp, err := tsruntime.RegistryFingerprint(reg); err != nil {
				result.addFinding(inspect.WarningCodeRegistryLoadFailed, "", "registry", "Registry fingerprint could not be computed.", evidenceError(err))
			} else {
				fingerprint = fp
			}
		}
	}

	hasFunnel := false
	if reg != nil {
		for _, svc := range reg.Services {
			if svc.Funnel {
				hasFunnel = true
			}
			diagnoseService(&result, svc, opts)
		}
	}
	if cfgOK && cfg.ControlURL != "" && hasFunnel {
		result.addFinding(
			inspect.WarningCodeFunnelGlobalControlURLUnknownCompat,
			"",
			"exposure",
			"Global custom control_url is configured while Funnel services exist; TSLink cannot prove Funnel compatibility locally.",
			nil,
		)
	}

	if result.Paths.RuntimeSnapshot != "" && fingerprint != "" {
		diagnoseRuntimeSnapshot(&result, fingerprint)
	}

	result.finalize()
	return result
}

func discoverDoctorPaths(result *DoctorResult) bool {
	ok := true
	if dir, err := doctorConfigDirFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Config directory path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.ConfigDir = dir
	}
	if path, err := doctorRegistryPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Registry path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.Registry = path
	}
	if path, err := doctorRuntimeSnapshotPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "Runtime snapshot path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.RuntimeSnapshot = path
	}
	if path, err := doctorPIDPathFn(); err != nil {
		ok = false
		result.addFinding(inspect.WarningCodeConfigPathUnavailable, "", "config", "PID path could not be discovered.", evidenceError(err))
	} else {
		result.Paths.PID = path
	}
	return ok
}

func diagnoseCredentials(result *DoctorResult) {
	apiToken, apiErr := doctorGetAPIKeyFn()
	clientSecret, clientSecretErr := doctorGetClientSecretFn()
	legacyAuthKey, legacyErr := doctorLegacyAuthKeyConfigured()

	if apiErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "API token credential could not be inspected.", evidenceError(apiErr))
	}
	if clientSecretErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "OAuth client secret credential could not be inspected.", evidenceError(clientSecretErr))
	}
	if legacyErr != nil {
		result.addFinding(inspect.WarningCodeCredentialReadFailed, "", "credentials", "Legacy auth key credential could not be inspected.", evidenceError(legacyErr))
	}

	hasAPI := strings.TrimSpace(apiToken) != ""
	hasOAuth := strings.TrimSpace(clientSecret) != ""
	count := 0
	for _, ok := range []bool{hasAPI, hasOAuth, legacyAuthKey} {
		if ok {
			count++
		}
	}
	switch {
	case count == 0:
		result.CredentialMode = doctorCredentialNone
		if apiErr == nil && clientSecretErr == nil && legacyErr == nil {
			result.addFinding(inspect.WarningCodeCredentialNone, "", "credentials", "No TSLink credential is configured; run tslink login.", nil)
		}
	case count > 1:
		result.CredentialMode = doctorCredentialMixed
	case hasAPI:
		result.CredentialMode = doctorCredentialAPIToken
	case hasOAuth:
		result.CredentialMode = doctorCredentialOAuthClientSecret
	case legacyAuthKey:
		result.CredentialMode = doctorCredentialLegacyAuthKey
	}

	if legacyAuthKey {
		result.addFinding(inspect.WarningCodeCredentialLegacyAuthKey, "", "credentials", "Legacy reusable auth key is configured; prefer API token or OAuth client secret login.", nil)
	}
	if !hasAPI && (hasOAuth || legacyAuthKey) {
		result.addFinding(inspect.WarningCodeCredentialNoAPIClient, "", "credentials", "No API token is configured; remote Tailscale API permissions cannot be proven locally.", nil)
	}
}

func doctorLegacyAuthKeyConfigured() (bool, error) {
	path, err := doctorAuthKeyPathFn()
	if err != nil {
		return false, err
	}
	data, err := doctorReadFileFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.TrimSpace(string(data)) != "", nil
}

func diagnoseDaemon(result *DoctorResult) {
	if result.Paths.PID == "" {
		return
	}
	if !isRunningFn(result.Paths.PID) {
		result.addFinding(inspect.WarningCodeDaemonNotRunning, "", "daemon", "TSLink daemon is not running.", nil)
		return
	}
	result.Daemon.Running = true
	pid, err := readPIDFn(result.Paths.PID)
	if err != nil {
		result.addFinding(inspect.WarningCodeDaemonPIDUnreadable, "", "daemon", "Daemon appears to be running, but the PID file could not be read.", evidenceError(err))
		return
	}
	result.Daemon.PID = pid
}

func diagnoseRuntimeSnapshot(result *DoctorResult, fingerprint string) {
	snapshot, loadErr := runtimeLoadSnapshotFn(result.Paths.RuntimeSnapshot)
	expected := tsruntime.ExpectedRuntime{
		CurrentRegistryFingerprint: fingerprint,
	}
	if result.Daemon.Running {
		expected.DaemonPID = result.Daemon.PID
		if lowerBound, err := pidFileModTimeFn(result.Paths.PID); err == nil {
			expected.DaemonStartedAtLowerBound = lowerBound
		}
	}
	freshness := tsruntime.Classify(snapshot, loadErr, expected)
	result.RuntimeSnapshot = runtimeSnapshotResult(snapshot, freshness)
	if freshness.Code != "" {
		message := freshness.Message
		if message == "" {
			message = inspect.WarningCodeRegistry[freshness.Code].Description
		}
		result.addFinding(freshness.Code, "", "runtime_snapshot", message, nil)
	}
}

func diagnoseService(result *DoctorResult, svc registry.Service, opts doctorOptions) {
	view := inspect.ServiceViewFor(svc)
	for _, warning := range view.Warnings {
		result.addFinding(warning.Code, svc.Name, warning.Source, warning.Message, nil)
	}

	if err := registry.ValidateService(svc); err != nil {
		code := doctorRegistryValidationCode(svc, err)
		message := "Service failed registry validation."
		if code == inspect.WarningCodeControlURLInvalid {
			message = "Service control_url is invalid."
		} else if code == inspect.WarningCodeTCPAllowedUsersInvalid {
			message = "Raw TCP service cannot enforce allowed_users."
		} else if stableCode, ok := registry.ErrorCode(err); ok && stableCode != "" {
			if meta := inspect.WarningCodeRegistry[stableCode]; meta.Description != "" {
				message = meta.Description
			}
		}
		result.addFinding(code, svc.Name, "registry", message, nil)
	}

	if len(svc.AllowedUsers) > 0 {
		result.addFinding(inspect.WarningCodeIdentityResolutionUnknown, svc.Name, "identity", "Local doctor cannot prove real Tailscale identity resolution or remote ACL policy for this allow list.", map[string]string{
			"allow_count": strconv.Itoa(len(svc.AllowedUsers)),
		})
	}

	switch svc.Type {
	case registry.TypeProxy:
		diagnoseNetworkTarget(result, svc, opts, inspect.WarningCodeProxyNonLoopbackTarget)
	case registry.TypeTCP:
		diagnoseNetworkTarget(result, svc, opts, inspect.WarningCodeTCPNonLoopbackTarget)
	case registry.TypeFile:
		diagnoseFileTarget(result, svc)
	}
}

func doctorRegistryValidationCode(svc registry.Service, err error) string {
	if code, ok := registry.ErrorCode(err); ok && code != "" {
		return code
	}
	if svc.Type == registry.TypeTCP && len(svc.AllowedUsers) > 0 {
		return inspect.WarningCodeTCPAllowedUsersInvalid
	}
	if svc.ControlURL != "" {
		if controlErr := registry.ValidateControlURL(svc.ControlURL); controlErr != nil {
			return inspect.WarningCodeControlURLInvalid
		}
	}
	return inspect.WarningCodeRegistryServiceInvalid
}

func diagnoseNetworkTarget(result *DoctorResult, svc registry.Service, opts doctorOptions, nonLoopbackCode string) {
	target, err := classifyServiceTarget(svc)
	if err != nil {
		result.addFinding(inspect.WarningCodeTargetInvalid, svc.Name, "target", "Service target could not be parsed for probing.", evidenceError(err))
		return
	}
	if target.External {
		result.addFinding(nonLoopbackCode, svc.Name, "target", "Service target is not loopback/local; review whether TSLink should proxy to this host.", map[string]string{
			"host": target.Host,
		})
		if !opts.ProbeExternal {
			result.addFinding(inspect.WarningCodeTargetProbeSkippedExternal, svc.Name, "target_probe", "External target probe skipped; rerun with --probe-external to probe this target.", map[string]string{
				"host": target.Host,
			})
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), doctorProbeTimeout)
	defer cancel()
	if err := doctorProbeTargetFn(ctx, target.ProbeAddress, doctorProbeTimeout); err != nil {
		code := classifyProbeError(err)
		result.addFinding(code, svc.Name, "target_probe", inspect.WarningCodeRegistry[code].Description, nil)
	}
}

func classifyServiceTarget(svc registry.Service) (doctorTarget, error) {
	switch svc.Type {
	case registry.TypeProxy:
		return classifyProxyTarget(svc.Target)
	case registry.TypeTCP:
		return classifyHostPortTarget(svc.Target, "")
	default:
		return doctorTarget{}, fmt.Errorf("unsupported target type %q", svc.Type)
	}
}

func classifyProxyTarget(raw string) (doctorTarget, error) {
	if raw == "" {
		return doctorTarget{}, fmt.Errorf("empty proxy target")
	}
	if !strings.Contains(raw, "://") {
		return classifyHostPortTarget(raw, "80")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return doctorTarget{}, err
	}
	if parsed.Host == "" {
		return doctorTarget{}, fmt.Errorf("proxy target host is empty")
	}
	defaultPort := "80"
	if strings.EqualFold(parsed.Scheme, "https") {
		defaultPort = "443"
	}
	return classifyHostPortTarget(parsed.Host, defaultPort)
}

func classifyHostPortTarget(authority, defaultPort string) (doctorTarget, error) {
	host, port, err := net.SplitHostPort(authority)
	if err != nil {
		if defaultPort == "" {
			return doctorTarget{}, err
		}
		if authorityHasExplicitPort(authority) {
			return doctorTarget{}, err
		}
		host = strings.Trim(authority, "[]")
		port = defaultPort
	}
	if port == "" {
		return doctorTarget{}, fmt.Errorf("missing target port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort <= 0 || parsedPort > 65535 {
		return doctorTarget{}, fmt.Errorf("invalid target port %q", port)
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	external := !isLocalTargetHost(host)
	dialHost := host
	if dialHost == "" || isUnspecifiedIP(dialHost) {
		dialHost = "127.0.0.1"
	}
	return doctorTarget{
		Host:         host,
		Port:         port,
		ProbeAddress: net.JoinHostPort(dialHost, port),
		External:     external,
	}, nil
}

func authorityHasExplicitPort(authority string) bool {
	if strings.HasPrefix(authority, "[") {
		return strings.Contains(authority, "]:")
	}
	return strings.Count(authority, ":") == 1
}

func isLocalTargetHost(host string) bool {
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func isUnspecifiedIP(host string) bool {
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsUnspecified()
}

func defaultDoctorProbeTarget(ctx context.Context, address string, timeout time.Duration) error {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}

func classifyProbeError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return inspect.WarningCodeTargetProbeTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return inspect.WarningCodeTargetProbeTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return inspect.WarningCodeTargetProbeRefused
	}
	return inspect.WarningCodeTargetProbeFailed
}

func diagnoseFileTarget(result *DoctorResult, svc registry.Service) {
	path := svc.Path
	if path == "" {
		result.addFinding(inspect.WarningCodeFilePathMissing, svc.Name, "file", "File service path is empty.", nil)
		return
	}
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	info, err := doctorStatFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			result.addFinding(inspect.WarningCodeFilePathMissing, svc.Name, "file", "File service path does not exist.", nil)
			return
		}
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path cannot be inspected.", evidenceError(err))
		return
	}
	if !info.IsDir() {
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path is not a directory.", nil)
		return
	}
	handle, err := doctorOpenPathFn(path)
	if err != nil {
		result.addFinding(inspect.WarningCodeFilePathUnreadable, svc.Name, "file", "File service path is not readable.", evidenceError(err))
		return
	}
	_ = handle.Close()
}

func (r *DoctorResult) addFinding(code, service, area, message string, evidence map[string]string) {
	meta, ok := inspect.WarningCodeRegistry[code]
	if !ok {
		meta = inspect.WarningCodeMeta{Severity: doctorSeverityError, Source: area, Description: message}
	}
	if area == "" {
		area = meta.Source
	}
	if message == "" {
		message = meta.Description
	}
	finding := DoctorFinding{
		Code:     code,
		Severity: meta.Severity,
		Area:     area,
		Message:  message,
		Service:  service,
		Evidence: evidence,
	}
	for _, existing := range r.Findings {
		if existing.Code == finding.Code && existing.Service == finding.Service {
			return
		}
	}
	r.Findings = append(r.Findings, finding)
}

func (r *DoctorResult) finalize() {
	r.Counts.Findings = len(r.Findings)
	for _, finding := range r.Findings {
		switch finding.Severity {
		case doctorSeverityInfo:
			r.Counts.Info++
		case doctorSeverityWarning:
			r.Counts.Warnings++
		case doctorSeverityError:
			r.Counts.Errors++
		case doctorSeverityCritical:
			r.Counts.Critical++
		}
	}
	switch {
	case r.Counts.Errors > 0 || r.Counts.Critical > 0:
		r.Status = doctorStatusError
	case r.Counts.Warnings > 0:
		r.Status = doctorStatusWarning
	default:
		r.Status = doctorStatusOK
	}
}

func doctorExit(result DoctorResult) error {
	if result.Counts.Errors > 0 || result.Counts.Critical > 0 {
		return output.SilentExit(output.ExitCritical)
	}
	if result.Counts.Warnings > 0 {
		return output.SilentExit(output.ExitWarning)
	}
	return nil
}

func formatDoctor(result DoctorResult, out io.Writer) {
	fmt.Fprintf(
		out,
		"TSLink doctor: %s (%d errors, %d warnings, %d info)\n",
		result.Status,
		result.Counts.Errors+result.Counts.Critical,
		result.Counts.Warnings,
		result.Counts.Info,
	)
	fmt.Fprintf(out, "Config: %s\n", emptyDash(result.Paths.ConfigDir))
	fmt.Fprintf(out, "Registry: %s (%d services)\n", emptyDash(result.Paths.Registry), result.Counts.Services)
	fmt.Fprintf(out, "Credentials: %s\n", result.CredentialMode)
	if result.Daemon.Running {
		fmt.Fprintf(out, "Daemon: running (pid %d)\n", result.Daemon.PID)
	} else {
		fmt.Fprintln(out, "Daemon: not running")
	}
	fmt.Fprintf(out, "Runtime snapshot: %s", emptyDash(result.RuntimeSnapshot.Status))
	if result.RuntimeSnapshot.Code != "" {
		fmt.Fprintf(out, " (%s)", result.RuntimeSnapshot.Code)
	}
	fmt.Fprintln(out)
	if len(result.Findings) == 0 {
		fmt.Fprintln(out, "\nNo warnings or errors found.")
		return
	}

	fmt.Fprintln(out, "\nFindings:")
	for _, finding := range result.Findings {
		service := ""
		if finding.Service != "" {
			service = " service=" + finding.Service
		}
		fmt.Fprintf(out, "- %s [%s] %s%s: %s", strings.ToUpper(finding.Severity), finding.Code, finding.Area, service, finding.Message)
		if len(finding.Evidence) > 0 {
			fmt.Fprintf(out, " (%s)", formatEvidence(finding.Evidence))
		}
		fmt.Fprintln(out)
	}
}

func formatEvidence(evidence map[string]string) string {
	keys := make([]string, 0, len(evidence))
	for key := range evidence {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+evidence[key])
	}
	return strings.Join(parts, ", ")
}

func evidenceError(err error) map[string]string {
	if err == nil {
		return nil
	}
	return map[string]string{"error": sanitizeDoctorEvidenceValue(err.Error())}
}

func evidenceControlURLInvalid() map[string]string {
	return map[string]string{"error": "control_url failed validation"}
}

func sanitizeDoctorEvidenceValue(value string) string {
	value = doctorURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
		if err != nil {
			if strings.ContainsAny(raw, "?@") || doctorCredentialTokenPattern.MatchString(raw) {
				return doctorRedactedURL
			}
			return raw
		}
		if parsed.User != nil || parsed.RawQuery != "" || doctorCredentialTokenPattern.MatchString(raw) {
			return doctorRedactedURL
		}
		return raw
	})
	value = doctorCredentialTokenPattern.ReplaceAllString(value, doctorRedactedEvidenceValue)
	return doctorUserInfoPattern.ReplaceAllString(value, doctorRedactedEvidenceValue+"@")
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnose TSLink safety and runtime evidence",
	Long: `Diagnose local TSLink configuration, credentials, daemon liveness,
runtime snapshot freshness, service guardrails, and reachable local targets.

Doctor is read-only. It does not mutate the registry, Tailscale policy, or
service state.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		probeExternal, err := cmd.Flags().GetBool("probe-external")
		if err != nil {
			return err
		}
		return runDoctor(cmd.OutOrStdout(), doctorOptions{ProbeExternal: probeExternal}, jsonOutput(cmd))
	},
}

func init() {
	doctorCmd.Flags().Bool("probe-external", false, "Probe non-loopback service targets")
	rootCmd.AddCommand(doctorCmd)
}
