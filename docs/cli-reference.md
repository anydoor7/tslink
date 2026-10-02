# Cli Reference

## People

`tslink people add <login-or-email> --apps photos,finance|all [--for 7d] [--invite] [--print-links]` grants private HTTP/file access and produces a recipient guide. `all` selects current private HTTP/file apps, excluding TCP/Funnel. `--for` uses the [unified duration grammar](durations.md); `--until` sets an absolute deadline. New grants default to 24h. `never` requires `--ack-never` and a tailnet-member login. `--invite` creates single-use per-app device invite links, requiring a user-owned API token. Links are masked unless explicitly requested with `--print-links`.

`tslink people list [--json]` reports people, app grants, absolute expiry, active state and revoked tombstones. `tslink people update <who> [--apps list|all] [--for duration|never] [--invite] [--print-links]` requires apps, expiry or invite; omitted values retain their current setting (new apps default to 24h). `tslink people remove <who>` revokes local HTTP/file access everywhere, including matching legacy allow entries. All use the versioned JSON envelope; add/update return partial invitation failure as `data.complete: false` without discarding grants. See [people](people.md) for enforcement, existing WebSocket connections, clock changes and schema 2 downgrade rules.

People logins accept any nonempty valid UTF-8 string without control characters or internal whitespace. Invalid UTF-8 uses `usage_error`; malformed WhoIs identities deny access. Trim outer ASCII space/tab/CR/LF/VT/FF; lowercase ASCII A-Z only, without Unicode folding or normalization, and compare bytes exactly. Punctuation and non-ASCII addresses work; Unicode lookalikes stay distinct. `people update --invite --replace-invite app=recorded-old-id` confirms replacement after remote absence, preserves grants/deadlines, and cannot combine with `--apps` or `--for`; MCP uses `replace_invites`. Confirmed deleted nodes end cleanup as `target_gone`, preserving evidence. Absence requires HTTP 200 with a nonempty body and a present non-null device/invite array; other 2xx, blank/null and malformed lists defer cleanup/reconciliation, retire nothing and send no replacement POST. Update accepts `--invite` alone to resume unfinished work. Update/remove accept repeatable `--reconcile-invite app=id|none` after owner verification of an unknown POST outcome. Removal saves local denial first, then reports `complete`/`cleanup` for remote pending invites; no-token removal defers cleanup. `access explain`/`access_explain` include redacted people policy and point to `people list`. See the people guide for durable states and the absence of an exactly-once guarantee.

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
| `tslink install` | Auto-start on login (macOS LaunchAgent / Linux systemd / Windows Task Scheduler) |
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

Without `--recipe`, `tslink add` with an existing name replaces that service: flags you do not repeat (`--allow`, `--tags`, `--funnel`, ...) are dropped. The JSON result lists `replaced_fields` and warns when access or the node identity changed.

| Flag | Description |
|------|-------------|
| `--proxy host:port` | Reverse proxy to a local HTTP service |
| `--preserve-host[=false]` | Proxy only: forward this node's trusted canonical external Host; ordinary add/share default false, recipes supply their own default. Explicit false overrides a recipe. |
| `--dir /path` | Serve a local file directory |
| `--tcp host:port` | Raw TCP forwarding |
| `--dry-run` | Validate and print the service without saving it |
| `--ephemeral` | Ephemeral node, auto-removed from tailnet when stopped |
| `--tags tag:a,tag:b` | ACL tags for Tailscale network policy |
| `--allow user@,tag:x` | HTTP access control for proxy/file services; rejected for TCP because raw TCP uses Tailscale ACL tags and target-service auth |
| `--control-url URL` | Per-service control server override, e.g. Headscale. TSLink never sends an auth key minted from a stored Tailscale credential to another control server: with such a credential stored, the service is refused with `credential_control_url_mismatch` |
| `--funnel` | Expose via Tailscale Funnel (public internet, proxy only, requires `--public`) |
| `--public` | Explicitly acknowledge public internet exposure for `--funnel`; invalid without `--funnel` |
| `--funnel-ttl <lifetime>` | Relative or `until <date/time>`; presets 1h, 8h, 24h, 3d, 7d; min 1h, default 24h, default max 7d; never refused; requires `--funnel` |
| `--no-auto-provision` | Disable Funnel policy provisioning for this service; requires `--funnel` |
| `--no-daemon-install` | Save configuration without installing or starting the daemon |
| `--health-path /ready` | HTTP business probe path joined to the proxy backend base path (default `/`) |
| `--health-status-min N`, `--health-status-max N` | Expected HTTP status range; default 200..299, proxy only |
| `--health-body text` | Expected substring in first 64 KiB; proxy only, omitted by default |
| `--health-timeout duration` | I/O timeout after worker admission, 100ms..30s; default `5s` |
| `--health-interval duration` | Probe interval, 10s..1d and at least timeout; default `1m` |
| `--wait duration` | Wait for a URL or enrollment URL; default `30s`, `0` disables waiting |
| `--json` | Print the versioned result envelope |

