package testenv

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
)

const heldRootReportPrefix = "testenv-held-root: "

// rootOwner is a child run of this test binary that sits inside its own Main,
// holding its root, until it is killed.
type rootOwner struct {
	cmd     *exec.Cmd
	root    string
	drained chan struct{}
}

// startRootOwner starts testName as a child with isolationProbeEnv=hold-root
// and returns once the child has reported its root.
func startRootOwner(t *testing.T, testName string) *rootOwner {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.count=1")
	cmd.Env = append(os.Environ(), isolationProbeEnv+"=hold-root")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start root owner: %v", err)
	}
	owner := &rootOwner{cmd: cmd, drained: make(chan struct{})}
	reported := make(chan string, 1)
	go func() {
		defer close(owner.drained)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			// Not TrimSpace: an owner outside Main reports an empty root, and
			// the prefix's trailing space must survive for that to be seen.
			if root, ok := strings.CutPrefix(scanner.Text(), heldRootReportPrefix); ok {
				select {
				case reported <- root:
				default:
				}
			}
		}
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		owner.kill()
	})
	select {
	case owner.root = <-reported:
	case <-owner.drained:
		t.Fatalf("root owner exited without reporting its root (stderr %q)", stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatalf("root owner did not report its root within 30s (stderr %q)", stderr.String())
	}
	if owner.root == "" {
		t.Fatalf("root owner reported no root: it is not running under Main (stderr %q)", stderr.String())
	}
	return owner
}

// kill ends the owner the way a -timeout or a crash does: Main never returns,
// so it cannot remove its root.
func (o *rootOwner) kill() {
	_ = o.cmd.Process.Kill()
	<-o.drained
	_ = o.cmd.Wait()
}

// eventually polls cond for up to 10s. The OS drops a dead process's file
// locks as it tears the process down; this allows for that to lag.
func eventually(cond func() bool) bool {
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// plantRoot makes dir/name the way a binary that died after locking and
// recording itself leaves it: a root with a home and a marker holding record,
// with nothing holding the marker's lock.
func plantRoot(t *testing.T, dir, name, record string) string {
	t.Helper()
	root := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(root, "home", ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rootMarker), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func requireExists(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s is gone (%v): %s", path, err, why)
	}
}

func requireGone(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (Lstat error %v): %s", path, err, why)
	}
}

// TestMainHoldsItsRootLockedForItsWholeRun: while tests run, this binary's
// own marker is locked and records an owner.
func TestMainHoldsItsRootLockedForItsWholeRun(t *testing.T) {
	root := Root()
	if root == "" {
		t.Fatal("Root() is empty: this binary is not running under Main, or Main does not hold its root's lock")
	}
	if state := rootMarkerState(root); state != markerHeld {
		t.Fatalf("marker state of this binary's own root = %d, want markerHeld (%d)", state, markerHeld)
	}
	info, err := os.Stat(filepath.Join(root, rootMarker))
	if err != nil || info.Size() == 0 {
		t.Fatalf("marker of this binary's own root: %v, %v; want it to record its owner", info, err)
	}
}

// TestRootNeverProbesThisBinarysOwnMarker: once Main has made this binary's
// root, Root reports it without opening its marker. This process holds that
// marker's lock. Where flock is emulated with byte-range locks (NFS,
// SMB/CIFS), a probe through a second open file is either refused, so Root
// would report "" and helper children would not inherit the root, or granted,
// and its unlock and close would drop this process's lock while the root is
// in use.
func TestRootNeverProbesThisBinarysOwnMarker(t *testing.T) {
	var probed []string
	markerProbedHook = func(root string) { probed = append(probed, root) }
	t.Cleanup(func() { markerProbedHook = nil })

	root := Root()
	if root == "" || root != os.Getenv(RootEnv) {
		t.Fatalf("Root() = %q, want the root Main made for this binary (%s=%q)", root, RootEnv, os.Getenv(RootEnv))
	}
	if len(probed) != 0 {
		t.Fatalf("Root() opened the marker of %q to try its lock; it must report this binary's own root without probing the lock this process holds", probed)
	}

	// Control: the hook sees a probe, so the check above can fail.
	other := plantRoot(t, t.TempDir(), RootPrefix+"other", "4242\n")
	rootMarkerState(other)
	if !slices.Equal(probed, []string{other}) {
		t.Fatalf("probes seen after rootMarkerState(%q) = %q; the hook does not see probes, so the check above proves nothing", other, probed)
	}
}

