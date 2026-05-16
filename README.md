<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">Private Tailscale gateway for local services.<br>Give each HTTP, file, or TCP service its own tailnet identity with one command.</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/actions"><img src="https://img.shields.io/github/actions/workflow/status/monody0007/tslink/ci.yml?branch=main&label=CI" alt="Build Status"></a>
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go"></a>
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

**TSLink brings zero-trust networking to everyone.** One command turns your machine into a secure gateway. Each service gets its own isolated identity on your [Tailscale](https://tailscale.com) network — encrypted, authenticated, and invisible to the public internet.

## Security Model

TSLink implements zero-trust principles at every layer:

| Zero-Trust Principle | TSLink Implementation |
|-----|-----|
| **Never trust, always verify** | Every request is authenticated via Tailscale WhoIs — identity headers (`X-Tailscale-User-Login`, `X-Tailscale-User-Name`) are injected into every proxied request. Inbound identity headers are stripped to prevent spoofing. |
| **HTTP least-privilege access** | `--allow` restricts proxy and file services to specific users or tags. TCP services rely on Tailscale network ACLs and tags. |
| **Assume breach** | End-to-end WireGuard encryption on every connection. Even if your local network is compromised, traffic between your devices remains encrypted. |
| **Microsegmentation** | Each service runs as an isolated tsnet node with its own hostname, TLS certificate, and network identity. Compromising one service does not grant access to others. |
| **No implicit trust** | No services are exposed to the public internet by default. Credentials are stored in the system keychain (macOS Keychain / Linux secret service), never in plaintext config files. Auth keys are derived dynamically and never persisted. |

## What TSLink Does

One command exposes any local service — a web app, an API, a file directory, a database — to your private Tailscale network with automatic TLS.

```bash
tslink add myapp --proxy localhost:3000
tslink serve --daemon
# → https://myapp.<your-tailnet>.ts.net — accessible from any device on your tailnet
```

### Features

- **Zero configuration** — no port forwarding, no DNS, no certificates to manage
- **End-to-end encrypted** — WireGuard encryption via Tailscale, your data never touches the public internet
- **Instant TLS** — automatic HTTPS with valid certificates, no setup required
- **Per-service isolation** — each service gets its own tailnet hostname and identity (`https://<name>.<tailnet>.ts.net`)
- **Live reload** — add or remove services while TSLink is running, changes take effect immediately
- **Cross-platform** — runs on macOS, Linux, and Windows
- **Runs as a daemon** — start once, runs in the background, auto-starts on login
- **TCP proxy** — expose databases, SSH, Redis, and other non-HTTP services
- **HTTP access control** — `--allow user@example.com,tag:admin` for proxy and file services
- **Minimal API mode** — JSON-over-stdin/stdout for list/add/remove/status automation
- **Headscale compatible** — works with self-hosted control servers via `--control-url`
- **Funnel** — optionally expose services to the public internet via Tailscale Funnel

### Launch status

| Shipped now | Roadmap / experimental |
|---|---|
| Proxy, file, and raw TCP services | Roadmap/experimental middleware pipeline (rate limit, Basic Auth, IP allow list, CORS) |
| One embedded `tsnet` node per service | Roadmap/experimental Docker label auto-discovery |
| Identity-aware HTTP proxy headers | Roadmap/experimental admin dashboard and REST API |
| HTTP `--allow` for proxy/file services | Roadmap/experimental Prometheus `/metrics` endpoint |
| Registry-backed hot reload | Roadmap/experimental custom domain / ACME runtime TLS |
| Daemon lifecycle and autostart | Roadmap/experimental cluster / multi-node registry sync |
| Minimal local `tslink api` | Roadmap/experimental full API parity with `tslink add` flags |

## Quick Start

### Install

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# From source (any platform)
go install github.com/monody0007/tslink@latest
```

### Get Started in 30 Seconds

```bash
# 1. Authenticate with Tailscale (browser login + API key)
tslink login

# 2. Expose a local web service
tslink add myapp --proxy localhost:3000

# 3. Start the gateway
tslink serve --daemon

# Access https://myapp.<your-tailnet>.ts.net from any device
```

TSLink accepts two credential types (you only need one):

- **API access token** (`tskey-api-*`) — generate at [Admin → Keys](https://login.tailscale.com/admin/settings/keys). Use this for the most complete automation today, including tag and device management through the Tailscale API. It expires periodically.
- **OAuth client secret** (`tskey-client-*`) — generate at [Admin → OAuth](https://login.tailscale.com/admin/settings/oauth). It does not expire, but TSLink's current REST API automation paths are narrower in this mode. Use it only after validating your required tag/device operations.

`tslink login` guides you through either path interactively. Credentials are stored in the system keychain (macOS Keychain / Linux secret service / Windows Credential Manager), never in plaintext.

### Tag Auto-Management

TSLink automatically manages Tailscale ACL tags for your services:

- **Default tag** — every service gets `tag:tsmain` applied automatically when `--tags` is not specified.
- **API-key tag automation** — with an API access token, startup can ensure registry tags exist before nodes start.
- **Strict tag grammar** — tags must match `tag:<lowercase-hyphen-name>` with lowercase letters, numbers, and hyphens. Migrate legacy tags such as `tag:Web`, `tag:db_main`, or `web` with `tslink tags set <service> tag:<lowercase-hyphen-name>` or by editing `registry.json`.
- **Runtime auth refresh** — tag, ephemeral, and effective control-server URL changes restart affected nodes with fresh per-service auth material. Restart `tslink serve` after credential mode swaps or legacy `authkey` file changes.

Use `tslink tags` to inspect and customize tag assignments:

```bash
# See all services and their tags
tslink tags list

# Fetch tags currently defined in your Tailscale ACL
tslink tags pull

# Add a tag to a specific service (node restarts automatically)
tslink tags add myapp tag:production

# Replace all tags on a service
tslink tags set myapp tag:webserver

# Change the default tag applied to new services
tslink tags set-default tag:myteam

# Remove a tag from Tailscale ACL
tslink tags delete-remote tag:old-tag
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

# Public exposure via Tailscale Funnel
tslink add public --proxy localhost:3000 --funnel

# ACL tags for Tailscale network policy
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

## Commands

| Command | Description |
|---------|-------------|
| `tslink login` | Authenticate with Tailscale (OAuth + API key) |
| `tslink logout` | Clear credentials from keychain and files |
| `tslink add <name> --proxy host:port` | Expose a local web service |
| `tslink add <name> --dir /path` | Expose a file directory |
| `tslink add <name> --tcp host:port` | Expose a raw TCP service (databases, SSH, etc.) |
| `tslink remove <name>` | Remove a service (+ auto-delete tailnet device) |
| `tslink list` | List all registered services |
| `tslink serve` | Start the gateway (foreground) |
| `tslink serve --daemon` | Start the gateway (background) |
| `tslink stop` | Stop the gateway |
| `tslink status` | Show gateway status |
| `tslink tags list` | List services and their assigned tags |
| `tslink tags pull` | Fetch remote tags from Tailscale ACL |
| `tslink tags add <service> <tag>` | Append a tag to a service |
| `tslink tags set <service> <tag>` | Replace a service's tags |
| `tslink tags set-default <tag>` | Change the default tag applied to new services |
| `tslink tags delete-remote <tag>` | Delete a tag from Tailscale ACL |
| `tslink api` | JSON-over-stdin/stdout mode for programmatic control |
| `tslink config` | Manage global configuration (set/get/list) |
| `tslink install` | Auto-start on login (macOS LaunchAgent / Linux systemd / Windows Startup) |
| `tslink uninstall` | Remove auto-start |

### Add Command Flags

| Flag | Description |
|------|-------------|
| `--proxy host:port` | Reverse proxy to a local HTTP service |
| `--dir /path` | Serve a local file directory |
| `--tcp host:port` | Raw TCP forwarding |
| `--ephemeral` | Ephemeral node, auto-removed from tailnet when stopped |
| `--tags tag:a,tag:b` | ACL tags for Tailscale network policy |
| `--allow user@,tag:x` | HTTP access control for proxy/file services; TCP ignores this HTTP ACL |
| `--funnel` | Expose via Tailscale Funnel (public internet, proxy only) |
| `--domain example.com` | Roadmap/experimental: accepted in service config, but custom-domain runtime TLS is not wired |
| `--acme-email user@example.com` | Roadmap/experimental: stored with `--domain`; no shipped ACME listener |

## How It Works

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│ Your Machine│         │   Tailscale Network  │         │  Your Phone  │
│             │         │   (WireGuard mesh)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  Browser     │
│  :3000      │  tsnet  │  End-to-end encrypted │  HTTPS │              │
│  :5432      │  nodes  │  No public internet   │  +TLS  │              │
│  ~/Documents│  (1/svc)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink creates a dedicated [tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service — no Tailscale client installation required on the server side. Each service joins your tailnet as its own device (e.g., `myapp`, `docs`, `mydb`), obtains automatic TLS certificates, and proxies requests to your local services.

**Key architectural decisions:**
- **Per-service embedded nodes** — each service gets its own tailnet identity, hostname, and TLS certificate (microsegmentation)
- **Identity-aware proxying** — WhoIs verification on every request, with identity headers injected and spoofing prevented
- **Secure credential management** — system keychain storage with file fallback for headless environments
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — clean daemon management with signal handling
- **Structured logging** — slog-based structured logging with access logs
- **Metrics instrumentation** — request metrics are collected internally; a public `/metrics` endpoint is roadmap

## API Mode

TSLink includes a minimal JSON-over-stdin/stdout API mode for local automation. It currently supports basic `list`, `add`, `remove`, and `status` actions; it is not yet full parity with every `tslink add` flag.

```bash
# List services
echo '{"action":"list"}' | tslink api

# Add a service
echo '{"action":"add","name":"myapp","type":"proxy","target":"localhost:3000"}' | tslink api

# Remove a service
echo '{"action":"remove","name":"myapp"}' | tslink api

# Check status
echo '{"action":"status"}' | tslink api
```

## Roadmap / Experimental Packages

The repository contains packages and registry fields for features that are not wired into the shipped `tslink serve` runtime yet. Treat these as roadmap or experimental until end-to-end integration tests are added:

| Area | Current status |
|---|---|
| Docker labels | Package exists, but `serve` does not start Docker discovery. |
| Middleware | Package and schema exist, but runtime does not apply rate limit, Basic Auth, IP allow list, or CORS. |
| Admin dashboard / REST API | Handler exists, but no admin node is launched. |
| Prometheus `/metrics` | Instrumentation exists, but no scrape endpoint is mounted. |
| Custom domain / ACME | Fields are accepted, but runtime TLS/ACME listener is not wired. |
| Cluster sync | Package exists without production transport or `serve` integration. |

## Prerequisites

- [Tailscale account](https://tailscale.com) (free for personal use)
- Tailscale installed on the devices you want to access from (phone, tablet, etc.)
- Go 1.25+ (if building from source)

## Platform Support

| Platform | Daemon | Auto-start |
|----------|--------|------------|
| macOS | `--daemon` | LaunchAgent |
| Linux | `--daemon` | systemd user service |
| Windows | `--daemon` | Startup folder |

## Roadmap

- [x] OAuth client secret accepted by login and tsnet auth paths; validate tag/device automation before unattended use
- [ ] Runtime custom-domain / ACME TLS
- [ ] Web dashboard accessible from tailnet
- [ ] Docker image and Docker label discovery
- [ ] Headscale end-to-end testing
- [ ] Full API parity and integration-tested Layer 2 modules

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## Security

For security concerns, please see [SECURITY.md](./SECURITY.md).

## License

This project is licensed under the [Apache License 2.0](./LICENSE).

```
Copyright 2026 Maintainer (monody0007)
```