App health (`healthy`/`degraded`/`down`/`unknown`), observation timestamps and consecutive failures appear in status, list JSON, `list --verbose`, MCP and `/events`. Node-key and credential expiry warnings use 14-day and 3-day thresholds with next steps; metadata sources remain explicit. `doctor` adds a fresh HTTP business probe and makes the 3-day expiry warning critical (exit 65). Owner notifications are opt-in through `alerts.json`; events and restart dedup state are persisted by default. See [health and alerts](health-and-alerts.md).

HTTP/TCP health checks enforce the registry's target-safety rules. Node-key expiry refreshes independently of `--health-interval` and is invalidated when a node is replaced. Each service has one read per pool; queue admission has a separate five-second bound, and unattempted checks preserve failure counts and observation timestamps. Stuck pools report `alerts.monitor_error=health_monitor_saturated` with monitor saturation/recovery events and a doctor warning. Ready results persist in batches; unchanged state does not write. Status and doctor read events and monitor state from the durable journal; snapshots may only supplement write errors. Notification delivery uses a bounded queue; commands have a 10-second deadline plus up to 250 ms of pipe cleanup, and cancellation counts as failure.

### Application recipes

| Command or flag | Behavior |
|---|---|
| `tslink apps list` | Versioned recipe catalog, including configuration snippets, safety policy and dated official docs |
| `tslink apps detect` | Credential-free loopback HTTP fingerprints of OS TCP listeners; confidence and existing registrations |
| `tslink apps share <id>` | Preview one recipe using its recommended name and local target |
| `tslink add [name] --recipe <id>` | Same recipe preview, with an optional name override |
| `--yes` | Apply the recipe plan; existing names are kept unchanged |
| `--dry-run` | Preview only, even when `--yes` is present |
| `--proxy host:port` | Override the recipe's loopback HTTP(S) host target |
| `--name name` | Name override on `apps share`; `add` uses its positional name |
| `--force-unsafe-public` | DANGER: override a `never_public` recipe; requires `--funnel --public` and can expose host control or private data to everyone |

Recipes support `--allow`, `--tags`, `--ephemeral`, `--control-url`, the existing Funnel acknowledgement/TTL/provisioning flags and `--no-daemon-install`. `add --recipe` rejects `--dir`, `--tcp` and `--wait`; poll `tslink url` after applying. Without `--recipe`, ordinary `add` retains its replacement behavior and rejects recipe-only flags.

The plan/apply JSON data includes `recipe`, `requested`, `service`, `action`, `dry_run`, `applied`, `warnings` and `next`. `applied=true` means this invocation created the service; `skip_existing` reports its preserved configuration. Detection includes `listeners`, `matches`, `complete` and `warnings`. A partial scan sets `complete=false`. New recipe services default to the catalog health path. `apps share` and `add --recipe` accept `--health-*` and request-limit overrides; MCP `recipe_plan`/`recipe_apply` accept `health` and `request_limits`. Reuse keeps existing configuration, including people scope and grants.

MCP tools: `recipe_list`, read-only `apps_detect`, `recipe_plan` and `recipe_apply`; plan/apply take `recipe_id`, optional `name`/`target`, string `allow`/`tags`, and snake_case counterparts of the flags above. Call the plan before apply. Existing generic `template` commands and `template_list/plan/apply` tools continue to work. See [application setup and limitations](apps.md).

