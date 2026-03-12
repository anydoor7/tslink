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

TSLink turns your machine into a secure gateway. One command exposes any local service — a web app, an API, a file directory — to your private [Tailscale](https://tailscale.com) network. Accessible from your phone, tablet, or any device on your tailnet.

- **Zero configuration** — no port forwarding, no DNS, no certificates to manage
- **End-to-end encrypted** — WireGuard encryption via Tailscale, your data never touches the public internet
- **Instant TLS** — automatic HTTPS with valid certificates, no setup required
- **Live reload** — add or remove services while TSLink is running, changes take effect immediately
- **Runs as a daemon** — start once, runs in the background, auto-starts on login via macOS LaunchAgent

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
# 1. Authenticate with Tailscale
tslink login

# 2. Expose a local web service
tslink add myapp --proxy localhost:3000

# 3. Start the gateway
tslink serve --daemon

# Open https://tslink.<your-tailnet>.ts.net/s/myapp from any device
```

### Expose a File Directory

```bash
tslink add documents --dir ~/Documents
# Access at https://tslink.<your-tailnet>.ts.net/f/documents/
```

## Commands

| Command | Description |
|---------|-------------|
| `tslink login` | Authenticate with your Tailscale account |
| `tslink logout` | Clear authentication state |
| `tslink add <name> --proxy host:port` | Expose a local web service |
| `tslink add <name> --dir /path` | Expose a file directory |
| `tslink remove <name>` | Remove a registered service |
| `tslink list` | List all registered services |
| `tslink serve` | Start the gateway (foreground) |
| `tslink serve --daemon` | Start the gateway (background) |
| `tslink stop` | Stop the gateway |
| `tslink status` | Show gateway status |
| `tslink install` | Auto-start on login (macOS LaunchAgent) |
| `tslink uninstall` | Remove auto-start |

## How It Works

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│   Your Mac  │         │   Tailscale Network  │         │  Your Phone  │
│             │         │   (WireGuard mesh)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  Browser     │
│  :3000      │  tsnet  │  End-to-end encrypted │  HTTPS │              │
│  ~/Documents│  node   │  No public internet   │  +TLS  │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink embeds a [tsnet](https://tailscale.com/kb/1244/tsnet) node directly into the binary — no Tailscale client installation required on the server side. It joins your tailnet as a device called `tslink`, obtains automatic TLS certificates, and reverse-proxies requests to your local services.

**Key architectural decisions:**
- **Embedded node** — no dependency on an external Tailscale daemon
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — clean daemon management with signal handling

## Prerequisites

- [Tailscale account](https://tailscale.com) (free for personal use)
- Tailscale installed on the devices you want to access from (phone, tablet, etc.)
- Go 1.25+ (if building from source)

## Roadmap

- [ ] Linux and Windows support
- [ ] Web dashboard for service management
- [ ] Multi-node service sharing
- [ ] Custom domain mapping
- [ ] API mode for programmatic integration
- [ ] Access control per service

## Contributing

Contributions are welcome. Please open an issue first to discuss what you would like to change.

## License

This project is licensed under the [Apache License 2.0](./LICENSE).

```
Copyright 2026 Maintainer (monody0007)
```
