package testenv

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// execCallText builds the source text for a real process exit to a service
// manager. The binary name arrives as a parameter and is quoted at runtime, so
// no line in this file matches serviceManagerTestExecPattern itself. Writing
// the call out literally here would make this file a finding of its own and
// force it onto the allowlist, which would then mean the allowlist no longer
// says "these files really exec a service manager".
func execCallText(binary, verb string) string {
	return "\t_, _ = exec.Command(" + strconv.Quote(binary) + ", " + strconv.Quote(verb) + ").CombinedOutput()\n"
}

// argSpreadCloser is written in pieces so that no line in this file matches
// serviceManagerTestArgPassthroughPattern itself, for the same reason
// execCallText quotes its binary at runtime: a scanner's own test file must not
// become one of its findings.
var argSpreadCloser = "." + ".." + ")"

// passthroughCallText builds the source text for a spawn that forwards
// caller-supplied arguments verbatim. It names no service manager, which is
// exactly why the two scans above cannot see it.
func passthroughCallText(program string) string {
	return "\t_, _ = exec.Command(" + program + ", args" + argSpreadCloser + "\n"
}

// literalText is a non-exec mention of a manager name, the shape the
// non-test inventory counts.
func literalText(binary string) string {
	return "\tname := " + strconv.Quote(binary) + "\n\t_ = name\n"
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// syntheticTree is the unmutated control corpus: one reviewed non-test file
// with one mention, one reviewed test file with one real exec, and one file
// that mentions nothing. Every negative case below is this tree plus exactly
// one change.
func syntheticTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "cmd/install_darwin.go", "package cmd\n\nfunc a() {\n"+literalText("launchctl")+"}\n")
	writeFile(t, root, "cmd/quiet.go", "package cmd\n\nfunc b() {}\n")
	writeFile(t, root, "cmd/e2e_test.go", "package cmd\n\nfunc c() {\n"+execCallText("systemctl", "--user")+"}\n")
	writeFile(t, root, "cmd/passthrough_test.go", "package cmd\n\nfunc d() {\n"+passthroughCallText("binary")+"}\n")
	return root
}

var syntheticInventory = map[string]serviceManagerExitEntry{
	"cmd/install_darwin.go": {note: "seam", occurrences: 1},
}

var syntheticTestAllowlist = map[string]serviceManagerExitEntry{
	"cmd/e2e_test.go": {note: "gated e2e", occurrences: 1},
}

var syntheticPassthroughAllowlist = map[string]serviceManagerExitEntry{
	"cmd/passthrough_test.go": {note: "reviewed compiled-binary helper", occurrences: 1},
}

func scanPassthrough(t *testing.T, root string) map[string]int {
	t.Helper()
	found, err := scanServiceManagerTestArgPassthroughs(root)
	if err != nil {
		t.Fatalf("scan argument pass-throughs: %v", err)
	}
	return found
}

func scanBoth(t *testing.T, root string) (nonTest, testFiles map[string]int) {
	t.Helper()
	nonTest, err := scanServiceManagerLiterals(root)
	if err != nil {
		t.Fatalf("scan non-test files: %v", err)
	}
	testFiles, err = scanServiceManagerTestExecs(root)
	if err != nil {
		t.Fatalf("scan test files: %v", err)
	}
	return nonTest, testFiles
}

