package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// writeRealDaemonSidecar produces a fixture by calling the real production
// daemon.WritePID/WritePIDWithBuildIdentity against a scratch path, then
// copying the raw bytes those functions actually wrote into env.pidPath's
// sidecar location. This deliberately avoids hand-writing what the sidecar
// "should" look like: the fixture is only as good as the real encoder that
// produced it, so any future field added to the wire shape is automatically
// reflected here without this test file needing to change.
func writeRealDaemonSidecar(t *testing.T, targetPIDPath string, withBuildIdentity bool, buildVersion string) {
	t.Helper()
	scratch := filepath.Join(t.TempDir(), "scratch.pid")
	var err error
	if withBuildIdentity {
		err = daemon.WritePIDWithBuildIdentity(scratch, buildVersion)
	} else {
		err = daemon.WritePID(scratch)
	}
	if err != nil {
		t.Fatalf("write scratch daemon sidecar: %v", err)
	}
	raw, err := os.ReadFile(scratch + ".identity")
	if err != nil {
		t.Fatalf("ReadFile(scratch identity) error = %v", err)
	}
	if err := os.WriteFile(targetPIDPath+".identity", raw, 0o600); err != nil {
		t.Fatalf("WriteFile(target identity) error = %v", err)
	}
}

// TestDoctorBuildSkew_NoSidecarAtAllProducesNoFindingByDefault is the
// existing-behavior guard: newDoctorTestEnv never writes a sidecar, and the
// default test process has Version=="" (never resolved by main.go, since
// tests never call main()), so both sides must be unmeasurable and the
// build-skew check must stay silent -- exactly what
// TestDoctorHealthyLoopbackCanExitZero already asserts (zero warnings) for
// the whole doctor run. This test isolates that same guarantee for the
// build-skew finding specifically, independent of that broader test's other
// assertions.
func TestDoctorBuildSkew_NoSidecarAtAllProducesNoFindingByDefault(t *testing.T) {
	newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = true, want false when neither side has a build identity")
	}
	if result.Daemon.BuildVersion != "" || result.Daemon.Executable != "" {
		t.Fatalf("Daemon build fields = version=%q executable=%q, want both empty for a missing sidecar", result.Daemon.BuildVersion, result.Daemon.Executable)
	}
}

// TestDoctorBuildSkew_BothSidesUnmeasuredProducesNoFinding is constraint #4's
// explicit "both sides can't determine a build identity -> don't report"
// case, this time with a real sidecar present (daemon ran the new code, but
// its own selfBuildIdentity()-equivalent returned "" when it wrote the
// sidecar) rather than no sidecar at all.
func TestDoctorBuildSkew_BothSidesUnmeasuredProducesNoFinding(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	writeRealDaemonSidecar(t, env.pidPath, true, "")

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = developmentVersionUnmeasured, ""

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = true, want false when both sides are unmeasured")
	}
}

// TestDoctorBuildSkew_MatchingBuildsProduceNoFinding is the true-negative:
// the daemon's sidecar and this CLI invocation resolve to the identical
// build identity, so nothing should fire even though both sides are
// genuinely measurable this time.
func TestDoctorBuildSkew_MatchingBuildsProduceNoFinding(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = "v3.4.5", "cafef00dbeef"

	writeRealDaemonSidecar(t, env.pidPath, true, selfBuildIdentity())

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = true, want false when the daemon's build matches this CLI's build")
	}
	if result.Daemon.BuildVersion != "v3.4.5 (cafef00dbeef)" {
		t.Fatalf("Daemon.BuildVersion = %q, want the matching build string", result.Daemon.BuildVersion)
	}
}

// TestDoctorBuildSkew_DifferingBuildsWarnWithBothValues is the primary
// positive case: two genuinely different builds must produce exactly one
// daemon_build_skew warning carrying both sides' identity strings, and must
// not touch daemon.running/health_status beyond the warning severity itself
// (constraint #1: reporting-only).
func TestDoctorBuildSkew_DifferingBuildsWarnWithBothValues(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	writeRealDaemonSidecar(t, env.pidPath, true, "v1.0.0 (aaaaaaaaaaaa)")

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = "v2.0.0", "bbbbbbbbbbbb"

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)

	finding := assertDoctorFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if finding.Severity != "warning" {
		t.Fatalf("daemon_build_skew severity = %q, want warning", finding.Severity)
	}
	if finding.Evidence["daemon_build"] != "v1.0.0 (aaaaaaaaaaaa)" {
		t.Fatalf("evidence daemon_build = %q, want the daemon's reported build", finding.Evidence["daemon_build"])
	}
	if finding.Evidence["cli_build"] != "v2.0.0 (bbbbbbbbbbbb)" {
		t.Fatalf("evidence cli_build = %q, want this CLI's build", finding.Evidence["cli_build"])
	}
	if !contains(finding.Message, "v1.0.0 (aaaaaaaaaaaa)") || !contains(finding.Message, "v2.0.0 (bbbbbbbbbbbb)") {
		t.Fatalf("daemon_build_skew message = %q, want both build strings present", finding.Message)
	}
	// When this CLI does carry a build stamp, reinstalling from it is the
	// remediation, and this is the direction that must say so -- the mirror
	// direction (unmeasurable CLI) deliberately says something else, see
	// TestDoctorBuildSkew_UnmeasurableCLIAgainstMeasuredDaemonStillWarns.
	if !contains(finding.Message, "run 'tslink install'") {
		t.Fatalf("daemon_build_skew message = %q, want the reinstall remediation when this CLI's build is known", finding.Message)
	}
	// The evidence map's daemon_executable must carry the path the real
	// encoder recorded, not just be present: writeRealDaemonSidecar ran
	// daemon.WritePIDWithBuildIdentity inside this same test process, whose
	// executable() seam is os.Executable, so this is the exact expected value.
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if finding.Evidence["daemon_executable"] != selfExe {
		t.Fatalf("evidence daemon_executable = %q, want the executable the sidecar recorded (%q)", finding.Evidence["daemon_executable"], selfExe)
	}
	if result.Daemon.Executable != selfExe {
		t.Fatalf("Daemon.Executable = %q, want the same recorded executable the evidence carries (%q)", result.Daemon.Executable, selfExe)
	}

	if !result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = false, want true for differing builds")
	}
	if !result.Daemon.Running {
		t.Fatal("Daemon.Running = false, want true: build skew must not affect daemon liveness reporting")
	}
	if result.Status != doctorStatusWarning || result.HealthStatus != doctorStatusWarning {
		t.Fatalf("Status=%q HealthStatus=%q, want warning from a build-skew-only finding", result.Status, result.HealthStatus)
	}
}

