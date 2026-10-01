package testenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
)

// RootEnv names the temporary root Main created for the running test binary.
// Every home, config, data and cache location of the process points inside it.
//
// A test binary that starts with RootEnv naming a root whose marker a running
// test binary holds locked was started by a test in an already isolated binary
// (a helper re-exec such as `os.Args[0] -test.run=^TestX$`). Its TSLINK_
// variables were put there by that test, not by the contributor's shell, so
// Main keeps them; it still moves every location into a fresh root of its own.
const RootEnv = "TSLINK_TESTENV_ROOT"

// RootPrefix is the name prefix of each root Main creates under os.TempDir().
const RootPrefix = "tslink-testenv-"

// IsolationFailure starts the message Main prints when it refuses to run a
// test binary it could not isolate.
const IsolationFailure = "testenv: cannot isolate this test binary"

// DoctorSkipTailscaleSSHEnv is the product knob that makes `tslink doctor`
// skip its read of the local tailscaled (cmd/doctor.go). Main sets it so every
// compiled tslink child a test runs inherits it; in-process tests fake the
// doctor seams instead, so the knob changes nothing inside the test binary.
const DoctorSkipTailscaleSSHEnv = "TSLINK_DOCTOR_SKIP_TAILSCALE_SSH"

// rootMarker is the file that proves a root was made by Main and that the
// test binary owning it is still running. The owner creates it, locks it
// (filelock) for as long as it runs, and only then writes its pid into it. The
// OS drops the lock when the owner exits, however it exits, so:
//
//   - a contributor who exports RootEnv, or a leftover root, cannot switch off
//     the TSLINK_ scrub: only a root whose marker is locked is inherited;
//   - a root whose marker is unlocked although it names an owner was left by
//     a binary that was interrupted, timed out or crashed, and the next Main
//     removes it (reclaimStaleRoots).
const rootMarker = "tslink-testenv-root"

// harnessReportEnvs are the test harness's own diagnostic switches. They only
// add summary lines to the guards' stderr, never change what a test sees, and
// an external runner may enable one for diagnostics; see
// report_env_independence_test.go. They survive the TSLINK_ scrub.
var harnessReportEnvs = []string{ServiceManagerGuardReportEnv, NetworkGuardReportEnv}

// goToolchainLocationEnvs are the Go toolchain's own locations. Their
// defaults derive from HOME, USERPROFILE, APPDATA and LOCALAPPDATA, which Main
// moves, so Main resolves and exports them first. Tests that run the go
// command (building the tslink binary, go list) then keep using the
// contributor's build cache, module cache and go env file instead of starting
// cold and downloading modules.
var goToolchainLocationEnvs = []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}

// Main is the TestMain entry point of every test binary in this module. It
// isolates the process before any test runs, calls run (m.Run when nil), and
// returns the exit code for os.Exit:
//
//   - every TSLINK_ variable inherited from the contributor's environment is
//     removed, so an exported credential or knob can neither be consumed by a
//     test nor change a result; a test that needs one sets it with t.Setenv;
//   - HOME, USERPROFILE, APPDATA, LOCALAPPDATA, XDG_CONFIG_HOME,
//     XDG_DATA_HOME, XDG_CACHE_HOME, XDG_STATE_HOME and TSLINK_CONFIG_DIR
//     point into one fresh temporary root, so os.UserHomeDir,
//     os.UserConfigDir, os.UserCacheDir and config.Dir resolve there, in this
//     process and in every child it starts;
//   - DoctorSkipTailscaleSSHEnv is set for compiled tslink children;
//   - a call to a host seam's test-binary default (UnfakedHostSeam) turns the
//     package red with the call's stack;
//   - the root is removed after run returns, and roots that interrupted,
//     timed-out or crashed binaries left in os.TempDir() are removed before
//     the new one is made.
//
// Everything that may still reach a real host resource is an explicit opt-in
// inside a test: t.Setenv, a fake that delegates to the real seam, or
// RealHostMain for a whole binary.
func Main(m *testing.M, run func() int) int {
	if run == nil {
		run = m.Run
	}
	root, marker, err := isolateProcess()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", IsolationFailure, err)
		return 2
	}
	mainRoot = root
	code := reportUnfakedHostSeams(run())
	// marker stays open, and its lock held, until here.
	if err := removeRoot(root, marker); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: remove isolation root %s: %v\n", root, err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// RealHostMain runs a test binary without Main's isolation. It exists for the
