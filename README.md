<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">Private Tailscale gateway for local services.<br>Give each HTTP, file, or TCP service its own tailnet identity with one command.</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/actions"><img src="https://img.shields.io/github/actions/workflow/status/monody0007/tslink/ci.yml?branch=main&label=CI" alt="Build Status"></a>
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.26.3%2B-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README_zh.md">中文文档</a> ·
  <a href="https://github.com/monody0007/tslink">GitHub</a>
</p>

<!-- TODO: Add terminal recording / GIF demo here -->
<!-- <p align="center"><img src="docs/demo.gif" alt="TSLink Demo" width="700"></p> -->

---

## Why TSLink?

Traditional approaches to exposing local services — port forwarding, VPNs, ngrok, Cloudflare Tunnel — were not designed for a zero-trust world. They either expose your services to the public internet, route private data through third-party servers, or require significant operational overhead.

As local AI workloads, self-hosted services, and personal infrastructure grow, the gap between what individuals need and what enterprise security tools provide keeps widening. The federal government recognized this shift: [Executive Order 14028](https://www.whitehouse.gov/briefing-room/presidential-actions/2021/05/12/executive-order-on-improving-the-nations-cybersecurity/) mandates zero-trust adoption, and [NIST SP 800-207](https://csrc.nist.gov/publications/detail/sp/800-207/final) defines the architecture. But most zero-trust tooling targets large enterprises with dedicated security teams.

**TSLink brings zero-trust networking to everyone.** One command turns your machine into a secure gateway. Each service gets its own isolated identity on your [Tailscale](https://tailscale.com) network — encrypted, authenticated, and not publicly reachable by default.

## Security Model

TSLink implements zero-trust principles at every layer:

| Zero-Trust Principle | TSLink Implementation |
|-----|-----|
| **Never trust, always verify** | Tailnet HTTP proxy/file requests are authenticated via Tailscale WhoIs — identity headers (`X-Tailscale-User-Login`, `X-Tailscale-User-Name`, `X-Tailscale-User-Picture`, `X-Tailscale-Node`) are injected into proxied requests. Public Funnel exposure and raw TCP streams are not treated as TSLink-enforced Tailscale user authentication. |
| **HTTP least-privilege access** | `--allow` restricts proxy and file services to specific users or tags. TCP services rely on Tailscale network ACLs and tags. |
| **Assume breach** | Tailnet device-to-device traffic uses WireGuard encryption. Even if your local network is compromised, traffic between your Tailscale devices remains encrypted; public Funnel paths follow Tailscale Funnel semantics. |
| **Microsegmentation** | Each service runs as an isolated tsnet node with its own hostname, TLS certificate, and network identity. Compromising one service does not grant access to others. |
| **No implicit trust** | No services are exposed to the public internet by default. Credentials are stored in the system keychain first, with restricted-permission file fallback for headless environments. API-token-derived startup auth keys are generated on demand and not persisted; legacy authkey files may still be read for compatibility and should be migrated. |

## What TSLink Does

One command exposes any local service — a web app, an API, a file directory, a database — to your private Tailscale network with automatic TLS.

```bash
tslink add myapp --proxy localhost:3000
tslink serve --daemon
# → https://myapp.<your-tailnet>.ts.net — accessible from any device on your tailnet
```

### Features

- **Zero configuration** — no port forwarding, no DNS, no certificates to manage
- **WireGuard tailnet path** — Tailnet device-to-device traffic uses WireGuard via Tailscale; public exposure requires explicit Funnel opt-in
- **Instant TLS** — automatic HTTPS with valid certificates, no setup required
- **Per-service isolation** — each service gets its own tailnet hostname and identity (`https://<name>.<tailnet>.ts.net`)
- **Live reload** — add or remove services while TSLink is running, changes take effect immediately
- **Cross-platform** — runs on macOS, Linux, and Windows
- **Runs as a daemon** — start once, runs in the background, auto-starts on login
- **TCP proxy** — expose databases, SSH, Redis, and other non-HTTP services
- **HTTP access control** — `--allow user@example.com,tag:admin` for proxy and file services
- **Safety diagnostics** — `tslink doctor`, `tslink status --urls`, and `tslink access explain` make local evidence and unknown external policy layers explicit
- **Local API mode** — JSON-over-stdin/stdout for local automation across list/add/remove/status, diagnostics, access explanation, and templates
- **Personal templates** — preview and apply small private service suites without overwriting existing services
- **Headscale compatibility path** — advanced/self-hosted control-server use via `--control-url`
- **Funnel guardrails** — public internet exposure is opt-in and requires explicit `--public` acknowledgement

### Launch status

| Shipped now | Roadmap / experimental |
|---|---|
| Proxy, file, and raw TCP services | Roadmap/experimental middleware pipeline (rate limit, Basic Auth, IP allow list, CORS) |
| One embedded `tsnet` node per service | Roadmap/experimental Docker label auto-discovery |
| Identity-aware HTTP proxy headers | Roadmap/experimental admin dashboard or REST surface |
| HTTP `--allow` for proxy/file services | Roadmap/experimental Prometheus `/metrics` endpoint |
| Registry-backed hot reload | Roadmap/experimental custom domain / ACME runtime TLS |
| Daemon lifecycle and autostart | Roadmap/experimental cluster / multi-node registry sync |
| Owner-only `status --urls`, `doctor`, and `access explain` | Roadmap/experimental member-facing portal or service directory |
| Local JSON `tslink api` parity for shipped owner workflows | Roadmap/experimental remote API, dashboard, or multi-user admin plane |
| Built-in personal templates | Roadmap/experimental marketplace or third-party template registry |

## Quick Start

### Install

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# From source (any platform)
go install github.com/monody0007/tslink@latest
```

### Release Artifacts

GitHub Releases publish these installable artifacts:

| Platform | Artifacts | Notes |
|---|---|---|
| macOS | Homebrew formula and `tar.gz` archives | The Homebrew formula uses GoReleaser `skip_upload: auto`, so pre-release tags can skip tap upload without failing the release. Use the archives for pre-release validation. |
| Linux | `.deb`, `.rpm`, and `tar.gz` archives | Packages contain the native `tslink` binary. Use `tslink install` after installation to register the user service. |
| Windows | `.zip` archives | Windows support is archive-only today. There is no MSI/MSIX/Winget package or Windows code-signed installer yet. Use `tslink install` from the extracted binary to register Startup autostart. |

Release assets are side-by-side files, not files embedded inside the archives. GoReleaser uploads installable archives/packages, `checksums.txt`, CycloneDX SBOM sidecars for archives, and keyless Sigstore bundle signatures for `checksums.txt` and SBOM sidecars. The signed `checksums.txt` covers both installable artifacts and SBOM sidecars. The release workflow also publishes GitHub artifact attestations for the installable artifacts and supply-chain sidecars.

### Verify Release Integrity

These commands require `gh` 2.49 or newer with `gh attestation verify`, `cosign` with `verify-blob --bundle` support, and either `sha256sum` or `shasum`. Use a tag such as `<version>` and an asset name such as `<artifact>` from the GitHub Release.

The Sigstore certificate trust root is the GitHub Actions OIDC issuer `https://token.actions.githubusercontent.com`. Verification pins the exact release workflow identity `https://github.com/monody0007/tslink/.github/workflows/release.yml@refs/tags/<version>` and the GitHub attestation signer workflow `github.com/monody0007/tslink/.github/workflows/release.yml`. The tag ref binding means a matching signature or attestation must come from this repository's release workflow for the requested tag.

```bash
set -euo pipefail

repo="monody0007/tslink"
version="<version>"
artifact="<artifact>"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

mkdir -p "tslink-$version-verify"
cd "tslink-$version-verify"

gh release download "$version" --repo "$repo" \
  --pattern "$artifact" \
  --pattern "checksums.txt" \
  --pattern "checksums.txt.sigstore.json"

require_file "$artifact"
require_file "checksums.txt"
require_file "checksums.txt.sigstore.json"
verify_checksum "$artifact"

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$artifact" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

Archive SBOM sidecars are verified separately because they are independent release assets. Run this from the same verification directory after the archive check, using the same `version` and `artifact`.

```bash
set -euo pipefail

repo="monody0007/tslink"
version="<version>"
artifact="<artifact>"
sbom="$artifact.sbom.json"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

gh release download "$version" --repo "$repo" \
  --pattern "$sbom" \
  --pattern "$sbom.sigstore.json"

require_file "$sbom"
require_file "$sbom.sigstore.json"
verify_checksum "$sbom"

cosign verify-blob "$sbom" \
  --bundle "$sbom.sigstore.json" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$sbom" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

### Get Started in 30 Seconds

```bash
# 1. Authenticate with Tailscale (choose API access token or OAuth client secret)
tslink login

# 2. Expose a local web service
tslink add myapp --proxy localhost:3000

# 3. Start the gateway
tslink serve --daemon

# Access https://myapp.<your-tailnet>.ts.net from any device
```

TSLink accepts two credential types (you only need one):

- **API access token** (`tskey-api-*`) — generate at [Admin → Keys](https://login.tailscale.com/admin/settings/keys). Use this for the most complete automation today, including tag and device management through the Tailscale API. It expires periodically.
- **OAuth client secret** (`tskey-client-*`) — generate at [Admin → OAuth](https://login.tailscale.com/admin/settings/oauth). It does not expire, but TSLink's current Tailscale tag/device automation is narrower in this mode because those operations use the Tailscale REST API. Use it only after validating your required tag/device operations.

`tslink login` guides you through either path interactively. Credentials are stored in the system keychain first (macOS Keychain / Linux secret service / Windows Credential Manager), with restricted-permission file fallback for headless environments.

For non-interactive setup, prefer environment variables or stdin so secrets do not land in shell history or process listings:

```bash
TSLINK_API_KEY="tskey-api-..." tslink login
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
TSLINK_CLIENT_SECRET="tskey-client-..." tslink login
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
```

The compatible `--api-key` and `--client-secret` flags remain available, but command-line arguments can be visible to other local processes.

### Tag Auto-Management

TSLink automatically manages Tailscale ACL tags for your services:

- **Default tag** — every service gets `tag:tsmain` applied automatically when `--tags` is not specified.
- **API access token tag automation** — with an API access token, startup can ensure registry tags exist before nodes start. `tslink tags pull` also fetches remote ACL tags only in API access token mode; OAuth-only mode skips the remote read and reports that an API access token is required.
- **Strict tag grammar** — tags must match `tag:<lowercase-hyphen-name>` with lowercase letters, numbers, and hyphens. Migrate legacy tags such as `tag:Web`, `tag:db_main`, or `web` with `tslink tags set <service> tag:<lowercase-hyphen-name>` or by editing `registry.json`. Invalid legacy tags fail `tslink serve` validation and must be fixed before the gateway starts.
- **Runtime auth refresh** — tag, ephemeral, and effective control-server URL changes restart affected nodes with fresh per-service auth material. Restart `tslink serve` after credential mode swaps or legacy `authkey` file changes.

Use `tslink tags` to inspect and customize tag assignments:

```bash
# See all services and their tags
tslink tags list

# Fetch tags currently defined in your Tailscale ACL (requires API access token; skipped in OAuth-only mode)
tslink tags pull

# Add a tag to a specific service (node restarts automatically)
tslink tags add myapp tag:production

# Replace all tags on a service
tslink tags set myapp tag:webserver

# Change the default tag applied to new services
tslink tags set-default tag:myteam

# Remove an ACL tag owner rule globally after local safety checks
tslink tags delete-remote tag:old-tag --force
```

### More Examples

```bash
# Expose a file directory
tslink add documents --dir ~/Documents

# Expose a database via TCP proxy
tslink add mydb --tcp localhost:5432

# Ephemeral node (auto-removed from tailnet when stopped)
tslink add demo --proxy localhost:8080 --ephemeral

# Identity-aware HTTP access control (proxy/file only)
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# Public exposure via Tailscale Funnel (requires explicit acknowledgement)
tslink add public --proxy localhost:3000 --funnel --public

# Migration note: existing Funnel entries created before public_ack was added
# must be re-added with --public or edited to include "public_ack": true.

# ACL tags for Tailscale network policy
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

## Commands

| Command | Description |
|---------|-------------|
| `tslink login` | Authenticate with Tailscale using either an API access token or an OAuth client secret |
| `tslink logout` | Clear credentials from keychain and files |
| `tslink add <name> --proxy host:port` | Expose a local web service |
| `tslink add <name> --dir /path` | Expose a file directory |
| `tslink add <name> --tcp host:port` | Expose a raw TCP service (databases, SSH, etc.) |
| `tslink remove <name>` | Remove a service (+ ownership-safe remote cleanup attempt) |
| `tslink list` | List all registered services |
| `tslink serve` | Start the gateway (foreground) |
| `tslink serve --daemon` | Start the gateway (background) |
| `tslink stop` | Stop the gateway |
| `tslink status` | Show gateway status |
| `tslink status --urls` | Show owner-only service URLs, exposure mode, allow summary, backend, and warning codes |
| `tslink doctor` | Diagnose credentials, daemon, registry, runtime snapshot, exposure, and target safety without mutating state |
| `tslink access explain <service>` | Explain what TSLink knows locally about one service's access path and what remains external policy/backend auth |
| `tslink logs` | Show recent gateway logs |
| `tslink template list` | List built-in personal service templates |
| `tslink template show <name>` | Preview a built-in template |
| `tslink template apply <name> --yes` | Add missing template services without overwriting existing services; omit `--yes` or pass `--dry-run` to preview |
| `tslink tags list` | List services and their assigned tags |
| `tslink tags pull` | Fetch remote tags from Tailscale ACL with an API access token; skipped in OAuth-only mode |
| `tslink tags add <service> <tag>` | Append a tag to a service |
| `tslink tags set <service> <tag>` | Replace a service's tags |
| `tslink tags set-default <tag>` | Change the default tag applied to new services |
| `tslink tags delete-remote <tag> --force` | Remove an ACL tag owner rule globally from Tailscale ACL after local safety checks |
| `tslink api` | JSON-over-stdin/stdout mode for programmatic control |
| `tslink config` | Manage global configuration (set/get/list) |
| `tslink install` | Auto-start on login (macOS LaunchAgent / Linux systemd / Windows Startup) |
| `tslink uninstall` | Remove auto-start |

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General runtime error |
| `2` | Usage, argument, or flag error |
| `3` | Authentication error |
| `4` | Conflict, such as an already-running daemon |
| `5` | Requested resource not found |
| `64` | Diagnostic warning threshold |
| `65` | Diagnostic critical threshold |

### Add Command Flags

| Flag | Description |
|------|-------------|
| `--proxy host:port` | Reverse proxy to a local HTTP service |
| `--dir /path` | Serve a local file directory |
| `--tcp host:port` | Raw TCP forwarding |
| `--ephemeral` | Ephemeral node, auto-removed from tailnet when stopped |
| `--tags tag:a,tag:b` | ACL tags for Tailscale network policy |
| `--allow user@,tag:x` | HTTP access control for proxy/file services; rejected for TCP because raw TCP uses Tailscale ACL tags and target-service auth |
| `--control-url URL` | Per-service control server override, e.g. Headscale |
| `--funnel` | Expose via Tailscale Funnel (public internet, proxy only, requires `--public`) |
| `--public` | Explicitly acknowledge public internet exposure for `--funnel`; invalid without `--funnel` |
| `--domain example.com` | Roadmap/experimental: accepted in service config, but custom-domain runtime TLS is not wired |
| `--acme-email user@example.com` | Roadmap/experimental: stored with `--domain`; no shipped ACME listener |

## How It Works

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│ Your Machine│         │   Tailscale Network  │         │  Your Phone  │
│             │         │   (WireGuard mesh)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  Browser     │
│  :3000      │  tsnet  │  WireGuard encrypted  │  HTTPS │              │
│  :5432      │  nodes  │  Encrypted tailnet path│  +TLS  │              │
│  ~/Documents│  (1/svc)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink creates a dedicated [tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service — no Tailscale client installation required on the server side. Each service joins your tailnet as its own device (e.g., `myapp`, `docs`, `mydb`), obtains automatic TLS certificates, and proxies requests to your local services.

**Key architectural decisions:**
- **Per-service embedded nodes** — each service gets its own tailnet identity, hostname, and TLS certificate (microsegmentation)
- **Identity-aware proxying** — WhoIs verification on tailnet HTTP proxy/file requests, with identity headers injected and spoofing prevented; public Funnel and raw TCP do not get TSLink-enforced HTTP identity
- **Secure credential management** — system keychain storage with restricted-permission file fallback for headless environments
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — daemon management with process identity checks and platform-specific stop behavior
- **Structured logging** — slog-based structured logging with access logs
- **Metrics instrumentation** — request metrics are collected internally; a public `/metrics` endpoint is roadmap

## API Mode

TSLink includes a local JSON-over-stdin/stdout API mode for owner-side automation. It is not a REST server, dashboard, or member-facing service directory. Unknown JSON fields are rejected, and public API responses use redacted views rather than raw registry records.

```bash
# List services
echo '{"action":"list"}' | tslink api

# Add a service
echo '{"action":"add","name":"myapp","type":"proxy","target":"localhost:3000"}' | tslink api

# Remove a service
echo '{"action":"remove","name":"myapp"}' | tslink api

# Check status
echo '{"action":"status"}' | tslink api

# Show owner-only endpoint/exposure overview
echo '{"action":"status","urls":true}' | tslink api

# Run read-only diagnostics
echo '{"action":"doctor","probe_external":false}' | tslink api

# Explain one service's local access model
echo '{"action":"access_explain","name":"myapp"}' | tslink api

# Preview/apply built-in templates
echo '{"action":"template_list"}' | tslink api
echo '{"action":"template_plan","name":"personal-harness"}' | tslink api
echo '{"action":"template_apply","name":"personal-harness"}' | tslink api
```

Both `tslink api` and CLI `--json` output use the same versioned envelope:

```json
{
  "ok": true,
  "schema_version": 1,
  "code": 0,
  "data": {
    "running": false,
    "count": 0
  }
}
```

Failures include a stable machine error code plus human text:

```json
{
  "ok": false,
  "schema_version": 1,
  "code": 2,
  "error": {
    "code": "usage_error",
    "message": "unknown action: explode"
  }
}
```

API `add` follows the same safety guardrails as the CLI. Funnel services require `public_ack:true`; TCP services reject `allow` because TSLink does not apply HTTP identity checks to raw TCP streams.

## Roadmap / Experimental Packages

The repository contains packages and registry fields for features that are not wired into the shipped `tslink serve` runtime yet. Treat these as roadmap or experimental until end-to-end integration tests are added:

| Area | Current status |
|---|---|
| Docker labels | Package exists, but `serve` does not start Docker discovery. |
| Middleware | Package and schema exist, but runtime does not apply rate limit, Basic Auth, IP allow list, or CORS. |
| Admin dashboard / REST API | No default-build package, REST handler, or admin node is shipped. Future work must be explicitly experimental and tested end to end. |
| Prometheus `/metrics` | Instrumentation exists, but no scrape endpoint is mounted. |
| Custom domain / ACME | Fields are accepted, but runtime TLS/ACME listener is not wired. |
| Cluster sync | Package exists without production transport or `serve` integration. |

## Prerequisites

- [Tailscale account](https://tailscale.com) (free for personal use)
- Tailscale installed on the devices you want to access from (phone, tablet, etc.)
- Go 1.26.3+ (if building from source)

## Platform Support

| Platform | Daemon | Auto-start | Stop behavior |
|----------|--------|------------|---------------|
| macOS | `--daemon` | LaunchAgent | Graceful SIGTERM |
| Linux | `--daemon` | systemd user service | Graceful SIGTERM |
| Windows | `--daemon` | Startup folder | Forced process termination |

macOS LaunchAgent installs use launchd `KeepAlive` with `ThrottleInterval=30`. If `tslink stop` is run while the LaunchAgent remains installed, launchd will restart TSLink. Run `tslink uninstall` before `tslink stop` when the intent is to disable autostart. During SSH/headless macOS installs, `tslink install` first tries `gui/$(id -u)` and falls back to `user/$(id -u)` if the GUI launchd domain is unavailable. Linux headless user services may need `loginctl enable-linger "$USER"` to keep running after logout; if lingering was enabled only for TSLink, run `loginctl disable-linger "$USER"` after uninstall.

## Roadmap

- [x] OAuth client secret accepted by login and tsnet auth paths; validate tag/device automation before unattended use
- [ ] Runtime custom-domain / ACME TLS
- [ ] Web dashboard accessible from tailnet
- [ ] Docker image and Docker label discovery
- [ ] Headscale end-to-end testing
- [x] Local API parity for shipped owner workflows
- [ ] Integration-tested Layer 2 modules and optional remote/admin surfaces

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## Security

For security concerns, please see [SECURITY.md](./SECURITY.md).

## License

This project is licensed under the [Apache License 2.0](./LICENSE).

```
Copyright 2026 Maintainer (monody0007)
```
