# Changelog

All notable changes to TSLink are documented here. The public compatibility
surface for the 0.x series is defined below; TSLink is a CLI, not a Go library.

## [0.1.0] - 2026-10-06

First public release. TSLink gives each app on your computer or server, whether a web app, a folder or file, or a TCP port, its own private Tailscale address, and controls who can reach it: people you name, browser guests with an expiring link, or, only when you ask for it, the public internet. Run it from the CLI or through an AI agent over MCP. TSLink works with Tailscale and is an independent project.

### Install

- Prebuilt archives for macOS, Linux and Windows (amd64 and arm64), `.deb` and `.rpm` packages for Linux, and a Homebrew cask for macOS and Linux: `brew install --cask anydoor7/tap/tslink`.
- macOS binaries are signed with a Developer ID certificate and notarized by Apple. Windows archives are not code-signed.
- `checksums.txt` and the SBOMs carry Sigstore signatures, and every release asset has a GitHub build provenance attestation. See [Verify a release](https://github.com/anydoor7/tslink/blob/v0.1.0/docs/verify-release.md). Building from source needs Go 1.26.6 or newer.

### Share your apps

- `tslink share 3000`, `tslink share ./photos` or `tslink share ./report.html` gives a web app, folder or single file its own HTTPS address in your tailnet. `tslink add <name> --tcp <port>` does the same for a TCP service. Each app runs as its own embedded Tailscale node inside one background daemon.
- `tslink install` starts the daemon at login: a LaunchAgent on macOS, a systemd user service on Linux and a per-user scheduled task on Windows (`--startup` uses the Startup folder instead).
- Recipes for 15 self-hosted apps, including Home Assistant, Jellyfin, Immich, Nextcloud, Open WebUI and Ollama, plus a generic web recipe. `tslink apps detect` finds apps listening on this machine, and `tslink apps share <recipe>` previews a registration that `--yes` applies.
- Per-app request limits (`--max-request-body` and timeouts) for large or slow uploads. The default body limit is 32 MiB.

### People, guest links and public access

- People: `tslink people add alice@example.com --apps photos,finance --for 7d` lets that Tailscale login open the chosen web and file apps until the deadline. `people update`, `extend` and `people remove` change or revoke access. `--invite --print-links` adds device invitations for someone outside your tailnet (this needs a user-owned API token), and `--qr` prints a QR code for phone setup.
- Guest links: `tslink guest create photos --for 3d --public --print-link` creates an expiring browser link, optionally with a PIN, for a web app. Guests need no Tailscale account. The link goes through Tailscale Funnel behind a mandatory guest gate. Anyone holding the link can use it; TSLink stores only its hash.
- Public access: an open Funnel needs `--funnel --public`, works for web apps only and always expires, after 24 hours by default and at most 7 days unless you raise `durations.public_max`.
- Durations: every share and access lifetime uses one grammar, such as `90m`, `36h`, `3d`, `1w`, `1d12h` or `until 2030-06-01`. The minimum is one hour. Only tailnet members can be given `never`, and only with `--ack-never`.

### Portal and access requests

- `tslink portal enable --owner you@example.com` starts a private home page on its own tailnet node. Each visitor sees the apps they may open, with their health and deadlines.
- People in your tailnet can ask for an app, or for more time, from the portal. Only apps registered with `--requestable` are offered. `tslink requests approve <id> --for 3d` or `tslink requests deny <id>` decides, and a new request can trigger an alert.

### Health, alerts and access history

- The daemon checks each app's backend (HTTP status and an optional body match, or a TCP connection) and reports `healthy`, `degraded`, `down` or `unknown` in `status`, `list --verbose`, `doctor` and the portal. `status` and `doctor` warn 14 days and 3 days before a node key expires.
- Optional alerts for an app going down or recovering, an approaching expiry and new access requests run a command or call a webhook configured in `alerts.json`.
- `tslink access log` shows who opened which app and when, including denied requests, with summaries per person and per app. By default only the first path segment is recorded; `tslink access path` changes that per app.

### Agents and automation

- Every command except `tslink mcp` accepts `--json` and returns a versioned `tslink.result` envelope. `tslink manifest --json` lists commands, flags, exit codes and error codes.
- `tslink mcp` is an MCP server over stdio. `tslink serve --mcp` adds a remote MCP endpoint on its own tailnet-only node; it is never published through Funnel.
- Scoped roles `viewer`, `app-operator` and `people-manager` limit an agent to listed apps, a fixed set of tools and a maximum grant duration, while `owner` keeps full control. Use `tslink mcp --scope` locally, or `mcp.bindings` for remote callers, who are identified by Tailscale login or tag. `tslink mcp-audit` reads the journal of changes made through MCP.

### Security defaults

- Apps stay private to your tailnet unless you create a guest link or publish through Funnel. TCP services are never public.
- Registration refuses link-local and cloud metadata targets (`link_local_target_refused`) and file shares that would expose TSLink's config directory (`path_exposes_config_dir`).
- The proxy removes client-supplied `Tailscale-*` and `X-Tailscale-*` identity headers before adding the caller's identity from Tailscale.
- `tslink remove` and cleanup delete a tailnet device only when TSLink recorded that exact device as its own.
- On macOS and Linux, daemon logs are owner-only (mode 0600). Guest PINs are checked against salted PBKDF2 hashes, with attempt limits per link and per source address.
- Over MCP, invitations with a role above member, or that allow exit-node use, need the owner's `mcp.allow_elevated_invites` opt-in.

### Not in this release

- Multi-host inventory is planned. Today each host runs its own TSLink, and the portal lists that host's apps.
- There is no admin dashboard or REST API, Docker image, custom domain support, middleware or Prometheus endpoint. See the [roadmap](https://github.com/anydoor7/tslink/blob/v0.1.0/docs/roadmap.md).

### Compatibility (0.x)

For later 0.x releases, these are the public automation and data contracts:

- `--json` keeps the `tslink.result` envelope and its `type`, `ok`, `schema_version`, `command`, `code`, `data`, and `error` fields. Optional `data`, `error`, and `error.next` stay optional; fields may be added.
- Documented command `data` fields keep their meaning; fields and warnings may be added. Public view `data.schema_version` stays an integer.
- Published error codes keep their meanings and exit-code mapping. Codes may be added; exit classes 0, 1, 2, 3, 4, 5, 64, and 65 stay as documented. `tslink manifest --json` derives `error_codes` from this mapping.
- `registry.json` schemas 1 and 2 and known `config.json` keys remain readable; fields may be added. Run `registry check` before an upgrade.
- MCP tool names and output schemas remain available with additive fields; tools and optional fields may be added. Refusals remain tool results with `isError: true`, one JSON text failure object (`code`, `message`, `next`, optional `data`), and no `structuredContent`; schema argument errors use `usage_error`. The four behavior annotations remain present on every tool.
- Human output, error messages, and logs are not parsing interfaces. Use `--json` instead. `runtime.json` and other daemon/CLI state files are private.
- Go packages are not a library API. A future incompatible 0.x change needs a migration note; private pre-release behavior is not a baseline.
