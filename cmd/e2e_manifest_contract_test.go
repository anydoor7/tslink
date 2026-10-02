package cmd

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/spf13/cobra"
)

// E5: every command the manifest declares is classified, and every command
// classified as executable is actually executed against the shipped binary and
// checked against what the manifest promises.
//
// # Classification is fail-closed
//
// This shipped first as a denylist: anything not explicitly denied was
// executed. That made a newly added command *executable by default* — a command
// merged tomorrow would have its body run twice by the default `go test ./...`
// before anyone decided whether that is safe. The independent review
// demonstrated this by registering a command that wrote outside
// TSLINK_CONFIG_DIR: the suite executed it and stayed green.
//
// The model is now an explicit two-way partition. Every manifest command must
// appear in exactly one of e2eExecutableSafeCommands or
// e2eNonExecutableCommands, and the union of the two must equal the manifest
// command set. A new command therefore turns this test red until a human puts
// it on one side, and the direction of the failure is "refuse to execute", not
// "execute and hope".
//
// # The child processes are NOT protected by the package dial guard
//
// internal/testenv.RunWithNonLoopbackDialGuard (installed by TestMain) replaces
// http.DefaultTransport inside *this test process only*. Every command here runs
// as a separately built OS subprocess, so the guard cannot see or stop it. A
// command that opens a socket from its own process is unconstrained by anything
// in this package. That is precisely why admission to the executable-safe set is
// a human judgement recorded below rather than something the harness can
// enforce at runtime.
//
// # Admission criteria for e2eExecutableSafeCommands
//
// A command may be listed as executable-safe only if, invoked with no arguments
// under the isolated environment e2eEnv builds, all four hold:
//
//  1. It performs no external action — no network I/O, no control-plane call,
//     no message to any process outside this test's own children.
//  2. It writes nothing outside TSLINK_CONFIG_DIR. Reads outside are acceptable;
//     any creation, mutation or deletion outside is not.
//  3. It does not drive a system service manager (launchctl, systemctl, sc) or
//     any other host-level registry, whose scope TSLINK_CONFIG_DIR does not
//     bound.
//  4. It consults no credential source other than the empty TSLINK_API_KEY /
//     TSLINK_CLIENT_SECRET this suite injects — in particular not the system
//     keyring, which TSLINK_DISABLE_KEYRING=1 must be enough to suppress.
//
// If any of the four is uncertain for a new command, it belongs in
// e2eNonExecutableCommands with the reason written down. Denial costs coverage;
// a wrong admission costs a side effect on the developer's machine.
//
// # What this test does and does not prove about the manifest
//
// It proves that every manifest entry that is emitted is honoured by the binary.
// It does not prove the manifest is complete: the traversal source and the
// completeness denominator are the same Manifest().Commands slice, so a command
// silently dropped by the manifest walker would simply not be traversed and the
// count would still agree. Completeness is covered separately by
// TestE2EManifestCommandSetMatchesCobraTree below, which walks the live Cobra
// tree through an independent traversal and requires the two sets to be equal.
//
// Relationship to TestCompiledEveryManifestJSONCommandProducesParseableEnvelope:
// that test forces Cobra's flag-validation path with a deliberately invalid
// flag, so no command body ever runs. It answers "does the failure envelope
// survive". This one actually runs the command bodies and answers "does the
// success path honour the declared exit codes and error codes". They are
// complementary; neither subsumes the other.

