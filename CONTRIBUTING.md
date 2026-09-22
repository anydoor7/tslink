# Contributing to TSLink

Thank you for your interest in contributing to TSLink! This document provides guidelines to make the contribution process smooth for everyone.

## Getting Started

### Prerequisites

- Go 1.26.6 or newer. The `go` directive in [`go.mod`](./go.mod) is the source of truth for the supported minimum toolchain, and local checks should use a compatible toolchain.
- A [Tailscale account](https://tailscale.com) (free for personal use) for integration testing
- Git

### Development Setup

```bash
# Clone the repository
git clone https://github.com/monody0007/tslink.git
cd tslink

# Build
go build ./...

# Run tests
go test ./...

# Static analysis
go vet ./...

# Optional local release checks
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...   # pinned to match CI (STATICCHECK_VERSION in .github/workflows/release-candidate.yml)
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/goreleaser/goreleaser/v2@v2.17.0 check

# Install locally
go install .
```

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

`ci.yml` runs the reusable Release Candidate gate on every pull request and on every push to `main`; `release.yml` runs the same gate on tags. Dependabot is active. CodeQL scanning is not enabled; to add it, commit a CodeQL workflow and enable code scanning for the repository. Running the local checks above before opening a pull request is still the fastest way to find a failure.

### Maintainer Release Notes

Stable releases are disabled until external readback proves the release environment, required reviewers, `refs/tags/v*` ruleset, branch protection, and Homebrew tap are configured. After that gate is enabled, stable releases publish a Homebrew cask to `monody0007/homebrew-tap`. The release workflow requires a repository secret named `HOMEBREW_TAP_GITHUB_TOKEN` with write access to that tap; the default repository-scoped `GITHUB_TOKEN` cannot write to the separate tap repository. Prefer a fine-grained personal access token or GitHub App installation token scoped only to `monody0007/homebrew-tap` with Contents read/write access. Use a broad classic `repo` token only as a fallback when fine-grained tokens or GitHub App credentials are not available.

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
cmd/           → CLI commands (Cobra)
internal/
  config/      → Configuration and paths
  credentials/ → Keychain + file-based credential management
  daemon/      → Process management and daemonization
  logging/     → Structured logging (slog)
  metrics/     → Internal request instrumentation (no scrape endpoint)
  registry/    → Service registry (JSON)
  server/      → tsnet server, proxy, file handler, TCP proxy
  tailapi/     → Tailscale API client
```

## How to Contribute

### Reporting Bugs

1. Check existing [issues](https://github.com/monody0007/tslink/issues) to avoid duplicates
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
5. Ensure all checks pass:
   ```bash
   go build ./...
   go vet ./...
   go test ./...
   go test ./... -race -coverprofile=coverage.out
   go tool cover -func=coverage.out
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

Look for issues labeled [`good first issue`](https://github.com/monody0007/tslink/labels/good%20first%20issue) — these are specifically chosen to be approachable for new contributors.

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
