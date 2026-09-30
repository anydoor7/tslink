package daemon

import (
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// daemonLogFileMode is the mode the daemon's own stdout and stderr logs are
// created with, and narrowed to when they already exist.
//
// Owner-only rather than the 0644 these used to get, because of what is in
// them: the access log names every principal that reached a service, invite
// flows log recipient email addresses, and tsnet's authorization URL is a
// bearer link to the tailnet. A log directory created 0700 does not make the
// files inside it safe on its own -- a mode is the thing that survives the
// directory being opened up, a backup being restored, or the files being copied
// somewhere else.
//
// It lives in the shared file, and the Windows Daemonize uses it too, even
// though Windows ignores the permission argument to OpenFile: a second literal
// there would read as a deliberate difference in how sensitive these files are,
// which is not what it would mean.
const daemonLogFileMode os.FileMode = 0o600

type processIdentityRecord struct {
	Version       int    `json:"version"`
	Product       string `json:"product"`
	PID           int    `json:"pid"`
	StartUnixNano int64  `json:"start_unix_nano"`
	// BuildVersion and Executable are additive (omitempty on the wire) and
	// exist only for 'tslink doctor' to report a build-skew warning when a
	// long-running daemon predates the binary a later CLI invocation is
	// running. Neither field is ever read by verifyProcessIdentity or any
	// other identity-verification decision: a sidecar written before these
	// fields existed, or one whose own build could not be determined when it
	// was written, is exactly as valid an identity record as one that
	// populates them. See ReadProcessBuildIdentity.
	BuildVersion string `json:"build_version,omitempty"`
	Executable   string `json:"executable,omitempty"`
}

var readExecutableBuildInfo = buildinfo.ReadFile

var (
	readProcessIdentityData = os.ReadFile
	errIdentityMismatch     = errors.New("process does not match the TSLink serve daemon")
	// errForeignProcess is the subset of errIdentityMismatch that carries
	// positive proof: the PID belongs to some other program. It wraps
	// errIdentityMismatch so every existing mismatch check keeps working, while
	// callers that must choose between "stopped" and "cannot tell" can ask for
	// the stronger fact. Sidecar version, recorded-field and timestamp
	// disagreements deliberately stay outside it: those say the evidence is
	// stale or from another build, not that the process is somebody else's.
	errForeignProcess = fmt.Errorf("%w: the PID belongs to a different program", errIdentityMismatch)
)

type processLiveness uint8

const (
	processLivenessUnknown processLiveness = iota
	processLivenessAbsent
	processLivenessAlive
)

// daemonServeArgs builds the child argv for the re-executed foreground serve
// process. controlURL, manageACL, and noAutoProvision choices observed by the
// parent must be forwarded to the child exactly once, otherwise a documented
// serve choice is silently dropped in daemon mode. It
// is shared by the Unix and Windows Daemonize implementations so both platforms
// forward identical flags.
func daemonServeArgs(controlURL string, manageACL, noAutoProvision, mcp bool) []string {
	args := []string{"serve"}
	if controlURL != "" {
		args = append(args, "--control-url", controlURL)
	}
	if manageACL {
		args = append(args, "--manage-acl")
	}
	if noAutoProvision {
		args = append(args, "--no-auto-provision")
	}
	if mcp {
		args = append(args, "--mcp")
	}
	return args
}

// WritePID writes the current process PID and a process-instance identity
// sidecar, with no build-identity report at all: neither BuildVersion nor
// Executable is set, so both stay omitted from the wire (see
// processIdentityRecord). This keeps WritePID's on-disk output exactly what
// it was before build-identity reporting existed, for its remaining callers
// (tests, and any future caller with no build-identity evidence to report).
// The PID file remains numeric for compatibility with older clients.
func WritePID(path string) error {
	pid := os.Getpid()
	started, err := processStartTime(pid)
	if err != nil {
		return fmt.Errorf("inspect current process start time: %w", err)
	}
	return writeProcessIdentityAndPID(path, processIdentityRecord{
		Version:       processIdentityVersion,
		Product:       processProductID,
		PID:           pid,
		StartUnixNano: started.UnixNano(),
	})
}

// WritePIDWithBuildIdentity writes the current process PID and a
// process-instance identity sidecar carrying a reporting-only build
// identity, so a later 'tslink doctor' invocation can compare what actually
// started against what CLI it is running now. buildVersion is supplied by
// the caller: only cmd knows the linked release version (see
// selfBuildIdentity in cmd). The running executable path is resolved here
// the same way verifyProcessProduct resolves a peer process's executable,
// best-effort — a resolution failure just leaves the field empty, because
// this is reporting-only and must never block daemon startup (see the
// BuildVersion/Executable comment on processIdentityRecord).
func WritePIDWithBuildIdentity(path, buildVersion string) error {
	pid := os.Getpid()
	started, err := processStartTime(pid)
	if err != nil {
		return fmt.Errorf("inspect current process start time: %w", err)
	}
	selfExecutable, _ := executable()
	return writeProcessIdentityAndPID(path, processIdentityRecord{
		Version:       processIdentityVersion,
		Product:       processProductID,
		PID:           pid,
		StartUnixNano: started.UnixNano(),
		BuildVersion:  buildVersion,
		Executable:    selfExecutable,
	})
}

func writeProcessIdentityAndPID(path string, record processIdentityRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode process identity: %w", err)
	}
	data = append(data, '\n')
	identityPath := processIdentityPath(path)
	if err := writePrivateFileAtomic(identityPath, data); err != nil {
		return fmt.Errorf("write process identity: %w", err)
	}
	if err := WritePIDForProcess(path, record.PID); err != nil {
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

// IsPIDFileMissing reports whether no PID file exists at path: no daemon was
// started from this config directory, or it exited and removed its file.
// Status reports that as absent rather than unknown. It says nothing about a
// PID file that exists but cannot be read, and it is not a reason to delete
// other PID artifacts (IsProcessAbsentFromPIDFile decides that).
func IsPIDFileMissing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
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

// ProcessBuildIdentity is the reporting-only subset of the daemon's identity
// sidecar exposed for 'tslink doctor' build-skew comparisons. Both fields are
// zero-valued when the sidecar is missing, unreadable, malformed, or simply
// predates build-identity reporting: callers must render that as "unknown,"
// never as a build identity equal to any other zero value.
type ProcessBuildIdentity struct {
	BuildVersion string
	Executable   string
}

// ReadProcessBuildIdentity reads the build-identity subset of the process
// identity sidecar at pidPath, for daemon/CLI build-skew reporting. It never
// participates in IsRunning, IsProcessRunning, or any liveness/identity
// verification decision (see verifyProcessIdentity): a missing, unreadable,
// or malformed sidecar simply yields a zero-value ProcessBuildIdentity, not
// an error a caller should act on. The sidecar's process-identity version and
// product fields are deliberately not checked here either, for the same
// reason — this is a best-effort report, not a gate.
func ReadProcessBuildIdentity(pidPath string) ProcessBuildIdentity {
	record, err := readProcessIdentity(pidPath)
	if err != nil {
		return ProcessBuildIdentity{}
	}
	return ProcessBuildIdentity{BuildVersion: record.BuildVersion, Executable: record.Executable}
}

// verifyRecordedProcessIdentity checks the sidecar's consistency fields only:
// Version, Product, PID, StartUnixNano, then verifyProcessProduct against the
// live process. Keep BuildVersion and Executable out of this function — they
// are reporting fields (see ReadProcessBuildIdentity), never gates.
//
// Beyond the sidecar-is-not-an-authentication-boundary reason on
// verifyProcessIdentity, there is a testing reason this rule cannot be
// relaxed here: a gate added in this function is masked by
// verifyLegacyProcessIdentity's fallback, so the common paths keep returning
// the same answer and the whole suite still passes. A regression introduced
// at this spot is invisible — it fails green. When a build or executable
// check genuinely has to exist, put it in IsRunning, where the fallback does
// not cover for it and the existing tests do catch it.
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
			return foreignProcessf("process %d executable %q belongs to Go module %q, not %q", pid, actual, info.Main.Path, processProductID)
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
		return foreignProcessf("process %d executable %q is not a TSLink Go executable: %v", pid, actual, buildErr)
	}
	return fmt.Errorf("process %d executable %q has unavailable TSLink build metadata: %w", pid, actual, buildErr)
}