// TestInventoryScannerControlGroupIsClean is the control every negative case
// below is measured against. A batch of red negatives proves nothing unless
// the unmutated corpus is green through the same code path.
func TestInventoryScannerControlGroupIsClean(t *testing.T) {
	root := syntheticTree(t)
	nonTest, testFiles := scanBoth(t, root)

	if got := nonTest["cmd/install_darwin.go"]; got != 1 {
		t.Fatalf("control: reviewed file counted %d mentions, want 1 (the scanner does not see the corpus at all)", got)
	}
	if got := testFiles["cmd/e2e_test.go"]; got != 1 {
		t.Fatalf("control: reviewed test file counted %d execs, want 1", got)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(nonTest, syntheticInventory)
	if len(unexpected)+len(missing)+len(miscounted) > 0 {
		t.Fatalf("control non-test diff not clean: unexpected=%v missing=%v miscounted=%v", unexpected, missing, miscounted)
	}
	unexpected, missing, miscounted = diffServiceManagerInventory(testFiles, syntheticTestAllowlist)
	if len(unexpected)+len(missing)+len(miscounted) > 0 {
		t.Fatalf("control test diff not clean: unexpected=%v missing=%v miscounted=%v", unexpected, missing, miscounted)
	}

	passthrough := scanPassthrough(t, root)
	if got := passthrough["cmd/passthrough_test.go"]; got != 1 {
		t.Fatalf("control: reviewed pass-through file counted %d spreads, want 1", got)
	}
	// The three scans must stay disjoint on the corpus: a pass-through names no
	// manager, so seeing it in either manager scan would mean a count somewhere
	// is really two findings stacked.
	if _, ok := testFiles["cmd/passthrough_test.go"]; ok {
		t.Fatal("the manager exec scan matched a pass-through that names no manager")
	}
	if _, ok := passthrough["cmd/e2e_test.go"]; ok {
		t.Fatal("the pass-through scan matched an exec with no argument spread")
	}
	unexpected, missing, miscounted = diffServiceManagerInventory(passthrough, syntheticPassthroughAllowlist)
	if len(unexpected)+len(missing)+len(miscounted) > 0 {
		t.Fatalf("control pass-through diff not clean: unexpected=%v missing=%v miscounted=%v", unexpected, missing, miscounted)
	}
}

// TestPassthroughScannerFlagsANewFile is the omission half: a helper nobody
// reviewed starts forwarding caller-supplied arguments into a spawned process.
func TestPassthroughScannerFlagsANewFile(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/rogue_runner_test.go", "package cmd\n\nfunc e() {\n"+passthroughCallText("compiledTSLinkBinary(t)")+"}\n")

	unexpected, missing, miscounted := diffServiceManagerInventory(scanPassthrough(t, root), syntheticPassthroughAllowlist)
	if len(unexpected) != 1 || unexpected[0] != "cmd/rogue_runner_test.go" {
		t.Fatalf("unexpected = %v, want [cmd/rogue_runner_test.go]", unexpected)
	}
	if len(missing)+len(miscounted) != 0 {
		t.Fatalf("a new file must not disturb the other directions: missing=%v miscounted=%v", missing, miscounted)
	}
}

// TestPassthroughScannerFlagsAnExtraSpreadInsideAReviewedFile keeps a reviewed
// entry from becoming a blanket permit for the file it names.
func TestPassthroughScannerFlagsAnExtraSpreadInsideAReviewedFile(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/passthrough_test.go",
		"package cmd\n\nfunc d() {\n"+passthroughCallText("binary")+passthroughCallText("other")+"}\n")

	found := scanPassthrough(t, root)
	if got := found["cmd/passthrough_test.go"]; got != 2 {
		t.Fatalf("mutated file counted %d spreads, want 2", got)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(found, syntheticPassthroughAllowlist)
	if len(miscounted) != 1 {
		t.Fatalf("miscounted = %v, want one entry for cmd/passthrough_test.go", miscounted)
	}
	if len(unexpected)+len(missing) != 0 {
		t.Fatalf("an extra spread must show up as a count change only: unexpected=%v missing=%v", unexpected, missing)
	}
}

// TestPassthroughScannerFlagsAReviewedFileThatStoppedMatching is the scanner's
// own tripwire. Without it, a broken pattern finds nothing and every direction
// of the diff reports nothing, which reads exactly like a clean repository.
func TestPassthroughScannerFlagsAReviewedFileThatStoppedMatching(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/passthrough_test.go", "package cmd\n\nfunc d() {}\n")

	_, missing, _ := diffServiceManagerInventory(scanPassthrough(t, root), syntheticPassthroughAllowlist)
	if len(missing) != 1 || missing[0] != "cmd/passthrough_test.go" {
		t.Fatalf("missing = %v, want [cmd/passthrough_test.go]", missing)
	}
}

// TestPassthroughScannerIgnoresAFixedArgumentList guards the opposite error. A
// scan that fired on every exec in every test file would be turned off by its
// own noise within a week.
func TestPassthroughScannerIgnoresAFixedArgumentList(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/fixed_args_test.go", "package cmd\n\nfunc f() {\n"+execCallText("sleep", "30")+"}\n")

	if n, ok := scanPassthrough(t, root)["cmd/fixed_args_test.go"]; ok {
		t.Fatalf("a spawn with a fixed argument list counted %d times, want no entry", n)
	}
}

// TestInventoryScannerFlagsANewFile is the original protection: a file nobody
// reviewed starts naming a service manager.
func TestInventoryScannerFlagsANewFile(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "internal/newpkg/exit.go", "package newpkg\n\nfunc d() {\n"+literalText("systemctl")+"}\n")

	nonTest, _ := scanBoth(t, root)
	unexpected, missing, miscounted := diffServiceManagerInventory(nonTest, syntheticInventory)
	if len(unexpected) != 1 || unexpected[0] != "internal/newpkg/exit.go" {
		t.Fatalf("unexpected = %v, want [internal/newpkg/exit.go]", unexpected)
	}
	if len(missing)+len(miscounted) != 0 {
		t.Fatalf("a new file must not disturb the other directions: missing=%v miscounted=%v", missing, miscounted)
	}
}