// TestAStaleRootIsReclaimedOnlyOnceItsOwnerIsGone follows a real root through
// its owner's death: a child binary sits in its Main and is then killed, as
// -timeout or a crash would end it.
//
//   - While the owner runs, its root is inherited and no sweep touches it.
//   - Once it is dead, the root is no longer inherited: a binary started with
//     RootEnv naming it scrubs TSLINK_ variables like any top-level run.
//   - That next ordinary run reclaims the root.
func TestAStaleRootIsReclaimedOnlyOnceItsOwnerIsGone(t *testing.T) {
	switch os.Getenv(isolationProbeEnv) {
	case "hold-root":
		fmt.Println(heldRootReportPrefix + Root())
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	case "after-owner-died":
		writeIsolationReport(t)
		return
	}

	parent := privateRootParent(t)
	owner := startRootOwner(t, t.Name())
	root := owner.root
	if filepath.Dir(root) != parent {
		t.Fatalf("owner root = %q, want it under private parent %q", root, parent)
	}
	home := filepath.Join(root, "home")
	requireExists(t, home, "the owner's root must exist while it runs")
	if state := rootMarkerState(root); state != markerHeld {
		t.Fatalf("marker state of a running owner's root = %d, want markerHeld (%d)", state, markerHeld)
	}
	t.Setenv(RootEnv, root)
	if got := inheritedRoot(); got != root {
		t.Fatalf("inheritedRoot() while the owner runs = %q, want %q", got, root)
	}
	reclaimStaleRoots(filepath.Dir(root))
	requireExists(t, home, "a sweep must leave a root alone while its owner runs")

	owner.kill()
	requireExists(t, home, "the killed owner could not have removed its root, so the checks below would prove nothing")
	if !eventually(func() bool { return inheritedRoot() == "" }) {
		t.Fatalf("inheritedRoot() = %q 10s after its owner was killed, want \"\"", inheritedRoot())
	}
	if state := rootMarkerState(root); state != markerAbandoned {
		t.Fatalf("marker state after the owner's death = %d, want markerAbandoned (%d)", state, markerAbandoned)
	}

	// The same kind of child as TestMainKeepsTestChosenTSLinkEnvInAChildOfAnIsolatedBinary,
	// whose live parent root keeps this canary; this root's owner is dead.
	env := append(os.Environ(), isolationProbeEnv+"=after-owner-died", "TSLINK_TEST_HELPER_CANARY=kept-only-under-a-live-parent")
	code, out, report := runIsolationProbe(t, t.Name(), env, "")
	if code != 0 || report == nil {
		t.Fatalf("next run: exit %d, report %v\n%s", code, report, out)
	}
	if filepath.Dir(report.Root) != parent || report.Root == root {
		t.Fatalf("next run root = %q, want a fresh root under private parent %q", report.Root, parent)
	}
	if got, ok := report.Env["TSLINK_TEST_HELPER_CANARY"]; ok {
		t.Fatalf("a binary started with %s naming a root whose owner is dead kept TSLINK_TEST_HELPER_CANARY=%q; that root must not be honoured", RootEnv, got)
	}
	requireGone(t, root, "the next run must reclaim a root whose owner is dead")
}

// TestReclaimStaleRootsRemovesOnlyAbandonedRoots: next to one abandoned root,
// which the sweep must remove (so it ran), every other kind of entry must stay
// exactly as it was.
func TestReclaimStaleRootsRemovesOnlyAbandonedRoots(t *testing.T) {
	dir := t.TempDir()
	abandoned := plantRoot(t, dir, RootPrefix+"abandoned", "4242\n")
	// Created but not yet locked by its owner, or made by a tree from before
	// roots were locked.
	empty := plantRoot(t, dir, RootPrefix+"empty", "")
	// A running owner: this test holds the lock through its own open file.
	held := plantRoot(t, dir, RootPrefix+"held", "4242\n")
	lock, err := os.OpenFile(filepath.Join(held, rootMarker), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = filelock.Unlock(lock)
		_ = lock.Close()
	})
	noMarker := filepath.Join(dir, RootPrefix+"nomarker")
	if err := os.MkdirAll(filepath.Join(noMarker, "home"), 0o700); err != nil {
		t.Fatal(err)
	}
	markerIsADir := filepath.Join(dir, RootPrefix+"markerdir")
	if err := os.MkdirAll(filepath.Join(markerIsADir, rootMarker), 0o700); err != nil {
		t.Fatal(err)
	}
	prefixFile := filepath.Join(dir, RootPrefix+"file")
	if err := os.WriteFile(prefixFile, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unprefixed := plantRoot(t, dir, "not-a-testenv-root", "4242\n")

	reclaimStaleRoots(dir)

	requireGone(t, abandoned, "an unlocked marker that records an owner means the owner is gone")
	requireExists(t, filepath.Join(empty, "home"), "an empty marker may belong to an owner that has not locked it yet")
	requireExists(t, filepath.Join(held, "home"), "a locked marker means the owner is running")
	requireExists(t, filepath.Join(noMarker, "home"), "an entry without a marker was not made by Main")
	requireExists(t, filepath.Join(markerIsADir, rootMarker), "a marker that is not a regular file was not made by Main")
	requireExists(t, prefixFile, "only directories are roots")
	requireExists(t, filepath.Join(unprefixed, "home"), "only entries named "+RootPrefix+"* are considered")
}

