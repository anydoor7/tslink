# Contributing to TSLink

Thank you for your interest in contributing to TSLink! This document provides guidelines to make the contribution process smooth for everyone.

## Getting Started

### Prerequisites

- Go 1.26.5 or newer. The `go` directive in [`go.mod`](./go.mod) is the source of truth for the supported minimum toolchain, and CI uses that file through `actions/setup-go`.
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

# Optional local mirrors of CI release gates
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
go run github.com/goreleaser/goreleaser/v2@v2.17.0 check

# Install locally
go install .
```

### Maintainer Release Notes

Stable releases are disabled until external readback proves the release environment, required reviewers, `refs/tags/v*` ruleset, branch protection, and Homebrew tap are configured. After that gate is enabled, stable releases publish a Homebrew cask to `monody0007/homebrew-tap`. The release workflow requires a repository secret named `HOMEBREW_TAP_GITHUB_TOKEN` with write access to that tap; the default repository-scoped `GITHUB_TOKEN` cannot write to the separate tap repository. Prefer a fine-grained personal access token or GitHub App installation token scoped only to `monody0007/homebrew-tap` with Contents read/write access. Use a broad classic `repo` token only as a fallback when fine-grained tokens or GitHub App credentials are not available.

GoReleaser signs `checksums.txt` and generated SBOM sidecars with keyless Sigstore bundles, then the release workflow publishes GitHub artifact attestations for installable artifacts and supply-chain sidecars. Keep the `release.yml` attestation globs aligned with `.goreleaser.yml` when adding or removing release asset types.

### Project Structure

```
cmd/           → CLI commands (Cobra)
internal/
  config/      → Configuration and paths
  credentials/ → Keychain + file-based credential management
  daemon/      → Process management and daemonization
  docker/      → Docker container auto-discovery
  logging/     → Structured logging (slog)
  metrics/     → Prometheus metrics
  middleware/  → Rate limiting, auth, IP allowlist, CORS
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

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use `log/slog` for logging (not `fmt.Printf`)
- Error messages should be lowercase and not end with punctuation
- Prefer returning errors over panicking

## Good First Issues

Look for issues labeled [`good first issue`](https://github.com/monody0007/tslink/labels/good%20first%20issue) — these are specifically chosen to be approachable for new contributors.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](./LICENSE).
