# Contributing to TSLink

Thank you for your interest in contributing to TSLink! This document provides guidelines to make the contribution process smooth for everyone.

## Documentation policy

Documentation is English-only. The only translations are the homepage READMEs in `docs/README.<lang>.md`.

## Getting Started

### Prerequisites

- Go 1.26.6 or newer. The `go` directive in [`go.mod`](./go.mod) is the source of truth for the supported minimum toolchain, and local checks should use a compatible toolchain.
- A [Tailscale account](https://tailscale.com) (free for personal use) for integration testing
- Git

### Development Setup

```bash
# Clone the repository
git clone https://github.com/anydoor7/tslink.git
cd tslink

# Run the portable local release checks
scripts/check.sh

# Refresh the committed CLI manifest after command or flag changes
go run ./tools/gen-manifest
```

`go run ./tools/gen-manifest` regenerates `docs/cli-manifest.json`
identically on macOS, Linux, and Windows. `go run ./tools/gen-manifest -check`
fails on any of them if the committed fixture is stale.

`THIRD_PARTY_NOTICES.md` also includes linked nested notices and reviewed
embedded assets. Embedded vendor bytes are SHA-256 pinned; when one changes,
review its license payload and update the notice inventory before regenerating
the file with `go run ./tools/gen-notices`.

### Test isolation

Every test binary starts in `testenv.Main` (`internal/testenv`), so a package
with tests needs a `TestMain` that calls it; `TestEveryTestPackageRunsTheSharedIsolation`
fails otherwise. Before any test runs, `testenv.Main` removes inherited
`TSLINK_*` variables, moves `HOME`, `USERPROFILE`, `APPDATA`, `LOCALAPPDATA`,
the `XDG_*` directories and `TSLINK_CONFIG_DIR` into a temporary root (the Go
build and module caches stay where they were), and exports
`TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1` so compiled children never query the
machine's `tailscaled`. Production seams that reach the host, such as real
tsnet nodes, the local Tailscale client and browser openers, have refusing
defaults in the test binaries; a test that reaches one without faking it
fails its package and names the seam. Build LocalAPI clients in tests with
`localapitest.NewClient`; `TestNoTestBuildsALocalAPIClientThatCanReachTheHost`
rejects any other form.

What this means for your machine and for the directory `TMPDIR` points at:

- **Leftover roots.** A test binary that is interrupted (Ctrl-C), killed by
  `-timeout` or crashes leaves its `tslink-testenv-*` root in `TMPDIR`; the next
  `go test` of any package in the same `TMPDIR` removes it. It removes only roots
  that your user owns and whose lock is free, so a shared `TMPDIR` such as `/tmp`
  is safe, and another user's leftovers are never touched. Roots from checkouts
  older than this mechanism (an empty marker file), and roots abandoned between
  taking their lock and writing their pid, are never removed automatically:
  delete them by hand while no TSLink test is running.
- **Locks.** `TMPDIR` must support advisory file locks (`flock` on macOS and
  Linux, `LockFileEx` on Windows). Local disks, tmpfs and NFSv4.2 do. Where
  locking fails, every test binary stops before its first test with
  `testenv: cannot isolate this test binary: lock …`.
- **NFS.** With `TMPDIR` on NFS, the `go` command's own work directory can fail
  to clean up (`unlinkat …/.nfs…: device or resource busy`), and `go` then exits
  1 after the package printed `ok`. That comes from the Go toolchain; set
  `GOTMPDIR` to a local directory. `t.TempDir()` then creates its directories in
  `GOTMPDIR`, which the tests accept as a temporary root.
- **`TSLINK_TESTENV_ROOT`** is honoured only while the test binary that created
  that root is running; exporting the path of a leftover root has no effect.
- **Go's own files.** Each test binary runs `go env -json` once with your real
  home, so it reads Go's environment and telemetry files, the same files the
  parent `go test` uses.
- **macOS sandbox.** Under `sandbox-exec`,
  `TestWriteFileInExistingDirReportsSpecialParentModeBits/setgid` skips and says
  why; unsandboxed it runs.

### Synthetic credential fixtures

Use unmistakably noncredential values in tests. When a test exercises the
API-token kind check, retain the `tskey-api-` prefix but use an angle-bracketed
suffix such as `tskey-api-<test-only-old>` and `tskey-api-<test-only-new>`.
The brackets make these literal placeholders, rather than provider-shaped
tokens. Keep distinct values for rotation, rollback and fingerprint assertions.
Use plain labels when the credential kind is irrelevant. Avoid colons in
fixtures passed as HTTP Basic Auth usernames, because colons delimit passwords.
For generic `tskey-` redaction tests, use an invented kind such as
`tskey-test-FAKE-error` to keep the contiguous token grammar under test. When
asserting a fixture is absent from JSON, check a distinctive unescaped suffix
or decoded fields: JSON escapes angle brackets, so comparing only the raw
angle-bracketed literal would miss an encoded leak.

Keep keyring, verification and transport operations mocked or loopback-only as
described above. Do not split strings to conceal a credential-shaped fixture,
exclude all test files from scanning, or disable a secret-scanning rule.

GitHub Secret Scanning alerts can remain open after a fixture is removed from
the current tree. A maintainer must verify the exact historical fixture and all
its locations before closing that individual alert as `used_in_tests`. An
unverified value must stay open; a test filename alone is not proof. See
[GitHub's alert-resolution guidance](https://docs.github.com/en/code-security/secret-scanning/managing-alerts-from-secret-scanning/resolving-alerts).

### Compiled-binary test isolation

Tests that execute a freshly compiled TSLink binary must set all three of
`TSLINK_CONFIG_DIR`, `TSLINK_DISABLE_KEYRING=1`, and
`TSLINK_TEST_DAEMON_PARENT_LIFETIME=1`. The shared helpers in
`cmd/compiled_binary_contract_test.go` already do this. The last variable is an
internal, test-only lifetime seam: a daemon launched by that process watches
the short-lived `serve --daemon` launcher and exits when the launcher exits,
including when a context cancellation kills it. The launcher supplies the
internal parent PID automatically; tests must not set it themselves.

Real `tslink share` and `tslink serve --daemon` commands do not set this seam,
so their detached daemon continues to outlive the command as designed.

The four compiled-binary end-to-end tests that start daemons find them by
listing your processes. From a process they read only its executable path
(`proc_pidpath` on macOS, `/proc/<pid>/exe` on Linux), and only when it is named
like the binary the test built (`tslink`) and started after the test binary.
They never read another process's arguments or environment, and never read a
process that was already running, such as an installed daemon; a `tslink` that
your shell prompt, editor or monitoring starts during the run may have its path
read, and nothing else. The product code under test works differently: `tslink
stop`, `status` and similar commands check the identity of the daemon named in
the pid file, and on macOS that check reads the process's arguments
(`kern.procargs2`). In these tests the pid file lives in the test's isolated
config directory, so the process checked is the test's own fake daemon.

`TSLINK_CONFIG_DIR` only isolates on-disk state — it does not isolate network
side effects or spawned processes. Commands like `share` or `add --wait` have
a product semantic of "register + bring the service up": they will still
start a `tsnet` node, contact the Tailscale control plane, and can print a
real `login.tailscale.com` authorization URL, even when pointed at a scratch
config dir. After exercising any command whose product semantics start or
listen on something, check for orphaned processes immediately (`ps`,
filtered by the scratch config dir path), rather than assuming the exit
value or a config-dir override proves there was no side effect.

### Continuous integration

`ci.yml` calls the exact-SHA Release Candidate workflow for PRs and pushes to `main`; `release.yml` calls it for tags. Main and tags always run the full three-OS gate. PRs choose a tier from the changed paths against the merge base:

| PR tier | Checks |
|---|---|
| Draft | Classification and aggregate only; heavy checks wait until ready for review. |
| Docs | Classification and aggregate only; no Go jobs. |
| Go | Proposed CI policy tests, Ubuntu native build/vet/test/race/coverage/shuffle, gofmt/tidy, staticcheck for three OSes, all six cross-builds and target vulnerability scans, Ubuntu compiled contracts, artifact verification, GoReleaser and manifest checks. |
| Full | All Go-tier checks plus macOS/Windows native and compiled contracts, strict Darwin manifest check and three-platform manifest comparison. |

Docs permits only root or `docs/` Markdown and images under `docs/assets/` that no Go source references. In merge-base, base and head, every `*.go` file (including tests and Darwin-only files) is scanned as raw Git bytes. A normalized path, basename, or case-sensitive stem substring selects full. The stem is the basename up to the first `.` or `_`: `README` for `README.ja.md`, `platforms` for `platforms.md`. Comments and partial-word matches count. Any change to a `*_zh.md` path selects full so the English-only documentation contract runs. This protects documentation contracts on every platform; Go-referenced documentation changes, including README edits, now run full. Correctness takes precedence over Actions-minute savings. Release metadata is read as raw Git bytes from every `.goreleaser*` file and `.github/workflows/release-candidate.yml`. A docs path (after removing leading `./`) or basename appearing anywhere in that text selects full, including comments. Any line with a glob character (`*`, `?`, `[`) and `docs`, `.md`, or a `files:`, `src:` or `contents:` key excludes every docs candidate. Embedded assets, executable files, `.gitattributes` and runtime/test fixtures cannot qualify as docs. Any symlink anywhere in the merge-base, base or head tree selects full. Embed directives are read as Git data with leading `./` normalized; uncertain inspection selects full. Platform/gate paths take precedence: OS-suffixed or build-tagged Go files, install/supervision/daemon code, files in an `internal/` package with OS-specific files, `go.mod`, `go.sum`, `.github/**` and `tools/**` select full. Deleted files are inspected too. Unknown paths or unavailable evidence select full. Add `ci:full` to force full on a ready PR; drafts still defer heavy checks. Draft transitions and label changes rerun classification. The tier job summary explains the decision.

The authoritative classifier runs from a separate checkout of the immutable PR base SHA with credentials persistence disabled. It reads PR Git trees as data. Tests of the proposed classifier run in a separate, non-authoritative job for Go/full tiers; any `.github/**` change selects full. If the base has no classifier yet, bootstrap selects full. Main and tags select full without executing a PR classifier.

The repository Actions variable `CI_PR_TIER_MODE` defaults to `tiered`. Set it to `full` to run the full gate for every ready PR that is not docs-only; drafts and docs-only PRs remain cheap. `ci:full` still overrides a ready docs PR. An invalid variable value emits a warning and selects full. Main and tags always remain full. **The owner will set `CI_PR_TIER_MODE=full` immediately after the repository becomes public.** Changing this variable does not configure branch protection.

The always-running aggregate job is named `gate`: required jobs must succeed, only legitimate tier skips pass, and failures/cancellations fail it. When configuring branch protection after the repository becomes public, require this single aggregate check, rather than individual matrix jobs. GitHub prefixes reusable jobs with the caller name, so verify the emitted context (`Release candidate gate / gate`) on the first hosted run before selecting it. This change does not configure branch protection. Staticcheck and cross-build targets each share one Linux job; vulnerability scanning uses two independent Linux jobs (main module and six-target repository loop), preserving per-target reports and artifact names. Each loop runs all targets and propagates any target failure.

`setup-go` explicitly caches downloaded modules and build outputs using `go.sum`. Tests deliberately use `-count=1` so cached test results cannot replace execution. Run the hermetic CI policy checks with `python3 -m unittest discover -s .github/scripts -p 'test_ci_tier.py' -v`.

 Dependabot is active. A Go dependency bump changes the module versions pinned in `THIRD_PARTY_NOTICES.md`, so the gate's `go run ./tools/gen-notices -check` fails on every Dependabot `gomod` pull request until someone runs `go run ./tools/gen-notices` and pushes the regenerated file onto that branch. CodeQL scanning is not enabled; to add it, commit a CodeQL workflow and enable code scanning for the repository. Running the local checks above before opening a pull request is still the fastest way to find a failure.

### Maintainer Release Notes

Stable releases are disabled until external readback proves the release environment, required reviewers, `refs/tags/v*` ruleset, branch protection, and Homebrew tap are configured. After that gate is enabled, stable releases publish a Homebrew cask to `anydoor7/homebrew-tap`. The release workflow requires a repository secret named `HOMEBREW_TAP_GITHUB_TOKEN` with write access to that tap; the default repository-scoped `GITHUB_TOKEN` cannot write to the separate tap repository. Prefer a fine-grained personal access token or GitHub App installation token scoped only to `anydoor7/homebrew-tap` with Contents read/write access. Use a broad classic `repo` token only as a fallback when fine-grained tokens or GitHub App credentials are not available.

GoReleaser signs `checksums.txt` and generated SBOM sidecars with keyless Sigstore bundles, then the release workflow publishes GitHub artifact attestations for installable artifacts and supply-chain sidecars. Keep the `release.yml` attestation globs aligned with `.goreleaser.yml` when adding or removing release asset types.

When a CI job fails in 0 steps within a few seconds (`steps: []` in the
check-run), do not diagnose it as if it were the same failure you can
reproduce locally — a same-named local error (e.g. a `govulncheck` exit code)
is a different evidence chain than a remote job that never ran any steps.
Read `gh api repos/<repo>/check-runs/<job>/annotations` first: an account- or
organization-level Actions gate reports as a job failure with no step output,
and every commit keeps reporting the wrong, more specific root cause until
someone reads the annotation.

### Project Structure

```
cmd/             → CLI commands (Cobra), MCP server, CLI manifest
internal/
  atomicfile/    → Permission-safe writes of local state files
  authmode/      → Credential-tier transitions applied at the next daemon start
  config/        → config.json and paths
  credentials/   → Keychain + file-based credential management
  daemon/        → Process management and daemonization
  filelock/      → Cross-platform file locks
  inspect/       → Service view and warning codes
  lifecycle/     → Reconciliation: Funnel expiry, owned-device and node-state cleanup
  logging/       → Structured logging (slog)
  logrotate/     → Size-bounded daemon log files
  manifestcheck/ → Messages of the CLI manifest check
  output/        → JSON result envelope and exit codes
  registry/      → Service registry (JSON)
  release/       → Tests that validate the release workflows (no product code)
  runtime/       → Daemon snapshot (runtime.json), node state, ownership ledger
  security/      → Capability manifest and plans for remote mutations
  server/        → tsnet server, proxy, file handler, TCP proxy
  tailapi/       → Tailscale API client
  testenv/       → Test-process isolation (every test binary starts here)
tools/           → Manifest and notices generators, repository checks
```

## How to Contribute

### Reporting Bugs

1. Check existing [issues](https://github.com/anydoor7/tslink/issues) to avoid duplicates
2. Open a new issue with the bug report template, including:
   - Steps to reproduce
   - Expected vs. actual behavior
   - Platform (macOS/Linux/Windows) and Go version
   - TSLink version (`tslink --version`)
   - Tailscale or Headscale version and control URL type
   - Sanitized logs or configuration snippets with credentials removed

### Suggesting Features

Open an issue with the feature request template. Describe:
- The problem you're trying to solve
- Your proposed solution
- Any alternatives you've considered

### Submitting Code

1. **Open an issue first** to discuss the change
2. Fork the repository and create a branch from `main`
3. Write your code following the existing patterns
4. Add or update tests as appropriate
5. Ensure the portable checks pass (add `-race` if needed):
   ```bash
   scripts/check.sh
   ```
6. Submit a pull request

### Pull Request Guidelines

- Keep PRs focused — one logical change per PR
- Write clear commit messages
- Include tests for new functionality
- Update documentation if behavior changes
- Do not mix formatting changes with behavior changes

### Documentation and Marketing Claims

Any "competitor cannot do X" claim going into the README or public docs must
be checked against that competitor's *current* official documentation before
it is written, with a URL kept alongside the claim. A claim that "feels
obviously true" is the most dangerous kind, precisely because it does not
trigger the same verification instinct as a plain factual assertion. A claim that
emerged from discussion rather than from a source is not exempt; agreement
between reviewers is not verification.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `log/slog` for logging (not `fmt.Printf`)
- Error messages should be lowercase and not end with punctuation
- Prefer returning errors over panicking

## Good First Issues

Look for issues labeled [`good first issue`](https://github.com/anydoor7/tslink/labels/good%20first%20issue) — these are specifically chosen to be approachable for new contributors.

## License

Submit only material you have the right to contribute, including any required
employer permission, and identify third-party material and its license.
Unless explicitly stated otherwise, contributions intentionally submitted
for inclusion in TSLink are offered under [Apache License 2.0](./LICENSE),
as described in its section 5. Contributors retain their copyright; no
copyright assignment or additional contributor agreement is required by
this policy. Separately agreed contribution terms remain effective.
The maintainers must verify the rights and license compatibility of
contributed material before merging it.
