# Cli Reference

## People

`tslink people add <login-or-email> --apps photos,finance|all [--for 7d] [--invite] [--print-links]` grants private HTTP/file access and produces a recipient guide. `all` selects current private HTTP/file apps, excluding TCP/Funnel. `--for` accepts a positive duration (including days) or `never`; omission creates no expiry. `--invite` creates single-use per-app device invite links, requiring a user-owned API token. Links are masked unless explicitly requested with `--print-links`.

`tslink people list [--json]` reports people, app grants, absolute expiry, active state and revoked tombstones. `tslink people update <who> [--apps list|all] [--for duration|never] [--invite] [--print-links]` requires apps, expiry or invite; omitted values retain their current setting (new apps have no expiry unless specified). `tslink people remove <who>` revokes local HTTP/file access everywhere, including matching legacy allow entries. All use the versioned JSON envelope; add/update return partial invitation failure as `data.complete: false` without discarding grants. See [people](people.md) for enforcement, existing WebSocket connections, clock changes and schema 2 downgrade rules.

People identities use ASCII `[A-Za-z0-9@._+-]+`, ASCII case folding and outer ASCII whitespace removal; Unicode is rejected. Update accepts `--invite` alone to resume unfinished work. Update/remove accept repeatable `--reconcile-invite app=id|none` after owner verification of an unknown POST outcome. Removal saves local denial first, then reports `complete`/`cleanup` for remote pending invites; no-token removal defers cleanup. `access explain`/`access_explain` include redacted people policy and point to `people list`. See the people guide for durable states and the absence of an exactly-once guarantee.

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
| `tslink doctor` | Diagnose credentials, daemon, registry, runtime snapshot, exposure, target safety, and Tailscale SSH enablement; may record missing credential metadata |
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

A missing implicit default registry is valid on first run. An explicit missing
`registry check <path>` reports `not_found` (exit 5). Malformed registry JSON,
field types, or trailing data report `usage_error` (exit 2), naming the path
and repair guidance. `list`, `status`, and `doctor` show bad entries beside
healthy services; fix or remove bad entries before changing the registry.

### Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | General runtime error |
| `2` | Usage, argument, or flag error |
| `3` | Authentication or authorization error |
| `4` | Conflict, such as an already-running daemon |
| `5` | Requested resource not found |
| `64` | Diagnostic warning threshold |
| `65` | Diagnostic critical threshold |

### Add Command Flags

`tslink add` with an existing name replaces that service: flags you do not repeat (`--allow`, `--tags`, `--funnel`, ...) are dropped. The JSON result lists `replaced_fields` and warns when access or the node identity changed.

| Flag | Description |
|------|-------------|
| `--proxy host:port` | Reverse proxy to a local HTTP service |
| `--dir /path` | Serve a local file directory |
| `--tcp host:port` | Raw TCP forwarding |
| `--dry-run` | Validate and print the service without saving it |
| `--ephemeral` | Ephemeral node, auto-removed from tailnet when stopped |
| `--tags tag:a,tag:b` | ACL tags for Tailscale network policy |
| `--allow user@,tag:x` | HTTP access control for proxy/file services; rejected for TCP because raw TCP uses Tailscale ACL tags and target-service auth |
| `--control-url URL` | Per-service control server override, e.g. Headscale. TSLink never sends an auth key minted from a stored Tailscale credential to another control server: with such a credential stored, the service is refused with `credential_control_url_mismatch` |
| `--funnel` | Expose via Tailscale Funnel (public internet, proxy only, requires `--public`) |
| `--public` | Explicitly acknowledge public internet exposure for `--funnel`; invalid without `--funnel` |
| `--funnel-ttl 1h\|8h\|24h\|72h\|7d\|never` | Public Funnel lifetime; default `24h`; requires `--funnel` |
| `--no-auto-provision` | Disable Funnel policy provisioning for this service; requires `--funnel` |
| `--no-daemon-install` | Save configuration without installing or starting the daemon |
| `--wait duration` | Wait for a URL or enrollment URL; default `30s`, `0` disables waiting |
| `--json` | Print the versioned result envelope |
