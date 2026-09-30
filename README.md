<p align="center">
  <h1 align="center">TSLink</h1>
  <p align="center">Give any local service its own tailnet hostname with one command.<br>No admin console, no tags, no host approval, no Go program to write.</p>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/badge/Go-1.26.6%2B-00ADD8.svg" alt="Go"></a>
  <a href="https://github.com/monody0007/tslink"><img src="https://img.shields.io/github/stars/monody0007/tslink?style=social" alt="Stars"></a>
</p>

<p align="center">
  <a href="./README_zh.md">中文文档</a> ·
  <a href="https://github.com/monody0007/tslink">GitHub</a>
</p>

**Open source:** Apache 2.0 permits personal and commercial use by organizations of any size. [Optional support and cooperation](./COMMERCIAL.md).

---

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

## Security Model

How TSLink maps onto common zero-trust principles:

| Zero-Trust Principle | TSLink Implementation |
|-----|-----|
| **HTTP caller verification** | Tailnet HTTP proxy/file requests can be authenticated via Tailscale WhoIs. Identity headers (`X-Tailscale-User-Login`, `X-Tailscale-User-Name`, `X-Tailscale-User-Picture`, `X-Tailscale-Node`) are injected only when WhoIs succeeds. Public Funnel exposure and raw TCP streams are not treated as TSLink-enforced Tailscale user authentication. |
| **HTTP least-privilege access** | `--allow` restricts proxy and file services to specific users or tags. TCP services rely on Tailscale network ACLs and tags. |
| **Assume breach** | Tailnet device-to-device traffic uses WireGuard encryption. Even if your local network is compromised, traffic between your Tailscale devices remains encrypted; public Funnel paths follow Tailscale Funnel semantics. |
| **Per-service network identity** | Each service runs as a separate tsnet node with its own hostname and network identity. This is network segmentation, not host process isolation or a compliance attestation. |
| **No implicit trust** | No services are exposed to the public internet by default. The default first run uses Tailscale interactive enrollment with no stored administrative credential, no advertised tags, and no ACL edits. Optional durable-install credentials use the system keychain first; macOS/Linux file fallback requires proof that no stale keychain credential remains. |

This is a design mapping, not a formal attestation. TSLink claims no compliance status; the machine-readable manifest is [`internal/security/capabilities.v1.json`](./internal/security/capabilities.v1.json), and every capability in it records its compliance status explicitly.

## What TSLink Does

One command exposes any local service — a web app, an API, a file directory, a database — to your private Tailscale network. Proxy/file services use Tailscale HTTPS listeners; raw TCP services use private tailnet transport without TSLink TLS termination.

```bash
tslink add myapp --proxy localhost:3000
# → https://myapp.<your-tailnet>.ts.net — accessible from any device on your tailnet
```

### Features

- **Zero configuration** — no port forwarding, no DNS, no certificates to manage
- **WireGuard tailnet path** — Tailnet device-to-device traffic uses WireGuard via Tailscale; public exposure requires explicit Funnel opt-in
- **Proxy/file HTTPS** — Tailscale HTTPS listeners for HTTP proxy and file services; raw TCP remains a private tailnet byte stream
- **Per-service isolation** — each service gets its own tailnet hostname and identity (`https://<name>.<tailnet>.ts.net`)
- **Live reload** — add or remove services while TSLink is running, changes take effect immediately
- **Cross-platform** — runs on macOS, Linux, and Windows
- **Runs as a daemon** — start once, runs in the background, auto-starts on login
- **TCP proxy** — expose databases, SSH, Redis, and other non-HTTP services
- **HTTP access control** — `--allow user@example.com,tag:admin` for proxy and file services
- **Safety diagnostics** — `tslink doctor`, `tslink status --urls`, and `tslink access explain` make local evidence and unknown external policy layers explicit
- **Agent-ready automation** — every command except the stdio `tslink mcp` server accepts `--json` and answers with one versioned envelope; `tslink mcp` (stdio) and `tslink serve --mcp` (tailnet-only remote control plane) expose the same MCP tools
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
| `--json` envelope on every command, `tslink mcp` over stdio, and the opt-in tailnet-only MCP control plane (`tslink serve --mcp`) | Roadmap/experimental dashboard, REST API, or multi-user admin plane |
| Built-in personal templates | Roadmap/experimental marketplace or third-party template registry |

## Quick Start

### Install

Requires Go 1.26.6 or newer.

```bash
go install github.com/monody0007/tslink@latest

# The binary lands in $(go env GOPATH)/bin, which is not on PATH by default:
export PATH="$PATH:$(go env GOPATH)/bin"
```

Homebrew and prebuilt archives arrive with the first tagged release. Until then,
installing from source is the supported path. Building from a clone works too:

```bash
git clone https://github.com/monody0007/tslink.git && cd tslink && go install .
```