// TestInventoryScannerFlagsAnExtraMentionInsideAReviewedFile is F3's negative:
// the file is already on the list, so a file-granular check stays green while
// a second, unwired exit sits in it.
func TestInventoryScannerFlagsAnExtraMentionInsideAReviewedFile(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/install_darwin.go",
		"package cmd\n\nfunc a() {\n"+literalText("launchctl")+"}\n\nfunc sneaky() {\n"+execCallText("launchctl", "kill")+"}\n")

	nonTest, _ := scanBoth(t, root)
	if got := nonTest["cmd/install_darwin.go"]; got != 2 {
		t.Fatalf("mutated file counted %d mentions, want 2", got)
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(nonTest, syntheticInventory)
	if len(miscounted) != 1 {
		t.Fatalf("miscounted = %v, want one entry for cmd/install_darwin.go", miscounted)
	}
	if len(unexpected)+len(missing) != 0 {
		t.Fatalf("an extra mention must show up as a count change only: unexpected=%v missing=%v", unexpected, missing)
	}
}

// TestInventoryScannerFlagsAReviewedFileThatStoppedMatching is the scanner's
// own tripwire: if the needles broke, every reviewed file goes missing rather
// than the run going quietly green.
func TestInventoryScannerFlagsAReviewedFileThatStoppedMatching(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/install_darwin.go", "package cmd\n\nfunc a() {}\n")

	nonTest, _ := scanBoth(t, root)
	_, missing, _ := diffServiceManagerInventory(nonTest, syntheticInventory)
	if len(missing) != 1 || missing[0] != "cmd/install_darwin.go" {
		t.Fatalf("missing = %v, want [cmd/install_darwin.go]", missing)
	}
}

// TestInventoryScannerFlagsAnUnreviewedTestExec is F1's commission half: a test
// file that builds its own exec.Command never touches a seam, so the guard
// cannot see it and the non-test scan skips _test.go by construction.
func TestInventoryScannerFlagsAnUnreviewedTestExec(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/rogue_test.go", "package cmd\n\nfunc e() {\n"+execCallText("launchctl", "bootout")+"}\n")

	nonTest, testFiles := scanBoth(t, root)
	if _, ok := nonTest["cmd/rogue_test.go"]; ok {
		t.Fatal("the non-test scan must not see _test.go files; that is why the test scan exists")
	}
	unexpected, missing, miscounted := diffServiceManagerInventory(testFiles, syntheticTestAllowlist)
	if len(unexpected) != 1 || unexpected[0] != "cmd/rogue_test.go" {
		t.Fatalf("unexpected = %v, want [cmd/rogue_test.go]", unexpected)
	}
	if len(missing)+len(miscounted) != 0 {
		t.Fatalf("missing=%v miscounted=%v", missing, miscounted)
	}
}

// TestInventoryScannerFlagsAnExtraExecInsideAnAllowlistedTestFile keeps the
// allowlist from turning into a blanket permit for one file.
func TestInventoryScannerFlagsAnExtraExecInsideAnAllowlistedTestFile(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/e2e_test.go",
		"package cmd\n\nfunc c() {\n"+execCallText("systemctl", "--user")+execCallText("systemctl", "stop")+"}\n")

	_, testFiles := scanBoth(t, root)
	if got := testFiles["cmd/e2e_test.go"]; got != 2 {
		t.Fatalf("mutated test file counted %d execs, want 2", got)
	}
	_, _, miscounted := diffServiceManagerInventory(testFiles, syntheticTestAllowlist)
	if len(miscounted) != 1 {
		t.Fatalf("miscounted = %v, want one entry for cmd/e2e_test.go", miscounted)
	}
}

// TestInventoryScannerTestPatternIgnoresSeamNames guards against the opposite
// error: the test scan must not fire on every mention of a manager name, or
// the wiring files that merely name their seam's manager would drown it.
func TestInventoryScannerTestPatternIgnoresSeamNames(t *testing.T) {
	root := syntheticTree(t)
	writeFile(t, root, "cmd/seam_test.go", "package cmd\n\nvar m = "+strconv.Quote("launchctl")+"\n")

	_, testFiles := scanBoth(t, root)
	if n, ok := testFiles["cmd/seam_test.go"]; ok {
		t.Fatalf("naming a manager without exec'ing it counted %d times, want no entry", n)
	}
}
