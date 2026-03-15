<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">Zero-trust service gateway for your private network.<br>Expose local services securely with one command — no public internet, no third-party servers.</p>
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
| **Least-privilege access** | Per-service ACL via `--allow` restricts access to specific users or tags. Each service operates under its own identity. |
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
- **Access control** — per-service ACL with user/tag-based filtering (`--allow user@example.com,tag:admin`)
- **Middleware** — built-in rate limiting, Basic Auth, IP allowlist, and CORS
- **Prometheus metrics** — request counts, latency histograms, active connections
- **Docker discovery** — auto-register containers via labels (`tslink.enable=true`)
- **API mode** — JSON-over-stdin/stdout for programmatic integration by AI agents and scripts
- **Headscale compatible** — works with self-hosted control servers via `--control-url`
- **Funnel** — optionally expose services to the public internet via Tailscale Funnel

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

TSLink only needs one key — your [Tailscale API access token](https://login.tailscale.com/admin/settings/keys). Auth keys are derived automatically. Your API key is stored in the system keychain (macOS Keychain / Linux secret service / Windows Credential Manager), never in plaintext.

### More Examples

```bash
# Expose a file directory
tslink add documents --dir ~/Documents

# Expose a database via TCP proxy
tslink add mydb --tcp localhost:5432

# Ephemeral node (auto-removed from tailnet when stopped)
tslink add demo --proxy localhost:8080 --ephemeral

# Identity-aware access control
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
| `--allow user@,tag:x` | Per-service access control (comma-separated) |
| `--funnel` | Expose via Tailscale Funnel (public internet, proxy only) |
| `--domain example.com` | Custom domain mapping (proxy only) |
| `--acme-email user@example.com` | Email for Let's Encrypt certificates (requires `--domain`) |

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
- **Secure credential management** — system keychain storage with dynamic auth key derivation (no keys stored in files)
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — clean daemon management with signal handling
- **Middleware pipeline** — rate limiting, Basic Auth, IP allowlist, CORS per service
- **Structured logging** — slog-based structured logging with access logs
- **Prometheus metrics** — `tslink_requests_total`, `tslink_request_duration_seconds`, `tslink_active_connections`
- **Docker discovery** — auto-register containers with `tslink.enable=true` label

## API Mode

TSLink includes a JSON-over-stdin/stdout API mode for programmatic integration — designed for AI agents, scripts, and CI/CD pipelines.

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

## Docker Auto-Discovery

TSLink can auto-discover and register Docker containers using labels:

```yaml
services:
  webapp:
    image: nginx
    labels:
      tslink.enable: "true"
      tslink.name: "webapp"
      tslink.type: "proxy"        # proxy (default) or tcp
      tslink.target: "localhost:8080"  # optional for proxy (auto-detects first exposed port)
      tslink.port: "8080"         # required for tcp type
      tslink.ephemeral: "true"    # optional
      tslink.tags: "tag:web"      # optional, comma-separated
```

Containers are automatically registered when started and unregistered when stopped.

## Middleware

Each service in `registry.json` can include a `middleware` block:

```json
{
  "services": [
    {
      "name": "myapp",
      "type": "proxy",
      "target": "http://localhost:3000",
      "middleware": {
        "rate_limit": 10.0,
        "basic_auth": "user:password",
        "ip_allow_list": ["100.64.0.1/16"],
        "cors_origins": ["https://frontend.example.com"]
      }
    }
  ]
}
```

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

- [x] OAuth long-lived credentials (`tskey-client-*` support)
- [x] Let's Encrypt integration for custom domains (`--domain` + `--acme-email`)
- [x] Web dashboard accessible from tailnet (admin API + HTML dashboard)
- [ ] Docker image (`ghcr.io/monody0007/tslink`)
- [ ] Headscale end-to-end testing
- [ ] Web dashboard enhancements

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## Security

For security concerns, please see [SECURITY.md](./SECURITY.md).

## License

This project is licensed under the [Apache License 2.0](./LICENSE).

```
Copyright 2026 Maintainer (monody0007)
```
