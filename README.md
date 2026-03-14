<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">Your local services, securely accessible from anywhere on your private network.</p>
</p>

<p align="center">
  <a href="https://github.com/monody0007/tslink/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README_zh.md">中文文档</a>
</p>

---

## Why TSLink Exists

We are entering a new era of personal computing.

AI agents now run on your machine — generating reports, processing data, building applications, serving local tools. Your Mac or PC is no longer just a workstation. It is becoming your **personal server**, a private hub of intelligence and productivity.

But here is the problem: **everything is locked inside your machine.**

Want to check that AI-generated report from your phone? Want to access your local development server from a tablet on the couch? Today, your options are:

- **Port forwarding** — complex, insecure, exposes your home network
- **ngrok / Cloudflare Tunnel** — routes your private data through third-party servers
- **VPN** — heavy, slow, requires infrastructure and maintenance

None of these were designed for the world we are entering — a world where every person has an AI-powered machine generating valuable, private content that needs to be **securely accessible from any device, instantly**.

### Privacy Is Not Optional

Your AI outputs — research, code, personal documents, business data — should never traverse a third-party server. In an age of increasing data breaches and surveillance, **the safest path between your devices is a direct one**.

### The National and Global Interest

As AI becomes embedded in daily work, a critical infrastructure gap has emerged: **how do individuals and organizations securely bridge the output of local AI systems to the devices they actually use?**

This is not just a convenience problem. It is a **security problem**, a **productivity problem**, and an **infrastructure problem** that affects:

- **Individual developers and researchers** who need private, zero-trust access to local services
- **Small businesses and startups** accelerating AI adoption without enterprise IT budgets
- **Enterprises** seeking to reduce attack surface by eliminating public exposure of internal tools
- **National cybersecurity posture** — every service that stays off the public internet is one less target

TSLink addresses this gap directly.

## What TSLink Does

TSLink turns your machine into a secure gateway. One command exposes any local service — a web app, an API, a file directory, a database — to your private [Tailscale](https://tailscale.com) network. Each service gets its own dedicated hostname with automatic TLS. Accessible from your phone, tablet, or any device on your tailnet.

- **Zero configuration** — no port forwarding, no DNS, no certificates to manage
- **End-to-end encrypted** — WireGuard encryption via Tailscale, your data never touches the public internet
- **Instant TLS** — automatic HTTPS with valid certificates, no setup required
- **Per-service nodes** — each service gets its own tailnet hostname (`https://<name>.<tailnet>.ts.net`)
- **Live reload** — add or remove services while TSLink is running, changes take effect immediately
- **Cross-platform** — runs on macOS, Linux, and Windows
- **Runs as a daemon** — start once, runs in the background, auto-starts on login
- **TCP proxy** — expose databases, SSH, Redis, and other non-HTTP services
- **Access control** — per-service ACL with user/tag-based filtering
- **Middleware** — built-in rate limiting, Basic Auth, IP allowlist, and CORS
- **Prometheus metrics** — request counts, latency histograms, active connections
- **Docker discovery** — auto-register containers via labels
- **API mode** — JSON-over-stdin/stdout for programmatic integration by AI agents and scripts
- **Funnel** — optionally expose services to the public internet via Tailscale Funnel

## Quick Start

### Install

```bash
# Homebrew (macOS)
brew install monody0007/tap/tslink

# From source
git clone https://github.com/monody0007/tslink.git
cd tslink && go install .
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

TSLink only needs one key — your [Tailscale API access token](https://login.tailscale.com/admin/settings/keys). Auth keys are derived automatically. Your API key is stored in the system keychain (macOS Keychain), never in plaintext.

### More Examples

```bash
# Expose a file directory
tslink add documents --dir ~/Documents
# Access at https://documents.<your-tailnet>.ts.net

# Expose a database via TCP proxy
tslink add mydb --tcp localhost:5432
# Connect from any device: psql -h mydb.<your-tailnet>.ts.net

# Ephemeral node (auto-removed when stopped)
tslink add demo --proxy localhost:8080 --ephemeral

# With access control
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# Public exposure via Tailscale Funnel
tslink add public --proxy localhost:3000 --funnel

# Custom domain
tslink add mysite --proxy localhost:3000 --domain app.example.com

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

## How It Works

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│   Your Mac  │         │   Tailscale Network  │         │  Your Phone  │
│             │         │   (WireGuard mesh)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  Browser     │
│  :3000      │  tsnet  │  End-to-end encrypted │  HTTPS │              │
│  :5432      │  nodes  │  No public internet   │  +TLS  │              │
│  ~/Documents│  (1/svc)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink creates a dedicated [tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service — no Tailscale client installation required on the server side. Each service joins your tailnet as its own device (e.g., `myapp`, `docs`, `mydb`), obtains automatic TLS certificates, and proxies requests to your local services.

**Key architectural decisions:**
- **Per-service embedded nodes** — each service gets its own tailnet hostname and TLS certificate
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — clean daemon management with signal handling
- **Middleware pipeline** — rate limiting, Basic Auth, IP allowlist, CORS per service
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
      tslink.type: "proxy"
      tslink.port: "8080"
```

Containers are automatically registered when started and unregistered when stopped.

## Middleware

Each service can be configured with middleware via `registry.json`:

```json
{
  "middleware": {
    "rate_limit": 10.0,
    "basic_auth": "admin:secret",
    "ip_allow_list": ["100.64.0.1/16"],
    "cors_origins": ["https://frontend.example.com"]
  }
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

- [ ] OAuth long-lived credentials (currently API key based)
- [ ] Let's Encrypt integration for custom domains
- [ ] Docker image (`ghcr.io/monody0007/tslink`)
- [ ] Headscale `--control-url` global config persistence
- [ ] Web dashboard accessible from tailnet

## Contributing

Contributions are welcome. Please open an issue first to discuss what you would like to change.

## License

This project is licensed under the [Apache License 2.0](./LICENSE).

```
Copyright 2026 Maintainer (monody0007)
```
