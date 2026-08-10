package daemon

import (
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/filelock"
)

const (
	processIdentityVersion = 1
	processProductID       = "github.com/monody0007/tslink"

	// Legacy PID files contain only a PID. Their mtime is the only durable
	// launch-time evidence available during the upgrade to identity sidecars.
	// Foreground startup can perform network preflight before writing the PID,
	// so keep this deliberately wider than the normal sub-second delta.
	legacyPIDStartTolerance = 2 * time.Minute
)

type processIdentityRecord struct {
	Version       int    `json:"version"`
	Product       string `json:"product"`
	PID           int    `json:"pid"`
	StartUnixNano int64  `json:"start_unix_nano"`
}

var readExecutableBuildInfo = buildinfo.ReadFile

var (
	readProcessIdentityData = os.ReadFile
	errIdentityMismatch     = errors.New("process does not match the TSLink serve daemon")
)

type processLiveness uint8

const (
	processLivenessUnknown processLiveness = iota
	processLivenessAbsent
	processLivenessAlive
)

// daemonServeArgs builds the child argv for the re-executed foreground serve
// process. controlURL and manageACL opt-ins observed by the parent must be
// forwarded to the child exactly once, otherwise the documented
// `serve --daemon --manage-acl` opt-in is silently dropped in daemon mode. It
// is shared by the Unix and Windows Daemonize implementations so both platforms
// forward identical flags.
func daemonServeArgs(controlURL string, manageACL bool) []string {
	args := []string{"serve"}
	if controlURL != "" {
		args = append(args, "--control-url", controlURL)
	}
	if manageACL {
		args = append(args, "--manage-acl")
	}
	return args
}

// WritePID writes the current process PID and a process-instance identity
// sidecar. The PID file remains numeric for compatibility with older clients.
func WritePID(path string) error {
	pid := os.Getpid()
	started, err := processStartTime(pid)
	if err != nil {
		return fmt.Errorf("inspect current process start time: %w", err)
	}
	record := processIdentityRecord{
		Version:       processIdentityVersion,
		Product:       processProductID,
		PID:           pid,
		StartUnixNano: started.UnixNano(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode process identity: %w", err)
	}
	data = append(data, '\n')
	identityPath := processIdentityPath(path)
	if err := writePrivateFileAtomic(identityPath, data); err != nil {
		return fmt.Errorf("write process identity: %w", err)
	}
	if err := WritePIDForProcess(path, pid); err != nil {
		_ = os.Remove(identityPath)
		return err
	}
	return nil
}

// WritePIDForProcess writes a numeric PID file. It is also used for the
// short-lived ready signal, so process identity is intentionally written only
// by WritePID at the daemon's canonical PID path.
func WritePIDForProcess(path string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	return writePrivateFileAtomic(path, []byte(strconv.Itoa(pid)+"\n"))
}

func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// WithPIDLock holds the PID-file lock while fn runs.
func WithPIDLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := filelock.Lock(lockFile); err != nil {
		return err
	}
	defer filelock.Unlock(lockFile)

	return fn()
}

// ReadPID reads and parses a PID file.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse PID in %s: %w", path, err)
	}

	return pid, nil
}

// RemovePID removes the PID file on a best-effort basis.
func RemovePID(path string) {
	_ = os.Remove(path)
	_ = os.Remove(processIdentityPath(path))
}

// IsProcessAbsentFromPIDFile reports true only when a readable positive PID is
// conclusively absent. Callers must preserve PID artifacts for unknown or
// permission-denied liveness results.
func IsProcessAbsentFromPIDFile(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}
	return inspectProcessLiveness(pid) == processLivenessAbsent
}

func processIdentityPath(pidPath string) string {
	return pidPath + ".identity"
}

// verifyProcessIdentity treats the sidecar as a consistency mechanism, not an
// authentication boundary. A same-user process can rewrite both PID artifacts.
// A missing, damaged, future-version, or stale sidecar therefore falls through
// to the legacy evidence bridge instead of making a live daemon invisible.
func verifyProcessIdentity(pidPath string, pid int) error {
	var sidecarErr error
	record, err := readProcessIdentity(pidPath)
	if err == nil {
		sidecarErr = verifyRecordedProcessIdentity(pid, record)
		if sidecarErr == nil {
			return nil
		}
	} else {
		sidecarErr = fmt.Errorf("read process identity: %w", err)
	}

	legacyErr := verifyLegacyProcessIdentity(pidPath, pid)
	if legacyErr == nil {
		return nil
	}
	if errors.Is(legacyErr, errIdentityMismatch) {
		return legacyErr
	}
	return fmt.Errorf("sidecar unavailable or stale (%v); legacy identity evidence unavailable: %w", sidecarErr, legacyErr)
}