// e2eExecutableSafeCommands is the explicit allowlist of command bodies this
// suite is permitted to execute. Membership means a human checked the four
// admission criteria documented above. It is cross-checked against the manifest
// in both directions, so it can neither hold a stale entry nor silently fail to
// cover a new command.
//
// The grouping is by what the BARE invocation does, because that is what the
// admission criteria are about. An earlier revision grouped by what each
// command is *for* — "local read-only inspection", "local mutations confined to
// TSLINK_CONFIG_DIR" — which reads well and describes a counterfactual: not one
// of the seven commands under that "mutations" heading mutates anything here,
// because every one of them stops at argument validation before its body runs.
// That mixture is how `tslink invite list` and `tslink cleanup` came to sit
// under a comment claiming argument validation stops them when both are
// cobra.NoArgs and both run their bodies. A reason that describes the command
// rather than the control flow cannot be checked against the code, and this
// table is the only defence the suite has, so every reason below is written as
// control flow.
//
// The claims were taken from the live Cobra tree (whether c.Args rejects a
// zero-argument invocation) and from bare runs under e2eEnv, not inferred from
// the shape of a command's name.
var e2eExecutableSafeCommands = map[string]struct{}{
	"tslink people":        {}, // Group help only; no leaf body.
	"tslink people add":    {}, // ExactArgs(1), no body on bare invocation.
	"tslink people update": {}, // ExactArgs(1), no body on bare invocation.
	"tslink people remove": {}, // ExactArgs(1), no body on bare invocation.
	"tslink people list":   {}, // Local isolated registry/snapshot reads; no credential or external action.
	// (a) Command groups. RunE is runCommandGroup: it prints help and exits 0.
	// No leaf command body runs.
	"tslink":            {},
	"tslink access":     {},
	"tslink apps":       {},
	"tslink apps list":  {}, // Pure embedded catalog; no OS inventory or network I/O.
	"tslink apps share": {}, // ExactArgs(1), bare invocation cannot run.
	"tslink config":     {},
	"tslink invite":     {},
	"tslink registry":   {},
	"tslink tags":       {},
	"tslink template":   {},

	// (b) Cobra argument validation rejects the bare invocation, so the RunE
	// body never runs at all and admission costs nothing to establish. Each
	// declares required positional arguments this suite does not supply;
	// c.Args(c, nil) returns an error and every one of them exits 2 (usage).
	// They are listed individually rather than as a family so that a future
	// change making one of them zero-argument is a deliberate edit here — which
	// is precisely the drift that put two NoArgs commands in this group.
	"tslink access explain":   {}, // ExactArgs(1)
	"tslink add":              {}, // ExactArgs(1)
	"tslink config get":       {}, // ExactArgs(1)
	"tslink config set":       {}, // RangeArgs(1,2)
	"tslink invite device":    {}, // ExactArgs(2)
	"tslink invite resend":    {}, // ExactArgs(1)
	"tslink invite revoke":    {}, // ExactArgs(1)
	"tslink invite user":      {}, // ExactArgs(1)
	"tslink remove":           {}, // ExactArgs(1)
	"tslink tags add":         {}, // ExactArgs(2)
	"tslink tags set":         {}, // ExactArgs(2)
	"tslink tags set-default": {}, // ExactArgs(1)
	"tslink template apply":   {}, // ExactArgs(1)
	"tslink template show":    {}, // ExactArgs(1)
	"tslink url":              {}, // ExactArgs(1)

	// (c) Cobra accepts the bare invocation, so the body RUNS. Each entry
	// records what its body does and the exit code observed under e2eEnv; a
	// change in one of those codes is the cheapest available signal that a body
	// started doing something else. Two of them (cleanup, login) create the
	// nodes/, logs/ and certs/ subdirectories — inside TSLINK_CONFIG_DIR, which
	// is what criterion 2 permits.
	"tslink manifest":       {}, // exit 0. Builds the manifest in process; opens no file.
	"tslink template list":  {}, // exit 0. Enumerates built-in templates in process.
	"tslink config list":    {}, // exit 0. Reads the isolated config; keyring suppressed.
	"tslink list":           {}, // exit 0. Reads the isolated registry/PID/snapshot; absent registry => empty list.
	"tslink tags list":      {}, // exit 0. Reads the isolated registry.
	"tslink status":         {}, // exit 0. Reads the isolated registry/PID/snapshot.
	"tslink logs":           {}, // exit 0. Opens only the isolated log path; absent => no entries.
	"tslink registry check": {}, // exit 0. Reads, or on failure lstats, only the path it was given; creates nothing.
	"tslink doctor":         {}, // exit 64 (warning). Local checks only; probes nothing external by default.
	"tslink stop":           {}, // exit 0. Signals only a daemon recorded in the isolated config dir, of which there is none.
	"tslink logout":         {}, // exit 1. TSLINK_DISABLE_KEYRING=1, so it neither reads nor writes the keyring; it refuses rather than guess.
	// exit 2 both bare and with --json, by two different routes, neither of
	// which reaches loginWithAPIKey/loginWithClientSecret — the only paths that
	// validate a credential remotely. Bare: both injected credential env vars
	// are empty and this is not --json, so it enters the interactive menu
	// (cmd/login.go:170) and the suite's empty stdin makes that menu read an
	// empty choice and return a usage error (cmd/login.go:677). With --json:
	// interactive login is refused outright (cmd/login.go:160).
	"tslink login": {},

	// (c.2) The two entries the review caught. Both are cobra.NoArgs and both
	// enter their bodies, which is the opposite of the reason they used to
	// carry. They are admitted on what those bodies do against the registry-less
	// isolated config dir e2eEnv guarantees:
	//
	//   invite list — inviteDeviceTargetsForListPaths (cmd/invite.go:124) reads
	//     registry.json first and returns output.ErrConflict when it is absent,
	//     which is the state here, so tailapi.ListInvites is never reached.
	//     Observed bare exit 4 (conflict), "registry.json is missing; invite
	//     completeness cannot be established".
	//   cleanup — enters lifecycle.Reconcile, but an absent registry yields zero
	//     remote targets and --manage-acl defaults to false, so neither remote
	//     device deletion nor an ACL mutation is attempted. Observed bare exit 0,
	//     "cleanup dry-run: expired_funnels=0 devices_deleted=0
	//     devices_protected=0 acl=not_requested".
	//
	// Both satisfy the four criteria, but through the contents of the isolated
	// config dir rather than through Cobra. Populate that registry and the
	// reasoning changes, which is exactly why the reason has to name the
	// mechanism instead of a family resemblance.
	"tslink invite list": {},
	"tslink cleanup":     {},
}