// TestReclaimStaleRootsLeavesAnotherUsersRootAlone: on a TMPDIR that users
// share, such as /tmp, the sweep must not remove another user's tree for
// them, however abandoned it looks. A root the owner check reports as another
// user's is left untouched and its marker is never opened; this user's
// abandoned root next to it still goes.
func TestReclaimStaleRootsLeavesAnotherUsersRootAlone(t *testing.T) {
	dir := t.TempDir()
	abandoned := plantRoot(t, dir, RootPrefix+"abandoned", "4242\n")
	foreign := plantRoot(t, dir, RootPrefix+"foreign", "4242\n")
	owned := rootOwnedByThisUser
	rootOwnedByThisUser = func(info os.FileInfo) bool {
		return info.Name() != filepath.Base(foreign) && owned(info)
	}
	var probed []string
	markerProbedHook = func(root string) { probed = append(probed, root) }
	t.Cleanup(func() {
		rootOwnedByThisUser = owned
		markerProbedHook = nil
	})

	reclaimStaleRoots(dir)

	requireGone(t, abandoned, "this user's abandoned root must still be removed, or the sweep did not run")
	requireExists(t, filepath.Join(foreign, "home"), "a root another user owns must be left alone")
	if slices.Contains(probed, foreign) {
		t.Fatalf("the sweep opened the marker of %s, which another user owns; the owner check must come before any marker is opened", foreign)
	}
}

// TestReclaimStaleRootsNeverFollowsASymlink: a symlink named like a root, and
// a root whose marker is a symlink, both point at an abandoned-looking root
// outside the swept directory. Neither the links nor their target may go.
func TestReclaimStaleRootsNeverFollowsASymlink(t *testing.T) {
	dir := t.TempDir()
	target := plantRoot(t, t.TempDir(), "looks-abandoned", "4242\n")
	link := filepath.Join(dir, RootPrefix+"link")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this Windows account may not create symlinks (it needs SeCreateSymbolicLinkPrivilege or Developer Mode): %v", err)
		}
		t.Fatal(err)
	}
	markerLink := filepath.Join(dir, RootPrefix+"markerlink")
	if err := os.Mkdir(markerLink, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(target, rootMarker), filepath.Join(markerLink, rootMarker)); err != nil {
		t.Fatal(err)
	}
	abandoned := plantRoot(t, dir, RootPrefix+"abandoned", "4242\n")

	reclaimStaleRoots(dir)

	requireGone(t, abandoned, "the sweep must have run")
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Lstat(%s) = %v, %v; want the symlink left in place", link, info, err)
	}
	requireExists(t, filepath.Join(target, "home"), "a symlink's target must never be removed")
	requireExists(t, filepath.Join(markerLink, rootMarker), "a marker that is a symlink was not made by Main")
}

// TestARootIsNotReclaimedBeforeItsOwnerLocksIt: another binary's sweep that
// lands between createRoot creating the marker and locking it finds the
// marker empty and leaves the root alone.
func TestARootIsNotReclaimedBeforeItsOwnerLocksIt(t *testing.T) {
	dir := t.TempDir()
	during := markerState(-1)
	markerCreatedHook = func(root string) {
		reclaimStaleRoots(dir)
		during = rootMarkerState(root)
	}
	t.Cleanup(func() { markerCreatedHook = nil })

	root, marker, err := createRoot(dir)
	markerCreatedHook = nil
	if err != nil {
		t.Fatalf("createRoot() error = %v", err)
	}
	if during != markerEmpty {
		t.Fatalf("marker state between creation and locking = %d, want markerEmpty (%d)", during, markerEmpty)
	}
	requireExists(t, filepath.Join(root, rootMarker), "a sweep between creating and locking the marker must not remove the root")
	if state := rootMarkerState(root); state != markerHeld {
		t.Fatalf("marker state after createRoot = %d, want markerHeld (%d)", state, markerHeld)
	}
	if err := removeRoot(root, marker); err != nil {
		t.Fatalf("removeRoot() error = %v", err)
	}
	requireGone(t, root, "removeRoot must remove the root")
}

// TestConcurrentReclaimsAreHarmless: several sweeps of one directory at once
// remove every abandoned root, report nothing, and leave a live root alone.
func TestConcurrentReclaimsAreHarmless(t *testing.T) {
	dir := t.TempDir()
	var abandoned []string
	for i := range 16 {
		abandoned = append(abandoned, plantRoot(t, dir, fmt.Sprintf("%sabandoned-%02d", RootPrefix, i), "4242\n"))
	}
	live, marker, err := createRoot(dir)
	if err != nil {
		t.Fatalf("createRoot() error = %v", err)
	}
	t.Cleanup(func() { _ = removeRoot(live, marker) })

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { reclaimStaleRoots(dir) })
	}
	wg.Wait()

	for _, root := range abandoned {
		requireGone(t, root, "every abandoned root must be removed")
	}
	if state := rootMarkerState(live); state != markerHeld {
		t.Fatalf("marker state of the live root after the sweeps = %d, want markerHeld (%d)", state, markerHeld)
	}
}