### From nothing to a URL

One command, no account setup, no token to copy:

```bash
tslink share ./build
```

TSLink registers the directory, starts the daemon if it is not already running,
and prints one Tailscale authorization URL. Open it once, approve the node, and
the command returns the live URL. Open that from your phone, your tablet, or any
other device on your tailnet.

No API token. No OAuth client. No admin console visit. The node is enrolled as
you, so it needs no ACL policy of its own.

If you would rather register services explicitly and keep them around:

```bash
# 1. Expose a local web service
tslink add myapp --proxy localhost:3000

# On first use, TSLink installs its background service and prints the exact URL.
# If Tailscale enrollment is needed, open the printed authorization URL once.

# Access https://myapp.<your-tailnet>.ts.net from any device
```

`add`, `share`, and `template apply --yes` automatically install and start TSLink's
background service when it is absent, including in CI or without a TTY. Installation
announces the manager, file location, config directory, and `tslink uninstall` undo
command on stderr; `--json` stdout remains a single result. Use `--no-daemon-install`
on these commands to opt out. Offline `add` saves configuration and reports
`daemon_running:false` plus repair guidance, without a green check. `share` with
that flag requires an already running service. `add` waits up to 30 seconds for an
exact URL or an enrollment URL; use `--wait=0` for registration without waiting.

The MCP `add`, `share`, and `template_apply` tools use the same bootstrap policy
and expose `no_daemon_install`. MCP `add` returns current URL/enrollment evidence
after setup without an additional URL wait; poll `url` if it is still pending.
`add` and template application save the registry before installing, retaining it
if setup fails. Explicit `install` also works before any registry exists.
Setup errors report whether a supervisor definition remains: Linux can leave an
enabled unit retrying after a readiness failure; macOS new-install verification
rolls back its job/plist when cleanup succeeds. Inspect `tslink logs` and `tslink
doctor` before retrying (Linux also: `journalctl --user -u tslink.service`). The
installer checks stable manager state. Bootstrap then judges setup on that alone:
the supervisor owns a stable process whose identity it verified. It also looks for
fresh daemon business evidence, but that evidence is produced only after the daemon
reaches the Tailscale coordination server, so its absence leaves setup successful and
the enrollment URL is resolved by the wait `add` already performs. Losing the verified
process is still a setup failure. Neither check guarantees future uptime.

`status` and `doctor` report `supervision` in text and JSON: verified manager,
autostart, `autostart_scope`, restart policy, and evidence. An unverified running
process is `manual`; an absent process without verified management is `none`.
`autostart_scope` answers what `autostart` alone cannot for a per-user supervisor:
`boot` returns with the machine while nobody is logged in, `login` waits for this
user to sign in, and `unknown` means the difference could not be determined. A macOS
LaunchAgent and a Windows Startup entry are always `login`. A systemd user unit is
`boot` only with lingering enabled; without it, `status` reports `login` and names
`loginctl enable-linger "$USER"`. TSLink reports lingering and never changes it,
because it applies to every service the user owns. Doctor treats registered
services without supervision as an error and defers backend probes while TSLink is
confirmed stopped. When PID identity or supervisor state is uncertain, doctor reports
a warning and keeps backend probes enabled; inspect the running binary and logs
before installing or restarting. `url` returns an enrollment action for any pending
node in this daemon instead of waiting again. `url` points to `tslink install` when the service is stopped.

On macOS this installs a LaunchAgent that starts at user login; on Linux it enables
a systemd user unit. For Linux boot before login and survival after logout, run
`loginctl enable-linger "$USER"` once. Windows Startup is started immediately by
automatic setup and on later sign-ins; it has no crash restart or live PID ownership
proof. The backend application must also start after reboot, and first-time Tailscale
enrollment still requires authorization. Each installed definition binds the absolute
`TSLINK_CONFIG_DIR`; automatic setup refuses to overwrite another config's manager.
Homebrew does not install a second service manager.

### Share in one command

`tslink share` infers whether its argument is a directory, a regular file, a
bare port, or `host:port`. It registers the service without overwriting an
existing name, starts the daemon when needed, waits for an exact runtime URL,
and prints only that URL to stdout. Shares use ephemeral nodes by default.

```bash
tslink share ./build                # serves the whole ./build tree, browsable
tslink share ./report.html          # serves only report.html, not its siblings
tslink share 3000
tslink share localhost:8080 --name preview
tslink share ./build --ephemeral=false
```

The two path forms differ in reachable surface, and the difference is the
service's own boundary rather than a listing preference. A directory target
serves every file under it and directories without an `index.html` render a
listing. A regular-file target serves that one file: its URL is the file, the
service root redirects to it, and every other path answers 404, including the
file's siblings in the same directory. The registry records the narrowing in
the file service's `file` field; an entry without that field is a directory
share.