// e2eNonExecutableCommands are excluded from body execution, with the reason
// each one is excluded. A command is denied only when running it would escape
// the test's isolation, never merely because it is inconvenient.
var e2eNonExecutableCommands = map[string]string{
	"tslink apps detect":        "probes local HTTP listeners; config isolation cannot isolate other applications, covered with hermetic listeners in recipes tests",
	"tslink install":            "writes a real LaunchAgent/systemd unit and calls launchctl/systemctl against the operator's live user domain, which TSLINK_CONFIG_DIR does not isolate",
	"tslink uninstall":          "removes a real LaunchAgent/systemd unit from the operator's live user domain",
	"tslink serve":              "starts tsnet nodes and contacts the Tailscale control plane",
	"tslink share":              "starts a serve daemon, so it contacts the Tailscale control plane",
	"tslink mcp":                "speaks JSON-RPC on stdout by design and declares no --json flag, so the result-envelope contract does not apply",
	"tslink tags pull":          "reads the remote Tailscale ACL policy",
	"tslink tags delete-remote": "deletes a remote Tailscale ACL tag owner",
}

// e2eDecoupledException describes one command whose process exit code is
// deliberately not its envelope code.
type e2eDecoupledException struct {
	// Reason states why the two signals differ.
	Reason string
	// CoveredBy names the test that independently exercises this command's full
	// envelope contract, including the states the exception makes E5 blind to.
	//
	// It is a function value rather than a name in a comment on purpose: the
	// compiler then enforces that the cited test exists, and deleting or
	// renaming it breaks the build here instead of quietly leaving an exception
	// with nothing behind it. Producing such a test is the price of admission to
	// this table, which is what stops it from becoming a general escape hatch.
	// A bare cardinality assertion ("there must be exactly one entry") was
	// considered instead and rejected: it would fire on the count while saying
	// nothing about whether any given exception is safe.
	CoveredBy func(*testing.T)
}

// e2eProcessExitDecoupledCommands are the commands whose process exit code is
// deliberately not the envelope code.
//
// The general rule below is that a machine reader gets the same answer from the
// process exit status and from the final envelope. `tslink doctor` is a real,
// intentional exception: the envelope reports whether *the doctor ran*
// (ok=true, code=0) while the process exit status reports *what the doctor
// found* (64 when there are warnings). An earlier brief asked for the process
// exit and the envelope code to be equal unconditionally; that is false on
// healthy code today, and asserting it would have reported a defect that is not
// one.
//
// Listing a command here is a *requirement to succeed*, not a permission to
// differ. Four conditions hold for every entry, and all four are enforced in
// assertEnvelopeContract:
//
//  1. the final envelope is ok=true;
//  2. it carries ExitSuccess;
//  3. it publishes its verdict in-band as data.health_exit_code;
//  4. the process exit code equals that published value.
//
// Gates 1 and 2 were the ones missing. The review showed the consequence: a
// perfectly legal failure envelope (ok=false + internal_error + code=1) that
// still carried data.health_exit_code, with a process exit matching that field,
// was accepted by this branch, so replacing runDoctor's envelope with
// output.NewFailure while keeping Data left the whole of E5 green.
var e2eProcessExitDecoupledCommands = map[string]e2eDecoupledException{
	"tslink doctor": {
		Reason:    "the envelope reports that the health check completed; the process exit status reports the health verdict, which the envelope also publishes as data.health_exit_code",
		CoveredBy: TestCompiledDoctorJSONHealthFixtures,
	},
}