// one gated test that must reach real host state (the Linux systemd E2E, which
// cmd's TestMain admits only with TSLINK_SYSTEMD_E2E=1 and one exact -test.run
// selector on a disposable VM). reason is printed so the run's log records the
// opt-in. Unfaked host seams are still reported.
func RealHostMain(m *testing.M, reason string, run func() int) int {
	if run == nil {
		run = m.Run
	}
	if strings.TrimSpace(reason) == "" {
		fmt.Fprintln(os.Stderr, "testenv: RealHostMain requires a reason")
		return 2
	}
	fmt.Fprintf(os.Stderr, "testenv: running without host isolation: %s\n", reason)
	return reportUnfakedHostSeams(run())
}

// isolateProcess applies Main's environment and returns the root it created
// with its marker, open and locked.
func isolateProcess() (string, *os.File, error) {
	child := inheritedRoot() != ""
	if !child {
		// Resolve the toolchain's locations while HOME and the platform
		// directories still name the contributor's own.
		if err := pinGoToolchainLocations(); err != nil {
			return "", nil, err
		}
		for _, name := range tslinkEnvNames() {
			if isHarnessReportEnv(name) {
				continue
			}
			if err := os.Unsetenv(name); err != nil {
				return "", nil, fmt.Errorf("unset %s: %w", name, err)
			}
		}
	}

	reclaimStaleRoots(os.TempDir())
	root, marker, err := createRoot(os.TempDir())
	if err != nil {
		return "", nil, err
	}
	fail := func(err error) (string, *os.File, error) {
		_ = removeRoot(root, marker)
		return "", nil, err
	}
	// Only the home exists. Like SetHome, the TSLink config dir inside it is
	// left for the code under test to create; pre-creating it would, on
	// Windows, look like a legacy %USERPROFILE%\.config\tslink to the config
	// migration of any test that clears TSLINK_CONFIG_DIR.
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		return fail(err)
	}
	for _, kv := range HomeEnv(home) {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			return fail(err)
		}
	}
	if err := os.Setenv(DoctorSkipTailscaleSSHEnv, "1"); err != nil {
		return fail(err)
	}
	if err := os.Setenv(RootEnv, root); err != nil {
		return fail(err)
	}
	return root, marker, nil
}

// mainRoot is the root Main created in this process, whose marker this
// process holds locked until Main removes it.
var mainRoot string

// markerCreatedHook, when a test sets it, runs between createRoot creating a
// marker and locking it.
var markerCreatedHook func(root string)

// markerProbedHook, when a test sets it, runs at the start of every
// rootMarkerState with the root whose marker it is about to open.
var markerProbedHook func(root string)

// createRoot makes a fresh root under dir and returns it with its marker open
// and locked. The marker records this process only once the lock is held, so
// a reclaimStaleRoots that opens it in between finds it empty and leaves the
// root alone.
func createRoot(dir string) (string, *os.File, error) {
	root, err := os.MkdirTemp(dir, RootPrefix)
	if err != nil {
		return "", nil, err
	}
	marker, err := os.OpenFile(filepath.Join(root, rootMarker), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(root)
		return "", nil, err
	}
	if markerCreatedHook != nil {
		markerCreatedHook(root)
	}
	if err := filelock.Lock(marker); err != nil {
		_ = marker.Close()
		_ = os.RemoveAll(root)
		return "", nil, fmt.Errorf("lock %s: %w", marker.Name(), err)
	}
	if _, err := fmt.Fprintf(marker, "%d\n", os.Getpid()); err != nil {
		_ = removeRoot(root, marker)
		return "", nil, fmt.Errorf("record the owner in %s: %w", marker.Name(), err)
	}
	return root, marker, nil
}