Neither form restricts *who* may read it: every member of the tailnet can
fetch the share, and `tslink share` has no `--allow` flag. To limit readers of
a directory, register it with `tslink add <name> --dir <directory> --allow
<principal>` instead, or pass `allow` to the MCP `share` tool.

On a credential-free first run, the one stdout line is the Tailscale
authorization URL and stderr gives the exact `tslink url <name> --wait`
continuation. With `--json`, this is a successful `status:"needs_login"`
result containing `auth_url`, not an authentication error. Retrying the same
target reuses its existing service instead of creating suffixed orphan nodes.

### MCP server for agents

`tslink mcp` runs a local MCP server over stdio. The MCP process itself opens no
network listener; invoking its `share` tool may start the separate TSLink daemon
and the requested tsnet service. It exposes 19 tools covering the per-service
surface of the CLI: `share`, `add`, `list`, `unshare`, `status`, `url`,
`tags_list`, `tags_set`, `access_explain`, `doctor`, `logs`, `invite_user`,
`invite_device`, `invite_list`, `invite_revoke`, `invite_resend`,
`template_list`, `template_plan`, and `template_apply`. Daemon lifecycle,
install, login/logout, and configuration stay CLI-only. Configure an
MCP client to launch the installed `tslink` command with the single argument
`mcp`:

```json
{
  "command": "tslink",
  "args": ["mcp"]
}
```

