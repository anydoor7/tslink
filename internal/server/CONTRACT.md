# internal/server — gateway, proxy, and supervision

## Gateway / Portal (`door`)

- Default install: persistent home gateway; existing registry/node/runtime/ownership state keeps its mode.
- Private HTTP/file apps: stable routes, own nodes/URLs; PortalOnly opt-in.
- Tier 1 `serve` (no stored credential): empty `AuthKey`, no `AdvertiseTags`, interactive `AuthURL` from `LocalClient.Status()`; `--json` returns `needs_login` immediately.
- Tier 2 credentialed serve: per-service auth material, tagged nodes, hot-reload on registry changes.

## Service flow (runtime)

1. `add` writes registry, verifies/installs supervision, waits for URL or enrollment.
2. Untagged nodes per service when no credential; browser URL per fresh node until `Running`.
3. Optional `login` → Keychain; ACL mutation default-off (`--manage-acl`).
4. Credentialed `serve --daemon`: tagged nodes, registry watch restarts; ownership-safe stale cleanup when permitted.
5. `remove` + proved ownership for remote cleanup.

## HTTP health probes

- Default success: **200–399**, no redirect follow unless configured.
- **499** `client_canceled` when caller aborts.
- **`route_closed` / `node_closed`**: route or node torn down (documented stable semantics).
- Access logs and supervision metrics must reflect probe outcome without leaking secrets.

## HTTP request limits (defaults)

- **32 MiB** upload cap, **10s** header timeout, **30s** upload read **inactivity** (no total body duration cap; backend backpressure excluded), **60s** keep-alive idle.
- Per-service `request_limits` and add/share flags override; `unlimited` requires explicit acknowledgement.
- Response streams and download sizes are **uncapped** (`docs/sharing.md`).

## Hot-reload and nodes

- Tag/ephemeral/control-URL changes restart affected nodes with fresh per-service auth.
- Credential mode swaps and legacy `authkey` file changes require full `serve` restart.

## Supervision invariants

- `daemon_supervision_unverified`: config may remain; inspect `doctor`, `install`, retry.
- Linux user unit may remain installed and retrying after setup failure.
- Windows: task scheduler sign-in autostart + crash backoff vs Startup fallback (no crash restart).
- `supervision.autostart_scope`: launchd/login vs systemd **boot** only with lingering (reported, never auto-enabled).

## Clock seam discipline (`serverNowFn`)

Package-level test seam in `internal/server`.

- **Capture rule**: read `serverNowFn` on the goroutine that spawns work and pass the value down; goroutines that can outlive their caller must **never** read the package seam directly (`t.Cleanup` restores it → data race). Applies to accept loop in `startNodeLocked`, lifecycle ticker, and event stream (`eventStateCache.nowFn`, `mcpEventStream.nowFn` captured once in `newMCPEventsHandler`).
- **Far-from-wall-time rule**: tests inject clocks in **2030** so passes cannot coincide with `time.Now()`.
- **Sync reads**: `Run`, `syncNodes`, `recordOwnedNodeWithBackoff`, `startNodeLocked` read within `Run`'s call tree; tests that start `Run` on a goroutine join it without stubbing the seam.
- **Pinned behaviour**: `TestMCPEventsHandlerCapturesTheClockAtConstruction` swaps seam after construction; frames still use pre-swap clock.

## SSE / MCP events stream

- Named events only: `snapshot`, `update`, `keepalive` — never default `message` / `onmessage`.
- No `retry:` field; no `Last-Event-ID` replay (reconnect gets full snapshot).
- `id:` is instance-global **`event_id`** (distinct from per-stream `sequence`); dedupe on `event_id`; drop cached state when `instance` changes.
- `mcpLogsTailscaleURLPattern`: delimiter-separated capability tokens from host are not redacted (`SECURITY.md`).

## Dependencies (pinned intent)

- `tailscale.com` tsnet + LocalClient; REST v2 for ACL/devices/auth keys; fsnotify registry watch.