// removeRoot deletes root, whose marker this process holds open and locked.
// Everything else goes while the lock still keeps reclaimStaleRoots out. Then
// the marker is unlocked and closed, which Windows needs before it can delete
// it, and the marker and root follow; a reclaimStaleRoots in another binary
// may remove those two first, which is fine.
func removeRoot(root string, marker *os.File) error {
	var firstErr error
	keep := func(err error) {
		if err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	entries, err := os.ReadDir(root)
	keep(err)
	for _, entry := range entries {
		if entry.Name() != rootMarker {
			keep(os.RemoveAll(filepath.Join(root, entry.Name())))
		}
	}
	keep(filelock.Unlock(marker))
	keep(marker.Close())
	keep(removeBriefly(filepath.Join(root, rootMarker)))
	keep(removeBriefly(root))
	return firstErr
}

// removeBriefly removes path, retrying for up to a second: another binary's
// rootMarkerState may have the marker open for a moment, and while it does
// Windows can delete neither the marker nor its directory.
func removeBriefly(path string) error {
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// rootOwnedByThisUser is the sweep's owner check. Tests replace it to report
// a root as another user's.
var rootOwnedByThisUser = ownedByThisUser

// reclaimStaleRoots removes the roots under dir that test binaries left when
// they were interrupted, killed by -timeout or crashed: entries named
// RootPrefix* that are directories, not symlinks, owned by this user, whose
// marker is abandoned (see rootMarkerState). Nothing else is touched: another
// user's entry, an entry without a marker, a symlink, a root whose owner still
// holds its lock, and a root whose marker is still empty. Two binaries that
// reclaim the same root at once both just remove it.
func reclaimStaleRoots(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), RootPrefix) {
			continue
		}
		root := filepath.Join(dir, entry.Name())
		// On a TMPDIR that users share, such as /tmp, another user's tree is
		// not this binary's to remove, however it looks. The owner is checked
		// before any marker is opened: nobody else can create files in a root
		// this user made (0700), so its marker cannot be swapped for a FIFO,
		// whose open would block.
		if info, err := os.Lstat(root); err != nil || !info.IsDir() || !rootOwnedByThisUser(info) {
			continue
		}
		if rootMarkerState(root) == markerAbandoned {
			_ = os.RemoveAll(root)
		}
	}
}

// markerState is what a root's marker says about the binary that owns it.
type markerState int

const (
	// markerMissing: no regular marker file, or one that cannot be locked.
	markerMissing markerState = iota
	// markerHeld: a running test binary holds the lock.
	markerHeld
	// markerEmpty: unlocked and empty. Its owner has not locked it yet, or
	// the root was made before roots were locked; either way it is left alone.
	markerEmpty
	// markerAbandoned: unlocked although an owner recorded itself in it, so
	// that owner has exited without removing its root.
	markerAbandoned
)

// rootMarkerState opens root's marker, without following a symlink in its
// place, and tries its lock. When the lock is free this call holds it just
// long enough to read the marker's size.
//
// root must not be this process's own root (see Root). The marker is opened
// for writing, although nothing is written: where Linux emulates flock with
// byte-range locks (NFS), an exclusive lock needs a descriptor open for
// writing, and a read-only one is refused as if the marker could not be
// locked. Main creates markers 0600, so another user's marker cannot be
// opened here and counts as missing, as it did when it was opened read-only.
func rootMarkerState(root string) markerState {
	if markerProbedHook != nil {
		markerProbedHook(root)
	}
	path := filepath.Join(root, rootMarker)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return markerMissing
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return markerMissing
	}
	defer f.Close()
	locked, err := filelock.TryLock(f)
	if err != nil {
		return markerMissing
	}
	if !locked {
		return markerHeld
	}
	defer func() { _ = filelock.Unlock(f) }()
	// The size is read under the lock: an owner writes its record while it
	// holds the lock, so it is either complete or absent here.
	held, err := f.Stat()
	if err != nil || !os.SameFile(info, held) {
		return markerMissing
	}
	if held.Size() == 0 {
		return markerEmpty
	}
	return markerAbandoned
}

// HomeEnv returns the variable assignments that make home the only home of a
// process: the conventional home variables of every platform, the platform
// config/data/cache/state directories derived from them, and TSLink's config
// override. Main and SetHome both apply exactly this set.
func HomeEnv(home string) [][2]string {
	return [][2]string{
		{"HOME", home},
		{"USERPROFILE", home},
		{"APPDATA", filepath.Join(home, "AppData", "Roaming")},
		{"LOCALAPPDATA", filepath.Join(home, "AppData", "Local")},
		{"XDG_CONFIG_HOME", filepath.Join(home, ".config")},
		{"XDG_DATA_HOME", filepath.Join(home, ".local", "share")},
		{"XDG_CACHE_HOME", filepath.Join(home, ".cache")},
		{"XDG_STATE_HOME", filepath.Join(home, ".local", "state")},
		{configDirEnv, ConfigDir(home)},
	}
}