func verifyLegacyProcessIdentity(pidPath string, pid int) error {
	// Compatibility bridge: any daemon without a usable sidecar is checked from
	// its running binary, serve argv, and the PID-file/start-time window. This is
	// intentionally available for as long as mixed-version installations exist;
	// it is not limited to one generation and is weaker than a valid sidecar.
	if err := verifyProcessProduct(pid); err != nil {
		return err
	}
	started, err := processStartTime(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d start time: %w", pid, err)
	}
	info, err := os.Stat(pidPath)
	if err != nil {
		return fmt.Errorf("stat legacy PID file: %w", err)
	}
	if delta := absoluteDuration(info.ModTime().Sub(started)); delta > legacyPIDStartTolerance {
		return identityMismatchf("legacy PID file timestamp differs from process %d start by %s", pid, delta)
	}
	return nil
}

func readProcessIdentity(pidPath string) (processIdentityRecord, error) {
	data, err := readProcessIdentityData(processIdentityPath(pidPath))
	if err != nil {
		return processIdentityRecord{}, err
	}
	var record processIdentityRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return processIdentityRecord{}, err
	}
	return record, nil
}

func verifyRecordedProcessIdentity(pid int, record processIdentityRecord) error {
	if record.Version != processIdentityVersion {
		return identityMismatchf("unsupported process identity version %d", record.Version)
	}
	if record.Product != processProductID {
		return identityMismatchf("process identity product is %q, not %q", record.Product, processProductID)
	}
	if record.PID != pid {
		return identityMismatchf("process identity PID is %d, not %d", record.PID, pid)
	}
	started, err := processStartTime(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d start time: %w", pid, err)
	}
	if started.UnixNano() != record.StartUnixNano {
		return identityMismatchf("process %d start time does not match PID identity", pid)
	}
	return verifyProcessProduct(pid)
}

func verifyProcessProduct(pid int) error {
	actual, err := processExecutable(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d executable: %w", pid, err)
	}
	actual = strings.TrimSpace(strings.TrimSuffix(actual, " (deleted)"))
	info, buildErr := readExecutableBuildInfo(actual)
	if buildErr == nil {
		if info.Main.Path != processProductID {
			return identityMismatchf("process %d executable %q belongs to Go module %q, not %q", pid, actual, info.Main.Path, processProductID)
		}
		return verifyProcessServeCommand(pid)
	}

	// A running executable can outlive its unlinked Homebrew Cellar file. Only
	// that absent-on-disk case may use the weaker basename+argv fallback.
	if _, statErr := os.Stat(actual); errors.Is(statErr, os.ErrNotExist) {
		return legacyProcessProductFallback(pid, actual)
	} else if statErr != nil {
		return fmt.Errorf("stat process %d executable %q: %w", pid, actual, statErr)
	}
	if isDefinitiveNonGoExecutable(buildErr) {
		return identityMismatchf("process %d executable %q is not a TSLink Go executable: %v", pid, actual, buildErr)
	}
	return fmt.Errorf("process %d executable %q has unavailable TSLink build metadata: %w", pid, actual, buildErr)
}

func verifyProcessServeCommand(pid int) error {
	args, err := processArguments(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d arguments: %w", pid, err)
	}
	if len(args) < 2 || args[1] != "serve" {
		return identityMismatchf("process %d argv %q does not identify a serve daemon", pid, args)
	}
	return nil
}

func legacyProcessProductFallback(pid int, executablePath string) error {
	wantBase := processExecutableBaseName()
	if filepath.Base(executablePath) != wantBase {
		return identityMismatchf("process %d unlinked executable basename %q is not %s", pid, filepath.Base(executablePath), wantBase)
	}
	return verifyProcessServeCommand(pid)
}

func processExecutableBaseName() string {
	if runtime.GOOS == "windows" {
		return "tslink.exe"
	}
	return "tslink"
}

func isDefinitiveNonGoExecutable(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not a Go executable")
}

func identityMismatchf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errIdentityMismatch, fmt.Sprintf(format, args...))
}

func identityVerifiedOrUnavailable(err error) bool {
	return err == nil || !errors.Is(err, errIdentityMismatch)
}

func absoluteDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