The `share` tool accepts the same path/port/host:port targets as the CLI. When
authorization is pending it returns `{"status":"needs_login","auth_url":"..."}`
as a normal tool result so an agent can open the URL and retry. Credential
values are never returned through MCP. The same tools can also be served to
other machines on your tailnet; see [Remote MCP Control Plane](#remote-mcp-control-plane).

TSLink has two authentication tiers:

- **Tier 1 — zero credential (default)**: a user-owned node with no advertised tags and no remote ACL edits. This is the least-privilege path for a quick page or ephemeral share. Each fresh service node has its own enrollment URL; a one-service quick share takes one browser click. User-owned Tailscale node keys expire, so a node left running for months can eventually require re-authentication.
- **Tier 2 — stored credential (opt-in)**: preserves tagged, per-service startup for durable multi-service installations. Run `tslink login` only when you need this tier. If Tier 1 services are already enrolled, restart `tslink serve` after login; TSLink records the transition and replaces their user-owned node state on the next credentialed start so tsnet cannot silently ignore the new auth key.

Tier 2 accepts one of these administrative credential types:

- **API access token** (`tskey-api-*`) — generate at [Admin → Keys](https://login.tailscale.com/admin/settings/keys). Use this for the most complete automation today, including tag and device management through the Tailscale API. It expires periodically.
- **OAuth client secret** (`tskey-client-*`) — generate at [Admin → OAuth](https://login.tailscale.com/admin/settings/oauth). It does not expire, but TSLink's current Tailscale tag/device automation is narrower in this mode because those operations use the Tailscale REST API. Use it only after validating your required tag/device operations.

`tslink login` guides you through either Tier 2 credential path. It does not perform a disposable browser login first. Credentials are stored in the system keychain first (macOS Keychain / Linux secret service / Windows Credential Manager). On macOS and Linux, restricted-permission file fallback succeeds only when TSLink can prove any stale keychain credential is absent or has been removed. A completely unreachable or uncertain keychain makes login fail rather than risk replacing an existing credential with an unproven file value; restore keychain access and retry. Headless operation alone does not guarantee fallback. Windows has no file fallback, because TSLink cannot prove a user-only DACL locally, so `tslink login` fails there when Credential Manager is unavailable. A running Tier 1 daemon remains unchanged until it is restarted; on the next credentialed start, existing per-service tsnet state is cleared and each service re-enrolls with its derived auth key and configured tags.

For non-interactive setup, prefer stdin. Environment variables are acceptable only when they are pre-injected by a secret manager before the command starts; do not inline secret values in the shell command because they can land in shell history:

```bash
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
```

The compatible `--api-key` and `--client-secret` flags remain available, but command-line arguments can be visible to other local processes.

### Tag Management

TSLink manages local service tags by default. Remote Tailscale ACL mutation is disabled by default because TSLink does not yet prove lossless HuJSON policy preservation.

On the zero-credential Tier 1 path, registry tags remain configured but are not advertised by the user-owned node, and no remote tag/ACL API is called. The following tag behavior applies to the stored-credential Tier 2 path.

- **Default tag** — every service gets `tag:tsmain` applied automatically when `--tags` is not specified.
- **Remote ACL reads** — `tslink tags pull` fetches remote ACL tags only in API access token mode; OAuth-only mode skips the remote read and reports that an API access token is required.
- **Remote ACL writes** — `tslink login --manage-acl`, `tslink serve --manage-acl`, and `tslink tags delete-remote --manage-acl` opt in to typed whole-policy ACL writes with a machine-readable side-effect plan. Default login, serve, and tag flows do not rewrite shared ACL policy.
- **Strict tag grammar** — tags must match `tag:<lowercase-hyphen-name>` with lowercase letters, numbers, and hyphens. Migrate legacy tags such as `tag:Web`, `tag:db_main`, or `web` with `tslink tags set <service> tag:<lowercase-hyphen-name>` or by editing `registry.json`. Invalid legacy tags fail `tslink serve` validation and must be fixed before the gateway starts.
- **Runtime auth refresh** — tag, ephemeral, and effective control-server URL changes restart affected nodes with fresh per-service auth material. A zero-credential to stored-credential login records a pending identity transition; restart `tslink serve` to clear the old user-owned node state and re-enroll with tagged credentials. Legacy `authkey` file changes also require a restart.

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

# Remove an ACL tag owner rule globally after local safety checks and explicit ACL-management opt-in
tslink tags delete-remote tag:old-tag --force --manage-acl
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
| `tslink login` | Store an optional Tier 2 API access token or OAuth client secret |
| `tslink logout` | Clear credentials from keychain and files |
| `tslink add <name> --proxy host:port` | Expose a local web service |
| `tslink add <name> --dir /path` | Expose a file directory |
| `tslink add <name> --tcp host:port` | Expose a raw TCP service (databases, SSH, etc.) |
| `tslink remove <name>` | Remove a service and report protected/manual remote cleanup guidance |
| `tslink list` | List the services registered on this machine |
| `tslink list --tailnet` | Read-only: list every TSLink-tagged device in the whole tailnet, including other machines' services and orphans (requires a stored API credential) |
| `tslink share <path\|port\|host:port>` | Share a local path or web port and print its tailnet URL |
| `tslink url <name>` | Print one service's exact runtime URL |
| `tslink cleanup` | Reconcile expired Funnel exposure and TSLink-owned resources; previews by default, applies with `--dry-run=false`; preserves the shared Funnel ACL grant even when no local service uses it |
| `tslink serve` | Start the gateway (foreground) |
| `tslink serve --daemon` | Start the gateway (background) |
| `tslink serve --mcp` | Start the gateway and serve the remote MCP control plane on a dedicated tailnet-only node (`mcp.allow` required) |
| `tslink stop` | Stop the gateway |
| `tslink status` | Show gateway status |
| `tslink status --urls` | Show owner-only service URLs, exposure mode, allow summary, backend, and warning codes |
| `tslink doctor` | Diagnose credentials, daemon, registry, runtime snapshot, exposure, target safety, and Tailscale SSH enablement without mutating state |
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
| `tslink tags delete-remote <tag> --force --manage-acl` | Remove an ACL tag owner rule globally from Tailscale ACL after local safety checks and explicit remote-write opt-in |
| `tslink invite user <email>` | Invite a user to join the tailnet; requires a user-owned API access token |
| `tslink invite device <service> <email>` | Share a TSLink-owned service device with an external user; requires exact node ownership proof |
| `tslink invite list` | List open user and TSLink-owned device invites |
| `tslink invite revoke <id> --kind <user\|device>` | Revoke a user or device invite |
| `tslink invite resend <id> --kind <user\|device>` | Resend an emailed user or device invite |
| `tslink mcp` | Local MCP server over stdio for agents; no network listener, no `mcp.allow` needed |
| `tslink config` | Manage global configuration (set/get/list) |
| `tslink manifest` | Print the machine-readable description of every command, flag, exit code, and error code |
| `tslink registry check [path]` | Strictly validate a `registry.json` without modifying it |
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
| `--domain example.com` | Reserved roadmap flag: rejected with `feature_unavailable`; custom-domain runtime TLS is not wired |
| `--acme-email user@example.com` | Reserved roadmap flag: rejected with `feature_unavailable`; no shipped ACME listener |

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

TSLink creates a dedicated [tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service — no Tailscale client installation required on the server side. Each service joins your tailnet as its own device (e.g., `myapp`, `docs`, `mydb`). Proxy/file services use Tailscale HTTPS listeners; raw TCP services use private tailnet transport and proxy bytes to the configured target.

**Key architectural decisions:**
- **Per-service embedded nodes** — each service gets its own tailnet identity and hostname; proxy/file services also get Tailscale HTTPS listener semantics
- **Identity-aware proxying** — WhoIs verification on tailnet HTTP proxy/file requests, with identity headers injected and spoofing prevented; public Funnel and raw TCP do not get TSLink-enforced HTTP identity
- **Secure credential management** — system keychain storage; macOS/Linux restricted-permission file fallback only after stale keychain authority is proven absent or cleared
- **File-based registry** — services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload** — file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle** — daemon management with process identity checks and platform-specific stop behavior
- **Structured logging** — slog-based structured logging with access logs
- **Metrics instrumentation** — request metrics are collected internally; a public `/metrics` endpoint is roadmap

## JSON Automation

Every command except the stdio `tslink mcp` server accepts `--json` and writes one versioned envelope to stdout, so owner-side automation is the same CLI with one flag. There is no REST server, dashboard, or member-facing service directory; JSON views are redacted rather than raw registry records.

```bash
# List services registered on this machine
tslink list --json

# Add a service
tslink add myapp --proxy localhost:3000 --json

# Remove a service
tslink remove myapp --json

# Check status
tslink status --json

# Show owner-only endpoint/exposure overview
tslink status --urls --json

# Run read-only diagnostics (non-loopback targets are probed only with --probe-external)
tslink doctor --json

# Explain one service's local access model
tslink access explain myapp --json

# Preview/apply built-in templates
tslink template list --json
tslink template apply local-web --dry-run --json
tslink template apply local-web --yes --json
```

The same operations are available to MCP clients through `tslink mcp` (stdio) and the remote control plane described below; `tslink manifest --json` prints the machine-readable description of every command, flag, exit code, and error code.

All `--json` output uses the same versioned envelope. `command` names the command that produced it:

```json
{
  "type": "tslink.result",
  "ok": true,
  "schema_version": 1,
  "command": "list",
  "code": 0,
  "data": {
    "schema_version": "vnext.1",
    "services": [],
    "count": 0
  }
}
```

Failures include a stable machine error code plus human text, and `error.next` lists recovery commands when one applies:

```json
{
  "type": "tslink.result",
  "ok": false,
  "schema_version": 1,
  "command": "list",
  "code": 2,
  "error": {
    "code": "usage_error",
    "message": "--tailnet conflicts with --verbose; --verbose filters this machine's registered services, while --tailnet reports tailnet devices",
    "next": ["tslink --help"]
  }
}
```

On macOS, `launchctl_domain_unavailable` is the deliberate exit-1 refusal used when TSLink cannot prove an install or uninstall handoff is safe. Its failure `data` includes `unavailable_domain`, `force_available`, the exact `force_command`, and `force_risk`; agents do not need to parse `error.message` to discover the recovery contract.

`--json` changes only the output format. `tslink add --json` follows the same safety guardrails as the human path: Funnel services require `--public`; TCP services reject `--allow` because TSLink does not apply HTTP identity checks to raw TCP streams.

## Working Across Machines

The registry in `~/.config/tslink/registry.json` is per machine, while the tailnet is shared. Two read-only features make that boundary visible, and the [Remote MCP Control Plane](#remote-mcp-control-plane) lets an agent on another tailnet machine operate this install.

### Every TSLink device in the tailnet

`tslink list` reads this machine's registry. `tslink list --tailnet` asks the Tailscale API instead and reports every TSLink-tagged device in the tailnet: services registered here, services registered on other machines, and orphan nodes. A device counts as TSLink-tagged when it carries the configured default tag (`tag:tsmain` unless changed), `tag:tslink-funnel`, or any other `tag:tslink-*` tag.

```bash
tslink list --tailnet
tslink list --tailnet --json
```

Each row says which side of the machine boundary it came from:

| `origin` | Meaning |
|---|---|
| `local_registry` | This machine's `registry.json` holds a service with exactly this hostname |
| `local_name_variant` | The hostname is a `<service>-N` tsnet collision variant of a service registered here, the usual shape of an orphan this machine left behind |
| `unregistered` | This machine's registry knows nothing about the hostname: another machine's service, or an orphan |

The human view ends with `N of M TSLink-owned tailnet devices are not registered on this machine.` and the JSON payload carries `registered_count` and `unregistered_count` alongside `count`. Every result also carries a constant `cleanup_authority` field, because the view exposes a real limit: `tslink cleanup` deletes only devices whose exact NodeID is recorded in this machine's local `node-ownership.json`, so a device this machine's registry does not name must be cleaned up from the machine that created it. `--tailnet` never emits NodeIDs and never deletes anything.

`--tailnet` needs a stored Tailscale API credential (a `tskey-api-*` access token or an OAuth client secret). Without one it fails with `auth_error` (exit 3) and bootstrap guidance in `error.next`; it does not return an empty list. It also conflicts with `--name`, `--type`, `--fields`, and `--verbose` (exit 2), because those filter this machine's registered services while `--tailnet` reports tailnet devices.

### Tailscale SSH as the remote CLI path

If Tailscale SSH is enabled on the machine running TSLink and the tailnet policy has an `ssh` rule admitting you, `tailscale ssh <host> tslink <command>` drives that install from any other tailnet device with no extra software. Both halves are Tailscale-layer configuration: TSLink neither enables Tailscale SSH nor edits the policy, and it never requires either.

To make the first half discoverable, `tslink doctor` reads Tailscale SSH enablement from the local `tailscaled` and prints `Tailscale SSH (this node): <state>`. The JSON payload carries `tailscale_ssh.state` and `tailscale_ssh.acl_rule_required: true`, the latter recording the half no local read can observe.

| State | Finding code | What it says |
|---|---|---|
| `enabled` | `tailscale_ssh_enabled` | `tailscale ssh <this-host> tslink list --json` works once a tailnet ACL `ssh` rule admits the caller |
| `disabled` | `tailscale_ssh_disabled` | Run `tailscale set --ssh` on this machine and add the ACL `ssh` rule to use the remote path |
| `unknown` | `tailscale_ssh_unknown` | The local Tailscale client state could not be read within one second; check `tailscale status` |

All three outcomes are informational. They never change doctor's status or exit code.

## Remote MCP Control Plane

`tslink serve --mcp` serves the same 19 MCP tools as `tslink mcp` over HTTPS on a dedicated tsnet node at `https://<node>.<tailnet>.ts.net/mcp`. It is an MCP endpoint for MCP clients, and there is no page to open in a browser. The node's default hostname is `tslink-mcp`; its tsnet state lives in `~/.config/tslink/mcp-node/`, beside the service nodes rather than among them.

The control plane is off by default. Enable it with the `--mcp` flag or with `mcp.enabled: true` in `config.json`; either one turns it on. `mcp.allow` is required and lives only in `config.json`, because it is the security boundary of the whole feature (`tslink config set` manages only `control-url`, so edit the file directly):

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com", "tag:ops"],
    "node_name": "tslink-mcp"
  }
}
```

| Fact | Detail |
|---|---|
| Default | Off. Without `--mcp` or `mcp.enabled: true`, `serve` opens no control-plane listener and creates no control-plane node |
| Authorization | `mcp.allow` is a list of login emails and/or `tag:` entries, matched against the caller's Tailscale WhoIs identity. An empty or whitespace-only list refuses to start `serve`; it never means "everyone". Every denial is the same `403` JSON-RPC `forbidden` body |
| Reach | The only listener is `ListenTLS` on the control plane's own tsnet node. It is never published through Funnel and never bound to a host interface or `0.0.0.0` |
| Node | Its own dedicated node, shared with no service. It is not a registry service, so it is absent from `tslink list`, and no code path can attach `--funnel` to it |
| Lifetime | Depends on how `serve` is logged in. With a stored credential (`tslink login`) the node is ephemeral: the derived auth key carries the ephemeral capability and tsnet logs in with the ephemeral flag, so a clean daemon stop logs the node out and Tailscale removes it within seconds; disabling `--mcp` leaves no device to delete by hand. After a crash the node lingers until Tailscale's ephemeral garbage collection reclaims it (Tailscale's KB states this normally happens 30 to 60 minutes after the last activity; that figure is Tailscale's, not measured by TSLink). With zero credentials (interactive browser login) the node is persistent and user-owned: one browser authorization survives daemon restarts, and disabling `--mcp` leaves the `tslink-mcp` device in your tailnet until you delete it in the Tailscale admin console |
| Power | An authorized peer has full control: register and remove services, publish a service to the public internet with Funnel, send and revoke real Tailscale invitations. Fill `mcp.allow` with that in mind; `serve` logs `mcp.controlplane.enabled` as a warning on every start |
| Origin | A request that carries an `Origin` header must match the endpoint's own `https://<node>.<tailnet>.ts.net` origin exactly, per the MCP Streamable HTTP transport specification; anything else is `403` before authorization runs. Requests without `Origin`, which is what command-line MCP clients send, pass through |
| Transport | Stateless Streamable HTTP; one request body is bounded at 1 MiB, the same limit the stdio transport applies per record |

**Which clients can reach it.** Only MCP clients running on a machine inside your tailnet, such as Claude Code on your laptop or a server, can connect. Claude Desktop and claude.ai cannot: their remote MCP connections originate from Anthropic's cloud rather than from your device, so they never reach a private tailnet address. Use `tslink mcp` over stdio for those clients when they run on the same machine.

### `tslink mcp` and `tslink serve --mcp` side by side

| | `tslink mcp` | `tslink serve --mcp` |
|---|---|---|
| Transport | stdio, newline-delimited JSON-RPC | Streamable HTTP at `https://<node>.<tailnet>.ts.net/mcp` |
| Network listener | None | TLS listener on a dedicated tsnet node, tailnet-only |
| Authorization | The local user who launched it | `mcp.allow` login emails and/or `tag:` entries, mandatory |
| Configuration | None | `--mcp` or `mcp.enabled`, plus `mcp.allow` in `config.json` |
| Tools | 19 | The same 19, from one tool registry |
| Typical client | An MCP client on this machine | An MCP client on another machine in the tailnet |

## Roadmap / Experimental Packages

The repository contains packages and registry fields for features that are not wired into the shipped `tslink serve` runtime yet. Treat these as roadmap or experimental until end-to-end integration tests are added:

| Area | Current status |
|---|---|
| Docker labels | Not implemented; the registry schema reserves the fields and the runtime rejects them with `feature_unavailable`. |
| Middleware | Not implemented; the registry schema reserves the fields and the runtime rejects them with `feature_unavailable`. |
| Admin dashboard / REST API | No dashboard or REST handler is shipped; the tailnet-only MCP control plane (`tslink serve --mcp`) is the only remote management surface. Future dashboard or REST work must be explicitly experimental and tested end to end. |
| Prometheus `/metrics` | Instrumentation exists, but no scrape endpoint is mounted. |
| Custom domain / ACME | Fields are reserved and rejected with `feature_unavailable`; runtime TLS/ACME listener is not wired. |
| Cluster sync | Not implemented; the registry schema reserves the fields and the runtime rejects them with `feature_unavailable`. |

## Prerequisites

- [Tailscale account](https://tailscale.com) (free for personal use)
- Tailscale installed on the devices you want to access from (phone, tablet, etc.)
- Go 1.26.6+ (if building from source)

## Platform Support

| Platform | Daemon | Auto-start | Stop behavior |
|----------|--------|------------|---------------|
| macOS | `--daemon` | LaunchAgent | Graceful SIGTERM |
| Linux | `--daemon` | systemd user service | Graceful SIGTERM |
| Windows | `--daemon` | Startup folder | Forced process termination |

Configuration and state live in `~/.config/tslink/` on macOS and Linux and in `%AppData%\tslink\` on Windows; set `TSLINK_CONFIG_DIR` to use another directory. Paths written as `~/.config/tslink/` elsewhere in this README mean that directory. On Windows, an older `%USERPROFILE%\.config\tslink\` is moved into `%AppData%\tslink\` the first time TSLink resolves its config directory. If the move is impossible (for example with a redirected profile), TSLink keeps using the old directory; if both directories exist, it refuses to choose and names both.

`credentials.lock` in that directory serializes credential changes between TSLink processes: `tslink login`, `tslink logout` and `tslink doctor --probe-remote` create it, while `tslink serve` and the commands that report credential status create it only when they record metadata for a stored credential that has none yet, or when `serve` finds a legacy `apikey` file. With the system keychain enabled, which is the default, TSLink also takes `.tslink/credentials.lock` in the OS account's home directory (`%USERPROFILE%\.tslink\` on Windows), which follows neither `TSLINK_CONFIG_DIR` nor `$HOME`; both files are empty and stay in place. `node-identities/` holds one record per service with the tags (including the derived `tag:tslink-funnel`), ephemeral setting and control URL its node was started with, so a change to any of them, even one made while the daemon was stopped, clears that node's state and enrolls it again. On Tier 1 (no stored credential) a node advertises no tags, so there only an ephemeral or control URL change does. Once a removed service's `nodes/<name>/` state is gone, whether `tslink remove` or the daemon deleted it (`tslink remove --help` says when), the daemon deletes the service's record on its next sync.

macOS LaunchAgent installs use launchd `KeepAlive` with `ThrottleInterval=30`. If `tslink stop` is run while the LaunchAgent remains installed, launchd will restart TSLink. Run `tslink uninstall` before `tslink stop` when the intent is to disable autostart. When no desktop session exists for the user, `tslink install` first tries `gui/$(id -u)` and falls back to `user/$(id -u)` if the GUI launchd domain is unavailable. Linux headless user services may need `loginctl enable-linger "$USER"` to keep running after logout; if lingering was enabled only for TSLink, run `loginctl disable-linger "$USER"` after uninstall.

Re-running `tslink install` is the supported upgrade path on every platform. On macOS, TSLink saves an existing plist before the launchd handoff and only treats a running daemon as launchd-owned when its pidfile PID matches `launchctl print`. If post-bootstrap verification fails during an upgrade, TSLink restores the previous plist and reloads a previously identified launchd-owned job; it cannot restore an executable binary that was replaced before the command ran. If the same verification fails during a first install, TSLink boots out the new job and removes the new plist only after bootout succeeds. If cleanup cannot finish, the plist is kept so `tslink uninstall` can retry. When an upgrade cannot check a launchd domain, it preserves the previous plist and refuses by default; retry from a desktop session, or run `tslink install --force` only after confirming no job remains in the unavailable domain, because the override may start a second daemon. Uninstall removes the plist only when launchctl reports no real error and every domain is either successfully unloaded or confirms the job already absent. If any domain is unavailable, or if a real launchctl error occurs, uninstall keeps the plist and exits non-zero. `tslink uninstall --force` is the explicit recovery path for domain unavailability: it removes the plist after all addressable domains are unloaded or absent, but a job may remain running in an unavailable domain. When the domain is addressable again, run `launchctl print gui/<uid>/com.tslink.daemon` (or the corresponding `user/<uid>` target) to check it; if the job is loaded, run `launchctl bootout gui/<uid>/com.tslink.daemon` (or the corresponding `user/<uid>` target) to remove it. Real launchctl errors remain fatal with `--force`.

On Linux, TSLink likewise saves an existing systemd user unit before replacing it. If `daemon-reload`, `enable`, `restart`, or post-restart verification fails, TSLink stops the failed service, atomically restores the previous unit, reloads systemd, and restarts a service that was previously confirmed systemd-owned. Neither platform can restore an executable binary that was replaced before `tslink install` ran. Fix the reported cause and re-run `tslink install`.

## Roadmap

- [x] OAuth client secret accepted by login and tsnet auth paths; validate tag/device automation before unattended use
- [ ] Runtime custom-domain / ACME TLS
- [ ] Web dashboard accessible from tailnet
- [ ] Docker image and Docker label discovery
- [ ] Headscale end-to-end testing
- [x] `--json` envelope on every command, plus stdio and tailnet-only MCP transports
- [ ] Integration-tested Layer 2 modules and optional remote/admin surfaces

#### Release Artifacts

There is no public tag/release or populated Homebrew tap yet. Before the first
published release/readback, install from source. After that external gate
passes, GitHub Releases are expected to publish these installable artifacts:

| Platform | Artifacts | Notes |
|---|---|---|
| macOS | Homebrew cask and `tar.gz` archives | The Homebrew cask uses GoReleaser `skip_upload: auto`, so pre-release tags can skip tap upload without failing the release. Use the archives for pre-release validation. Stable macOS binaries are signed with a Developer ID certificate and notarized by Apple; Gatekeeper runs them directly, whether they arrive through the cask or a downloaded archive, provided the first run can reach Apple to check the notarization ticket (a bare binary cannot carry a stapled ticket). Pre-release tags may ship unsigned archives: Gatekeeper blocks those, so use them only for validation. |
| Linux | `.deb`, `.rpm`, and `tar.gz` archives | Packages contain the native `tslink` binary. The first `tslink add` registers and starts the user service automatically. |
| Windows | `.zip` archives | Windows support is archive-only today. There is no MSI/MSIX/Winget package or Windows code-signed installer yet. Use `tslink install` from the extracted binary to register Startup autostart. |

Release assets are side-by-side files, not files embedded inside the archives. GoReleaser uploads installable archives/packages, `checksums.txt`, CycloneDX SBOM sidecars for archives, and keyless Sigstore bundle signatures for `checksums.txt` and SBOM sidecars. The signed `checksums.txt` covers both installable artifacts and SBOM sidecars. The release workflow also publishes GitHub artifact attestations for the installable artifacts and supply-chain sidecars.

#### Verify Release Integrity

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


## Documentation

| Document | What it covers |
|---|---|
| [docs/mcp-clients.md](./docs/mcp-clients.md) | Connecting an MCP client over stdio or HTTP, the 19 tools, and the event-stream contract |
| [docs/cli-manifest.json](./docs/cli-manifest.json) | Generated machine-readable description of every command, flag, exit code, and error code |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | Development setup, local checks, CI scope, and contributor rights |
| [SECURITY.md](./SECURITY.md) | Security model, boundaries, and how to report a vulnerability |
| [COMMERCIAL.md](./COMMERCIAL.md) | Commercial use and voluntary cooperation |
| [CHANGELOG.md](./CHANGELOG.md) | Release history |
| [AGENTS.md](./AGENTS.md) | Operating manual for AI agents working on or with TSLink |

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## Security

For security concerns, please see [SECURITY.md](./SECURITY.md).

## License

TSLink is licensed under the [Apache License 2.0](./LICENSE). Individuals and organizations of any size may use, modify, and redistribute it, including commercially, subject to the license. No TSLink license fee, registration, usage reporting, or size threshold applies.

If TSLink helps your organization, we welcome contributions and inquiries about maintenance, integration assistance, or custom development. Participation is voluntary; paid work and any support commitments require a separate written agreement. See [commercial use and cooperation](./COMMERCIAL.md).

Retain applicable license and attribution notices when redistributing. See [NOTICE](./NOTICE) and [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md). TSLink is an independent project; its license does not grant rights to Tailscale services or imply endorsement. Tailscale agreements and plan eligibility apply separately.

```
Copyright 2026 monody0007
```