// Root returns the root Main created for this test binary, or "" when the
// binary is not running under Main. Before Main has run, a non-empty Root
// means the binary was started by a test of an isolated binary: helper modes
// that a TestMain dispatches before calling Main check it, so a variable a
// contributor exported cannot turn a top-level run into a helper.
//
// Once Main has made this binary's root, Root reports it without opening its
// marker. This process holds that marker's lock, and only local flock
// promises that a second open file of it is a separate lock holder. Where
// Linux emulates flock with byte-range locks (NFS, SMB/CIFS), such a probe
// can be refused, and Root would report "", or be granted to the process
// that already holds the lock, and its unlock and close would then drop the
// lock while the root is in use, so another binary's reclaimStaleRoots could
// remove it.
func Root() string {
	if mainRoot != "" {
		return mainRoot
	}
	return inheritedRoot()
}

// inheritedRoot returns RootEnv's root if a running test binary holds its
// marker locked: the parent that started this binary. It is called only
// before this binary has a root of its own. A root whose owner is gone is not
// honoured.
func inheritedRoot() string {
	root := os.Getenv(RootEnv)
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	if rootMarkerState(root) != markerHeld {
		return ""
	}
	return root
}

func tslinkEnvNames() []string {
	var names []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TSLINK_") {
			names = append(names, name)
		}
	}
	return names
}

func isHarnessReportEnv(name string) bool {
	for _, allowed := range harnessReportEnvs {
		if name == allowed {
			return true
		}
	}
	return false
}

// pinGoToolchainLocations exports the go command's resolved GOCACHE,
// GOMODCACHE, GOPATH and GOENV. `go env` is the only resolver that honours
// the environment, the go env file and the platform defaults in the same
// order the go command itself does. Without a go command on PATH nothing in
// this binary can run go either, so there is nothing to pin.
func pinGoToolchainLocations() error {
	missing := false
	for _, name := range goToolchainLocationEnvs {
		if os.Getenv(name) == "" {
			missing = true
		}
	}
	if !missing {
		return nil
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil
	}
	out, err := exec.Command(goBin, append([]string{"env", "-json"}, goToolchainLocationEnvs...)...).Output()
	if err != nil {
		return fmt.Errorf("resolve Go toolchain locations with %s env: %w", goBin, err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(out, &values); err != nil {
		return fmt.Errorf("parse %s env -json: %w", goBin, err)
	}
	for _, name := range goToolchainLocationEnvs {
		if os.Getenv(name) != "" || values[name] == "" {
			continue
		}
		if err := os.Setenv(name, values[name]); err != nil {
			return err
		}
	}
	return nil
}

// ErrUnfakedHostSeam is returned by a host seam's test-binary default. A
// production seam that reaches a real host resource (a tsnet node, the local
// tailscaled, a browser) is replaced in its package's TestMain by a default
// that returns this error instead; a test that exercises the seam installs
// its own fake.
var ErrUnfakedHostSeam = errors.New("a test reached a production seam that touches a real host resource without faking it")

type unfakedHostSeamCall struct {
	seam  string
	stack []byte
}

var unfakedHostSeams struct {
	sync.Mutex
	calls []unfakedHostSeamCall
}

// UnfakedHostSeam records that the named seam ran its test-binary default and
// returns an error that names it. Main prints every recorded call with its
// stack after the tests finish and fails the package, so a forgotten fake is
// loud even when the product code turns the error into a soft outcome.
func UnfakedHostSeam(seam string) error {
	unfakedHostSeams.Lock()
	unfakedHostSeams.calls = append(unfakedHostSeams.calls, unfakedHostSeamCall{seam: seam, stack: debug.Stack()})
	unfakedHostSeams.Unlock()
	return fmt.Errorf("%w: %s (fake it in the test)", ErrUnfakedHostSeam, seam)
}

func reportUnfakedHostSeams(code int) int {
	unfakedHostSeams.Lock()
	calls := append([]unfakedHostSeamCall(nil), unfakedHostSeams.calls...)
	unfakedHostSeams.Unlock()
	if len(calls) == 0 {
		return code
	}
	fmt.Fprintf(os.Stderr, "testenv: %d call(s) reached the TestMain default of a host seam; the test must install its own fake:\n", len(calls))
	for i, call := range calls {
		fmt.Fprintf(os.Stderr, "testenv: unfaked host seam %d: %s\n%s", i+1, call.seam, call.stack)
	}
	return 1
}