func verifyProcessServeCommand(pid int) error {
	args, err := processArguments(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d arguments: %w", pid, err)
	}
	if len(args) < 2 || args[1] != "serve" {
		return foreignProcessf("process %d argv %q does not identify a serve daemon", pid, args)
	}
	return nil
}

func legacyProcessProductFallback(pid int, executablePath string) error {
	wantBase := processExecutableBaseName()
	if filepath.Base(executablePath) != wantBase {
		return foreignProcessf("process %d unlinked executable basename %q is not %s", pid, filepath.Base(executablePath), wantBase)
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

// foreignProcessf records a mismatch that was observed on the live process
// itself, rather than on the artifacts describing it.
func foreignProcessf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errForeignProcess, fmt.Sprintf(format, args...))
}

// IsForeignProcessFromPIDFile reports true only when the PID file names a live
// process that is provably a different program. A process that merely cannot
// be identified returns false, so the conservative "identity unverified"
// treatment stays the default and only positive proof moves a caller off it.
func IsForeignProcessFromPIDFile(path string) bool {
	pid, err := ReadPID(path)
	if err != nil || pid <= 0 {
		return false
	}
	if inspectProcessLiveness(pid) != processLivenessAlive {
		return false
	}
	return errors.Is(verifyProcessProduct(pid), errForeignProcess)
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