// TestDoctorBuildSkew_MissingSidecarFieldsStillWarnsAsUnknown covers the
// "sidecar predates build-identity reporting entirely" case: a real daemon
// that never wrote build_version/executable at all (WritePID, not
// WritePIDWithBuildIdentity). When this CLI can measure its own build, that
// must still produce a warning -- the daemon is provably not running the
// same code that could report a build identity -- rendered as "unknown" on
// the daemon side rather than an empty string in the message.
func TestDoctorBuildSkew_MissingSidecarFieldsStillWarnsAsUnknown(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	writeRealDaemonSidecar(t, env.pidPath, false, "")

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = "v2.0.0", "bbbbbbbbbbbb"

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)

	finding := assertDoctorFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if finding.Evidence["daemon_build"] != "" {
		t.Fatalf("evidence daemon_build = %q, want empty for a sidecar predating build-identity reporting", finding.Evidence["daemon_build"])
	}
	if !contains(finding.Message, "unknown") {
		t.Fatalf("daemon_build_skew message = %q, want an explicit unknown phrase for the missing daemon side", finding.Message)
	}
	if !result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = false, want true when the sidecar predates build-identity reporting")
	}
}

// TestDoctorBuildSkew_UnmeasurableCLIAgainstMeasuredDaemonStillWarns is the
// mirror of _MissingSidecarFieldsStillWarnsAsUnknown: there the daemon was
// the side that could not report a build, here it is this CLI. That asymmetry
// is a genuine difference and must still warn -- only two unknowns suppress
// the finding, never one. Without this case, adding a `|| selfIdentity == ""`
// suppression to diagnoseDaemonBuildSkew's comparison passes the entire cmd
// suite, so this direction had no test holding it in place.
//
// It also pins the half of the remediation that only exists in this
// direction: a CLI with no build stamp must not be advertised as the binary
// to reinstall from, because doing so would swap a daemon whose build is
// identified for one that is not.
func TestDoctorBuildSkew_UnmeasurableCLIAgainstMeasuredDaemonStillWarns(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	writeRealDaemonSidecar(t, env.pidPath, true, "v1.0.0 (aaaaaaaaaaaa)")

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = developmentVersionUnmeasured, ""
	if got := selfBuildIdentity(); got != "" {
		t.Fatalf("fixture did not verify: selfBuildIdentity() = %q, want the unmeasured empty string", got)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)

	finding := assertDoctorFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if finding.Severity != "warning" {
		t.Fatalf("daemon_build_skew severity = %q, want warning", finding.Severity)
	}
	if finding.Evidence["daemon_build"] != "v1.0.0 (aaaaaaaaaaaa)" {
		t.Fatalf("evidence daemon_build = %q, want the daemon's reported build", finding.Evidence["daemon_build"])
	}
	if finding.Evidence["cli_build"] != "" {
		t.Fatalf("evidence cli_build = %q, want empty for a CLI that cannot measure its own build", finding.Evidence["cli_build"])
	}
	if !contains(finding.Message, "v1.0.0 (aaaaaaaaaaaa)") {
		t.Fatalf("daemon_build_skew message = %q, want the daemon's build string", finding.Message)
	}
	if !contains(finding.Message, "unknown") {
		t.Fatalf("daemon_build_skew message = %q, want an explicit unknown phrase for the missing CLI side", finding.Message)
	}
	if !contains(finding.Message, "release build") {
		t.Fatalf("daemon_build_skew message = %q, want the remediation to ask for a release build rather than reinstalling from this unstamped one", finding.Message)
	}
	if !result.Daemon.BuildSkew {
		t.Fatal("Daemon.BuildSkew = false, want true when only this CLI is unmeasurable")
	}
	if result.Daemon.BuildVersion != "v1.0.0 (aaaaaaaaaaaa)" {
		t.Fatalf("Daemon.BuildVersion = %q, want the daemon's reported build", result.Daemon.BuildVersion)
	}
	if !result.Daemon.Running {
		t.Fatal("Daemon.Running = false, want true: build skew must not affect daemon liveness reporting")
	}
}

// TestDoctorBuildSkew_ExecutableIsAlwaysReportedIndependentlyOfBuildVersion
// checks that Daemon.Executable is populated from whatever the sidecar
// carries even in the "both sides unmeasured, no finding" case: Executable
// is informational evidence, not part of the skew decision itself (only
// BuildVersion gates the finding).
func TestDoctorBuildSkew_ExecutableIsAlwaysReportedIndependentlyOfBuildVersion(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	writeRealDaemonSidecar(t, env.pidPath, true, "")

	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = developmentVersionUnmeasured, ""

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonBuildSkew)
	if result.Daemon.Executable == "" {
		t.Fatal("Daemon.Executable = \"\", want the sidecar's resolved executable path even when BuildVersion is empty")
	}
}
