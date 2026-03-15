# Contributing to TSLink

Thank you for your interest in contributing to TSLink! This document provides guidelines to make the contribution process smooth for everyone.

## Getting Started

### Prerequisites

- Go 1.25+
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

# Install locally
go install .
```

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
2. Open a new issue with:
   - Steps to reproduce
   - Expected vs. actual behavior
   - Platform (macOS/Linux/Windows) and Go version
   - TSLink version (`tslink --version`)

### Suggesting Features

Open an issue with the `enhancement` label. Describe:
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
