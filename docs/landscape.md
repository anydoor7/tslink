# Landscape

## Why TSLink?

### What you skip

Per-service hostnames are a first-class Tailscale feature. [Tailscale Services](https://tailscale.com/docs/features/tailscale-services) has been [generally available since February 2026](https://tailscale.com/blog/services-ga), and `tailscale serve --service=svc:web-server --https=443 127.0.0.1:8080` gives you `https://web-server.<tailnet>.ts.net` on a plain `tailscaled`. TSLink is built on [tsnet](https://tailscale.com/docs/features/tsnet), which Tailscale documents for exactly this purpose. TSLink independently uses this documented integration pattern.

What TSLink changes is who can set it up and how long it takes. The native path asks for:

- **Admin reach.** Defining a Service requires "Owner, Admin, or Network admin account permissions."
- **A tagged host.** "You cannot use a device authenticated with a user account as a Service host."
- **An approval step.** "An Admin, Network admin, or Owner must approve the host before it becomes active."
- **A tailnet-wide policy edit** to scope access, through the admin console, GitOps, or the API. There is no `tailscale` command for editing ACLs.
- **A Go program**, if you go the tsnet route directly. tsnet is a library, so each service is something you write and compile.

TSLink asks for none of them. `tslink add ollama --proxy localhost:11434` points at a process that is already running, from an unprivileged account, and `--allow you@example.com` is a flag on that same command rather than a change to a shared policy file.

That access list is enforced at the HTTP layer, so it complements tailnet ACLs rather than replacing them. Anything on your tailnet that can reach the port directly is still governed by your ACLs.

### The broader landscape

Port forwarding, VPNs, ngrok, and Cloudflare Tunnel each either place the service on the public internet or route private traffic through a third party, and a VPN that avoids both carries the operational overhead of running one.

As local AI workloads, self-hosted services, and personal infrastructure grow, the gap between what individuals need and what enterprise security tools provide keeps widening. Most zero-trust tooling targets large enterprises with dedicated security teams.

**TSLink gives local services per-service tailnet identities.** One command turns your machine into a Tailscale-backed gateway. Tailnet transport uses Tailscale/WireGuard semantics; proxy/file services can add HTTP identity and `--allow` checks, while raw TCP stays a private byte stream without TSLink HTTP middleware.

## What TSLink Does

One command exposes any local service: a web app, an API, a file directory, a database: to your private Tailscale network. Proxy/file services use Tailscale HTTPS listeners; raw TCP services use private tailnet transport without TSLink TLS termination.

```bash
tslink add myapp --proxy localhost:3000
# → https://myapp.<your-tailnet>.ts.net: reachable from tailnet devices your tailnet policy permits
```

### Features

- **Zero configuration**: no port forwarding, no DNS, no certificates to manage
- **WireGuard tailnet path**: Tailnet device-to-device traffic uses WireGuard via Tailscale; public exposure requires explicit Funnel opt-in
- **Proxy/file HTTPS**: Tailscale HTTPS listeners for HTTP proxy and file services; raw TCP remains a private tailnet byte stream
- **Per-service isolation**: each service gets its own tailnet hostname and identity (`https://<name>.<tailnet>.ts.net`)
- **Live reload**: add or remove services while TSLink is running, changes take effect immediately
- **Cross-platform**: runs on macOS, Linux, and Windows
- **Runs as a daemon**: start once, runs in the background, auto-starts on login
- **TCP proxy**: expose databases, SSH, Redis, and other non-HTTP services
- **HTTP access control**: `--allow user@example.com,tag:admin` for proxy and file services
- **Safety diagnostics**: `tslink doctor`, `tslink status --urls`, and `tslink access explain` make local evidence and unknown external policy layers explicit
- **Agent-ready automation**: every command except the stdio `tslink mcp` server accepts `--json` and answers with one versioned envelope; `tslink mcp` (stdio) and `tslink serve --mcp` (tailnet-only remote control plane) expose the same MCP tools
- **Personal templates**: preview and apply small private service suites without overwriting existing services
- **Headscale compatibility path**: advanced/self-hosted control-server use via `--control-url`
- **Funnel guardrails**: public internet exposure is opt-in and requires explicit `--public` acknowledgement