func TestE2EManifestDeclaredCommandsHonourTheirDeclaredContract(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	manifest := Manifest()

	declared := make(map[string]CommandInfo, len(manifest.Commands))
	for _, command := range manifest.Commands {
		declared[command.Path] = command
	}

	// Drift guard 1: a classification that no longer names a real command is a
	// stale entry and must be deleted, not silently ignored.
	for path := range e2eNonExecutableCommands {
		if _, ok := declared[path]; !ok {
			t.Fatalf("exclusion list names %q, which the manifest no longer declares; delete the stale entry", path)
		}
	}
	for path := range e2eExecutableSafeCommands {
		if _, ok := declared[path]; !ok {
			t.Fatalf("executable-safe list names %q, which the manifest no longer declares; delete the stale entry", path)
		}
	}
	for path, exception := range e2eProcessExitDecoupledCommands {
		if _, ok := declared[path]; !ok {
			t.Fatalf("process-exit-decoupled list names %q, which the manifest no longer declares; delete the stale entry", path)
		}
		if _, denied := e2eNonExecutableCommands[path]; denied {
			t.Fatalf("%q is both denied and declared process-exit-decoupled; a denied command never reaches the decoupled assertion", path)
		}
		if exception.Reason == "" || exception.CoveredBy == nil {
			t.Fatalf("%q is declared process-exit-decoupled without a Reason and a CoveredBy test; "+
				"an exception that nothing else covers is a hole, not an exception", path)
		}
	}

	// Drift guard 2, the fail-closed gate: the two classifications must
	// partition the manifest exactly. An unclassified command is refused
	// execution and fails the test, so a newly added command cannot be run by
	// default before somebody decides it is safe to run.
	var unclassified, bothClassified []string
	for path := range declared {
		_, safe := e2eExecutableSafeCommands[path]
		_, denied := e2eNonExecutableCommands[path]
		switch {
		case safe && denied:
			bothClassified = append(bothClassified, path)
		case !safe && !denied:
			unclassified = append(unclassified, path)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(bothClassified)
	if len(bothClassified) > 0 {
		t.Fatalf("commands classified both executable-safe and non-executable: %v", bothClassified)
	}
	if len(unclassified) > 0 {
		t.Fatalf("manifest declares %d command(s) this suite refuses to execute because they are unclassified: %v\n"+
			"Add each one to e2eExecutableSafeCommands (after checking the four admission criteria documented at the top of this file) "+
			"or to e2eNonExecutableCommands with the reason it must not run.", len(unclassified), unclassified)
	}
	if len(e2eExecutableSafeCommands)+len(e2eNonExecutableCommands) != len(declared) {
		t.Fatalf("classification sizes do not partition the manifest: safe=%d denied=%d manifest=%d",
			len(e2eExecutableSafeCommands), len(e2eNonExecutableCommands), len(declared))
	}

	allowedExit := make(map[int]string, len(manifest.ExitCodes))
	for name, code := range manifest.ExitCodes {
		allowedExit[code] = name
	}
	if len(allowedExit) == 0 {
		t.Fatal("manifest declares no exit codes; the contract is unverifiable")
	}

	executed, denied := 0, 0
	observedExit := map[int]int{}
	observedErrorCodes := map[string]int{}

	for _, command := range manifest.Commands {
		command := command
		t.Run(strings.ReplaceAll(command.Path, " ", "_"), func(t *testing.T) {
			args := strings.Fields(strings.TrimPrefix(command.Path, "tslink"))

			if reason, blocked := e2eNonExecutableCommands[command.Path]; blocked {
				denied++
				// Still assert the envelope contract, via the same
				// non-mutating flag-validation probe the existing binary
				// contract suite uses. Denial reduces what we learn; it must
				// not reduce coverage to zero.
				if !commandDeclaresJSONFlag(command) {
					return
				}
				probe := append(append([]string(nil), args...), "--json", "--tslink-contract-probe-invalid-flag")
				run := e2eRunBinary(t, binary, t.TempDir(), "", e2eEnv(t.TempDir()), probe...)
				assertEnvelopeContract(t, run, command.Path, command.Path+" (probe: "+reason+")", allowedExit, manifest.ErrorCodes, observedExit, observedErrorCodes)
				return
			}
			// Unreachable for an unclassified command: the partition gate above
			// is t.Fatalf, so control only arrives here for an explicitly
			// admitted executable-safe command.
			executed++

			// Bare invocation: no --json. Exit code must still be declared.
			bareDir := t.TempDir()
			bare := e2eRunBinary(t, binary, bareDir, "", e2eEnv(bareDir), args...)
			if name, ok := allowedExit[bare.ExitCode]; !ok {
				t.Fatalf("%s (bare) exit = %d, which the manifest does not declare (declared: %v)\nstdout=%s\nstderr=%s",
					command.Path, bare.ExitCode, sortedExitNames(allowedExit), bare.Stdout, bare.Stderr)
			} else {
				t.Logf("%s (bare) exit=%d (%s)", command.Path, bare.ExitCode, name)
			}
			observedExit[bare.ExitCode]++

			if !commandDeclaresJSONFlag(command) {
				t.Fatalf("%s declares no --json flag and is not in the exclusion list", command.Path)
			}

			// --json invocation: full envelope contract.
			jsonDir := t.TempDir()
			run := e2eRunBinary(t, binary, jsonDir, "", e2eEnv(jsonDir), append(append([]string(nil), args...), "--json")...)
			assertEnvelopeContract(t, run, command.Path, command.Path, allowedExit, manifest.ErrorCodes, observedExit, observedErrorCodes)
		})
	}

	// Drift guard 3: the traversal must actually have traversed.
	if executed == 0 || executed+denied != len(manifest.Commands) {
		t.Fatalf("traversal incomplete: executed=%d denied=%d manifest=%d", executed, denied, len(manifest.Commands))
	}
	if len(e2eNonExecutableCommands) != denied {
		t.Fatalf("denied=%d but the exclusion list has %d entries", denied, len(e2eNonExecutableCommands))
	}
	if len(e2eExecutableSafeCommands) != executed {
		t.Fatalf("executed=%d but the executable-safe list has %d entries", executed, len(e2eExecutableSafeCommands))
	}
	t.Logf("manifest commands=%d executed=%d denied=%d", len(manifest.Commands), executed, denied)
	t.Logf("observed exit codes: %v", observedExit)
	t.Logf("observed error codes: %v", observedErrorCodes)
}

// TestE2EManifestCommandSetMatchesCobraTree is the manifest-completeness oracle
// the traversal above cannot be.
//
// TestE2EManifestDeclaredCommandsHonourTheirDeclaredContract iterates
// Manifest().Commands and measures completeness against the same slice, so it
// proves fidelity to the manifest and nothing about whether the manifest is
// complete. `gen-manifest -check` is not an independent oracle either: it
// compares the freshly generated manifest against the committed generated
// artifact, so an omission shared by both passes.
//
// This test reaches the other authority directly. Living in package cmd, it can
// walk the live Cobra tree that Manifest() itself walks, through a separate
// iterative traversal, and require the two command sets to be equal. A
// traversal or filter regression in the manifest walker — the failure mode the
// review reproduced by returning early on "tslink url" — makes the sets differ
// and this test red.
//
// The exclusion policy is restated here on purpose. It is the contract the
// manifest walker is supposed to implement, not a copy taken from it, so
// changing the production filter without changing this test is itself a
// detected difference.
func TestE2EManifestCommandSetMatchesCobraTree(t *testing.T) {
	manifestPaths := map[string]struct{}{}
	for _, command := range Manifest().Commands {
		if _, duplicate := manifestPaths[command.Path]; duplicate {
			t.Fatalf("manifest declares %q twice", command.Path)
		}
		manifestPaths[command.Path] = struct{}{}
	}

	cobraPaths := map[string]struct{}{}
	type pending struct {
		cmd    *cobra.Command
		prefix string
	}
	queue := []pending{{cmd: rootCmd, prefix: ""}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		path := strings.TrimSpace(current.prefix + " " + current.cmd.Name())
		if _, seen := cobraPaths[path]; seen {
			t.Fatalf("Cobra tree reaches %q twice", path)
		}
		cobraPaths[path] = struct{}{}
		for _, sub := range current.cmd.Commands() {
			if e2eManifestExcludesSubcommand(sub) {
				continue
			}
			queue = append(queue, pending{cmd: sub, prefix: path})
		}
	}

	missingFromManifest := e2eSortedSetDifference(cobraPaths, manifestPaths)
	extraInManifest := e2eSortedSetDifference(manifestPaths, cobraPaths)
	if len(missingFromManifest) > 0 {
		t.Errorf("the Cobra tree exposes %d command(s) the manifest does not declare: %v\n"+
			"Agents treat the manifest as the command SSOT, so an undeclared command is invisible to them.",
			len(missingFromManifest), missingFromManifest)
	}
	if len(extraInManifest) > 0 {
		t.Errorf("the manifest declares %d command(s) the Cobra tree does not expose: %v",
			len(extraInManifest), extraInManifest)
	}
	if t.Failed() {
		return
	}
	t.Logf("manifest and Cobra tree agree on %d commands", len(cobraPaths))
}

// e2eManifestExcludesSubcommand restates the manifest walker's exclusion policy:
// generated help/completion commands are not part of the published surface, and
// hidden commands are excluded except for `manifest` itself.
func e2eManifestExcludesSubcommand(sub *cobra.Command) bool {
	if sub.Name() == "help" || sub.Name() == "completion" {
		return true
	}
	return sub.Hidden && sub.Name() != "manifest"
}

func e2eSortedSetDifference(from, subtract map[string]struct{}) []string {
	var diff []string
	for key := range from {
		if _, ok := subtract[key]; !ok {
			diff = append(diff, key)
		}
	}
	sort.Strings(diff)
	return diff
}

// assertEnvelopeContract checks the machine-readable contract of one --json
// invocation: stdout is one-or-more well-formed envelopes, stderr is empty so a
// parser is never handed interleaved text, the process exit code is declared by
// the manifest, every success envelope carries ExitSuccess, any error.code is
// one the manifest declares with the exit code the manifest assigns it, and the
// process exit code agrees with the final envelope.
//
// For the commands in e2eProcessExitDecoupledCommands the last of those becomes
// "agrees with the health code the final envelope publishes", and only that one.
// Entry to the exception additionally requires the final envelope to be ok=true
// carrying ExitSuccess, so the exception cannot be used to admit a failure.
func assertEnvelopeContract(
	t *testing.T,
	run e2eRun,
	commandPath string,
	label string,
	allowedExit map[int]string,
	errorCodes map[string]ErrorCodeInfo,
	observedExit map[int]int,
	observedErrorCodes map[string]int,
) {
	t.Helper()

	if _, ok := allowedExit[run.ExitCode]; !ok {
		t.Fatalf("%s --json exit = %d, which the manifest does not declare (declared: %v)\nstdout=%s\nstderr=%s",
			label, run.ExitCode, sortedExitNames(allowedExit), run.Stdout, run.Stderr)
	}
	observedExit[run.ExitCode]++

	if run.Stderr != "" {
		t.Fatalf("%s --json wrote %d bytes to stderr; machine-readable mode must keep stdout parseable and stderr silent\nstderr=%q",
			label, len(run.Stderr), run.Stderr)
	}

	// Every --json invocation must publish at least one envelope. No command in
	// the current surface can legitimately print nothing, so empty stdout is
	// unconditionally a contract violation.
	if strings.TrimSpace(run.Stdout) == "" {
		t.Fatalf("%s --json produced no envelope on stdout", label)
	}

	results := parseCompiledJSONLines(t, run.Stdout)
	if len(results) == 0 {
		t.Fatalf("%s --json produced no parseable envelope\nstdout=%s", label, run.Stdout)
	}
	for i, result := range results {
		if _, ok := allowedExit[result.Code]; !ok {
			t.Fatalf("%s --json record %d carries code=%d, which the manifest does not declare", label, i, result.Code)
		}
		if result.OK && result.Error != nil {
			t.Fatalf("%s --json record %d is ok=true but carries an error object: %+v", label, i, result.Error)
		}
		if result.OK && result.Code != output.ExitSuccess {
			// A success envelope carrying some other declared code (say
			// ExitWarning) lets one command read as success or as warning
			// depending on which field the agent consumes. "Declared" is not
			// enough; a success envelope must carry ExitSuccess.
			t.Fatalf("%s --json record %d is ok=true but carries code=%d; a success envelope must carry ExitSuccess (%d)",
				label, i, result.Code, output.ExitSuccess)
		}
		if !result.OK {
			if result.Error == nil {
				t.Fatalf("%s --json record %d is ok=false with no error object", label, i)
			}
			info, declaredCode := errorCodes[result.Error.Code]
			if !declaredCode {
				t.Fatalf("%s --json record %d uses error.code=%q, which the manifest does not declare",
					label, i, result.Error.Code)
			}
			if info.ExitCode != result.Code {
				t.Fatalf("%s --json record %d: error.code=%q is declared with exit %d but the envelope code is %d",
					label, i, result.Error.Code, info.ExitCode, result.Code)
			}
			if result.Error.Message == "" {
				t.Fatalf("%s --json record %d has an empty error.message", label, i)
			}
			observedErrorCodes[result.Error.Code]++
		}
	}

	// The process exit code must match the last envelope's code, otherwise an
	// agent reading either signal alone gets a different answer. This is
	// asserted for successes as well as failures: the review showed that
	// gating it on !last.OK let a success envelope drift away from the process
	// exit without any test noticing.
	last := results[len(results)-1]
	if exception, decoupled := e2eProcessExitDecoupledCommands[commandPath]; decoupled {
		// The exception narrows exactly one of the four gates documented on
		// e2eProcessExitDecoupledCommands: "process exit == envelope code"
		// becomes "process exit == the health code the envelope publishes". The
		// other three are asserted here, at the point the exception is taken, so
		// that four gates read as four gates.
		//
		// Gate 1 in particular cannot be delegated to the loop above. That loop
		// says "if ok=true then the code must be ExitSuccess"; nothing said "to
		// enter this branch at all you must be ok=true". Without it a legal
		// failure envelope that happened to carry data.health_exit_code was
		// admitted, and the review demonstrated the hole by turning runDoctor's
		// envelope into output.NewFailure with Data retained: E5 stayed green.
		if !last.OK {
			t.Fatalf("%s is declared process-exit-decoupled (%s) but its final envelope is ok=false (error=%+v).\n"+
				"The decoupling describes a command that ran successfully and is reporting a verdict; a failure envelope has no verdict to publish "+
				"and must not be reconciled through data.health_exit_code.", label, exception.Reason, last.Error)
		}
		if last.Code != output.ExitSuccess {
			t.Fatalf("%s is declared process-exit-decoupled but its final envelope carries code=%d; a decoupled command's envelope must carry ExitSuccess (%d)",
				label, last.Code, output.ExitSuccess)
		}
		health, published := e2eEnvelopeHealthExitCode(t, last)
		if !published {
			t.Fatalf("%s is declared process-exit-decoupled (%s) but its final envelope publishes no data.health_exit_code, "+
				"so a machine reader cannot reconcile the process exit with the envelope", label, exception.Reason)
		}
		if run.ExitCode != health {
			t.Fatalf("%s --json process exit = %d but the envelope publishes data.health_exit_code = %d",
				label, run.ExitCode, health)
		}
		return
	}
	if run.ExitCode != last.Code {
		t.Fatalf("%s --json process exit = %d but final envelope code = %d", label, run.ExitCode, last.Code)
	}
}

// e2eEnvelopeHealthExitCode reads data.health_exit_code from an envelope,
// reporting whether the field was present and integral.
func e2eEnvelopeHealthExitCode(t *testing.T, result output.Result) (int, bool) {
	t.Helper()
	if result.Data == nil {
		return 0, false
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatalf("marshal envelope data: %v", err)
	}
	var payload struct {
		HealthExitCode *int `json:"health_exit_code"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, false
	}
	if payload.HealthExitCode == nil {
		return 0, false
	}
	return *payload.HealthExitCode, true
}

func commandDeclaresJSONFlag(command CommandInfo) bool {
	for _, flag := range command.Flags {
		if flag.Name == "json" {
			return true
		}
	}
	return false
}

func sortedExitNames(allowed map[int]string) []string {
	names := make([]string, 0, len(allowed))
	for code, name := range allowed {
		names = append(names, name+"="+itoa(code))
	}
	sort.Strings(names)
	return names
}

func itoa(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}