`share <port|host:port>` also accepts `--preserve-host` (default false); file/directory shares reject true. With preservation enabled, Host and X-Forwarded-Host use this node's canonical external DNS name, never a client-supplied authority. The first runtime certificate domain takes precedence over the node's DNS FQDN, matching the shared HTTPS URL, including Funnel. The name is lowercase with no terminal dot or port. If it is unavailable or invalid, HTTP 503 returns `canonical_host_unavailable` without contacting the backend. Client aliases and unexpected authorities are forwarded under the canonical name, with no 421 rejection. Default mode keeps upstream Host rewriting and its existing incoming-authority X-Forwarded-Host behavior. X-Forwarded-Proto/For come from the actual request in both modes; Origin is unchanged. Reuse with a different Host policy reports a conflict. Existing services and generic templates keep upstream Host rewriting. Registry `preserve_host` is an optional proxy boolean; absent means false. Recipes can be overridden with `--preserve-host=false` or MCP `preserve_host:false`. Status/list service projections and `access explain` report the configured policy. In global-failure status without a readable registry, the Host policy is unknown and omitted.
### HTTP request limits (add and share)

| Flag | Default | Meaning |
|------|---------|---------|
| `--max-request-body 20GiB` | `32MiB` | Maximum upload size; accepts positive integer bytes, B, KiB/MiB/GiB/TiB or decimal KB/MB/GB/TB |
| `--ack-unlimited-request-body` | false | Required with `--max-request-body unlimited`; explicitly removes the body cap |
| `--request-header-timeout 20s` | `10s` | Maximum time to receive request headers |
| `--request-read-timeout 2m` | `30s` | Maximum inactivity while reading an upload; continuing uploads have no total-duration deadline |
| `--idle-timeout 90s` | `60s` | Idle time between HTTP keep-alive requests |

All timeout overrides are positive Go durations (for example `30s`, `2m`).
Settings apply to proxy and file services; raw TCP rejects HTTP request limits.
An add replacing a name resets omitted limits to defaults, like other add flags.
A share only reuses a service with equivalent effective limits.
Limit conflicts name the differing flags and their existing/requested values;
use add with the complete service configuration to reconfigure them.
Unused or rejected HTTP/1 bodies have an absolute cleanup deadline of at most
1s, shortened by `--request-read-timeout` when below 1s. Incomplete cleanup closes
the connection without imposing a total timeout on accepted uploads.

`add --json`, `share --json`, `status --urls --json` and `list --verbose --json`
report `request_limits` with `max_body_bytes`, `header_timeout`, `read_timeout`
and `idle_timeout`. `max_body_bytes:-1` means acknowledged unlimited.
Human `status --urls` and `list --verbose` show the effective limits too.
The envelope remains schema version 1. MCP add/share accept the optional object
`request_limits: {"max_body":"20GiB","read_timeout":"2m"}`; unlimited requires
`{"max_body":"unlimited","unlimited_ack":true}`. Omitted fields inherit defaults.

Body limits return 413; stalled uploads or incomplete headers return 408.
Structured logs name the service, limit, status and code (`request_body_limit`,
`request_read_timeout`, `request_header_timeout`). The first hit of each limit
is retained in the current node's runtime warnings; status and verbose list show
it, and doctor suggests the corresponding flag. Warnings reset when that node
restarts. A backend may already have received part of a rejected streaming body.
Its own upload limits and any public relay limits still apply.

Windows `tslink install --startup` uses the Startup fallback for the next sign-in, without crash restart. Default `install` uses Task Scheduler to launch a built-in supervisor and verifies immediate startup. `stop` stops both processes, including during crash backoff; `install` resets a tripped crash-loop breaker. See [daemon lifecycle](daemon-lifecycle.md#windows-supervision-and-migration).

## Change a deadline

`tslink extend <service> [--person <login>] (--for <lifetime> | --until <date/time>) [--regrant] [--ack-never]` changes one person grant or Funnel TTL. A relative duration is measured from the operation time and can shorten or extend. Expired grants need `--regrant`; revoked people remain revoked. It always returns a versioned JSON envelope. MCP `extend` uses `service`, `who`, `for`/`until`, `regrant` and `ack_never`. See [durations](durations.md) for DST, config validation, policy API and the syntax of health/timeout/keepalive flags, and [Funnel](funnel.md) for public expiry.
