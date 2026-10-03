# Access history

TSLink works with Tailscale and is an independent project. Local access history helps an app owner check who opened an app and when, including requests denied after a person's grant expired.

```sh
tslink access log --app photos --since 24h
tslink access log --who alice@example.com --decision denied --limit 50 --json
tslink access log --since 2030-01-01T00:00:00Z --until 2030-01-02T00:00:00Z --json
```

`--who` matches an account login (ASCII case-insensitive), an exact node name, or a tag. `--since` accepts a positive Go duration such as `24h`, or an RFC3339 timestamp; `--until` accepts RFC3339. Both timestamp bounds are inclusive. Events are returned newest first. The default limit is 100, with a maximum of 10,000. Summaries cover every matching retained event, even if the returned list is truncated. They include counts per person and per app, allowed/denied counts, and each app's last allowed access. A denial does not advance last seen. TCP contributes two events per connection; counts are event counts, not unique people or visits.

The CLI uses the usual versioned result envelope, with `events`, `summary`, and `truncated` under `data`. The read-only MCP tools `access_log` and `access_summary` accept the same filters and return these payloads directly. Both declare `readOnlyHint`; they require no credential, contact no remote service, and create no files or locks. Viewer, app-operator and people-manager scopes may query explicit permitted apps. Filtering happens before counts and event limits. Inventory-only viewers have no access-history app grants. Global or mixed-app receipts are omitted unless every affected app is in scope.

## Recorded metadata and privacy

Each HTTP request that reaches the service handler produces one `kind: http` event. File services use the same chain. TCP emits `tcp_open` and `tcp_close`, with a connection ID, timestamps, identity and byte counts. Events carry schema version 1, UTC time, app/service, method, status, bytes in/out, duration in milliseconds, allowed/denied decision, optional denial reason and optional matched grant (`person` or `legacy_allow`). The combined query also includes `mcp` mutation receipts, `lifecycle` authority changes and `guest` link decisions.

Identity is attested by WhoIs: account login, node name and tags. Tagged nodes have no account login. Public Funnel connections use the `public` login and never retain an IP address. Tailnet connections to the same listener retain their WhoIs identity. Classification uses the trusted `ipn.FunnelConn` transport marker, including TLS/HTTP2 and TSLink connection wrappers; headers cannot select an identity. An unknown private caller has only a coarse IPv4 /24 or IPv6 /48 prefix. Identity enrichment for otherwise unprotected requests occurs in the background with a 250 ms lookup budget; logging does not add an identity lookup to request serving. People/ACL decisions supply their fresh authorization identity to the record.

The default path mode is `prefix`: only the first nonempty path segment is recorded, so `/album/private/item` becomes `/album`. Decoding happens before splitting: encoded `/` (`%2F`, `%2f`), backslashes (literal or encoded), and nested escapes are treated conservatively as separators. Malformed or excessively nested escapes are redacted. Token-shaped segments become `[redacted]`. Query strings and fragments are removed. The main purpose is to show who opened which app.

`full` explicitly opts into all sanitized segments. **Full mode may store app-specific bearer paths or other sensitive names:** TSLink cannot recognize every application's capabilities. TSLink's own reserved bearer routes (`guest`, `guests`, `invite`, `invites`, `g`, `link`, `links`, plus `auth` and `token`) always redact subsequent segments, including encoded separators. Future guest routes must use these reserved names or extend this sanitizer before shipping. There are no header, cookie, body, credential or full-URL fields. The typed contract cannot make an arbitrary producer's free-form text safe; integrations must pass identifiers and stable codes only. A short opaque capability used as the first segment may not look like a token; use `off` for such apps.

```sh
tslink access path photos prefix
tslink access path docs full     # opt in only after inspecting the app's URL design
tslink access path sensitive off
tslink access path photos inherit
tslink config set access-log-path-mode prefix
```

Service JSON uses `access_log_path_mode`; global JSON uses `access_log.path_mode`. Modes are `prefix`, `full`, and `off`. A service mode overrides an inherited global `prefix` or `full`; global `off` is a hard opt-out. Legacy keys still work: global `record_path: false` / `config set access-log-path false`, or service `access_log_path: false` / `access path <app> false`, map to `off` and take precedence over modes. Legacy `true` and omitted/null inherit the new mode, defaulting to `prefix`; they do not implicitly opt into `full`. `access path <app> inherit` clears both service keys; explicit mode commands clear the legacy service boolean.

Per-app changes reconcile through the registry watcher; global options take effect after restarting `serve`. Disable HTTP/TCP/guest-use recording with `tslink config set access-log-enabled false`. Existing retained history remains queryable until it is evicted. Mutation receipts remain enabled independently; their bounded journal is required for MCP authority changes.

`acl` denotes TSLink's legacy HTTP allow-list; `people` denotes people authorization or an unavailable people registry/identity; `expired` denotes a matched person's expired grant; `limits` denotes HTTP upload/read protections or a TCP connection-cap refusal; `preserve_host_unavailable` denotes the canonical-host refusal. An app's own 403 is still an allowed gateway request. Tailnet-policy packets and incomplete headers rejected before HTTP handler dispatch cannot become HTTP request records. F2 health probes contact the local backend directly and do not enter this chain; a person opening `/health` through the gateway is logged normally.

HTTP byte counts measure body bytes actually read and response bytes written, excluding HTTP headers and framing. They do not infer the unread part of an early-rejected upload. Upgraded HTTP connections count plaintext stream bytes after hijack, including the upgrade response handshake. TCP counts actual stream bytes. Long-lived streams publish their completed HTTP/close event when the handler/connection finishes.

## Bounded local storage and health

History lives under the config directory in `access-log/YYYY-MM-DD-NNNNNN.jsonl`, with POSIX 0700/0600 modes using TSLink's existing helpers. Windows inherits directory ACLs from the owner's config directory; TSLink does not claim to validate a user-only DACL. Only one daemon writer may own that directory; a second writer refuses its lock without waiting. CLI/MCP reads never change permissions or repair files.

Defaults and validated configuration:

| CLI config key | `config.json` key under `access_log` | Default | Valid values |
|---|---|---|---|
| `access-log-enabled` | `enabled` | true | true/false |
| `access-log-path` | `record_path` | inherit | legacy true/false |
| `access-log-path-mode` | `path_mode` | prefix | prefix/full/off |
| `access-log-retention-days` | `retention_days` | 30 | 1–3650 |
| `access-log-max-bytes` | `max_bytes` | 67108864 | 65536–1073741824 |
| `access-log-queue-size` | `queue_size` | 1024 | 1–65536 |

An empty CLI value resets the option. Omitted numeric JSON keys, or zero, select the default. Unknown keys, invalid types, negative numbers and values outside the bounds are refused on config load/save. Service keys are `access_log_path_mode` and the compatible `access_log_path` boolean.

Segments rotate at at most 1 MiB (or one quarter of a smaller size cap), and oldest segments are deleted first to maintain the cap. Retention counts UTC calendar days including today: 30 means today plus the previous 29 dates. Eviction runs on startup, before append and every 250 ms even while idle. The cap covers JSONL data; a small health file and writer lock are additional.

Appends are one newline-delimited JSON write followed by file sync; a newly created segment also syncs its directory on macOS/Linux. Startup removes an incomplete final record before new appends. Complete corrupt records produce an explicit unhealthy state and dropped events; queries return an error rather than silently hiding corruption. Windows flushes file contents using `File.Sync`; directory syncing follows the existing atomicfile Windows boundary. Abrupt process death can lose events still in the memory queue, and hard power loss has the OS/filesystem's durability limits.

Serving only enqueues into a bounded queue. A full queue drops the event immediately. Disk errors also count as drops and preserve request serving. The daemon keeps a stable writer handle even when initialization fails. Existing listeners use that same handle after initialization is retried on each lifecycle tick (normally 30 seconds); recovery does not require restarting the listener. Requests during the failure still serve and are counted as missing records.

`status` (including `--urls`) and `doctor` read access-log health from `runtime.json` for the current daemon PID/start-time window, independently of the access-log directory. They expose `current`, `enabled`, `last_write`, `drops`, `size_bytes`, `updated_at`, stable `error`, and `missing_history` windows (`start`, optional `end`, `reason`). No `end` means initialization is still unavailable; a closed window remains visible after recovery. Current counters begin with each daemon instance. A stale/missing/incompatible runtime snapshot reports `access_log_runtime_unavailable`, and never substitutes an older successful health file. A stopped daemon's health file is historical (`current: false`). If the config directory itself cannot be read/written, current health cannot be proven; consumers report it unavailable. Doctor warns on known drops, missing windows or I/O failure.

The store separately publishes its historical health file every 250 ms and at drain, even while append/enrichment stalls; the existing daemon health monitor publishes changed current runtime health. Neither snapshot proves complete history after a crash. Shutdown gives draining a bounded one-second wait. Logs stay local; there is no external shipping or flow-log integration.

## Writer contract for integrations

`internal/accesslog.Writer.Record(Event) bool` is the shared producer seam. It is nonblocking; false means a counted drop. Producers pass typed metadata, a supported `kind`, a decision and stable reason, never a payload or credential. The store assigns a missing UTC event timestamp and applies common sanitization. `Store.RecordResolved` is the daemon's optional asynchronous identity-enrichment seam; its callback must be bounded and capture only identity/address metadata. Integrations obtain the owning daemon writer through `Server.AccessLogWriter`; only the owning daemon should create a store. Read-only tools call `Query`, without creating a writer.


HTTP fields retain their HTTP meaning; audit data uses optional typed `mcp` and `guest` objects. MCP has `principal` (login, tag, or F6's `local-user:<OS user>`), `role` (legacy `scope` is also accepted), caller `identity` (`login`, `node`), `capabilities` (`role`, `apps`, `inventory`, `max_duration`), `tool`, `apps`, `result` (`status`: `ok`, `denied`, `error`; `code`: stable code), optional scope expiry, audit ID and `phase` (`intent`/`completion`, with legacy `started` accepted). A guest object has non-secret `link_id`, `app`, `decision` (`allowed`/`denied`) and a stable `reason`, including allow reasons; there is no token field. HTTP authorization reason sanitization does not discard audit reasons. Producer-owned slices are copied before queuing. MCP app filters and summaries include every app in the operation, while the overall count increments once per event rather than once per app. Intent and completion receipts are separate events.

F6's `mcpaudit.Entry` maps without reusing HTTP/identity fields:

| F6 entry | Shared event |
|---|---|
| `Kind` | `kind` (`mcp` or `lifecycle`) |
| `Surface`, `Changes` | `surface`, `changes` (typed action, app, subject, request/link ID, previous/new expiry) |
| `ID`, `Time`, `Principal`, `Role` | `mcp.id`, `time`, `mcp.principal`, `mcp.role` |
| `Identity`, `Phase` | `mcp.identity` (login/node), `mcp.phase` (intent/completion) |
| legacy `Who`, `Scope` | `mcp.principal`, `mcp.role` |
| `Capabilities`, `ScopeExpiresAt` | `mcp.capabilities`, `mcp.scope_expires_at` |
| `Tool`, `Apps` | `mcp.tool`, `mcp.apps` |
| `Result == ok` | `mcp.result = {status: ok, code: ok}` |
| `Result == denied` | `mcp.result = {status: denied, code: denied}` |
| `Result == mcp_scope_denied` | `mcp.result = {status: denied, code: mcp_scope_denied}` |
| `Result == started` | `mcp.phase = intent` (legacy started), `mcp.result = {status: ok, code: started}` (intent, not completed success) |
| other `Result` stable code | `mcp.result = {status: error, code: <original code>}` |

`Query` reads both access-log segments and the bounded `mcp-audit.json` journal. CLI and stdio processes write only to the journal; they never open a second daemon access-log writer. Each intent and completion remains a separate event, correlated by `mcp.id`. Summary counts include lifecycle receipts and intents, so they are not visit counts or counts of successful mutations.

Approvals and denials record `request_approved`/`request_denied`; extensions record `extended` with old/new deadlines. Expiry latches record `grant_expired`, `guest_expired` or `funnel_expired` once after a successful registry write. Guest use goes through the owning daemon writer with the non-secret `guest.link_id`; revocation records `guest_revoked`. The surface is `cli`, `mcp`, `scoped_mcp`, `guest` or `lifecycle`. CLI actors are `local-user:<OS user>` and expiry uses `system:expiry`. MCP changes are attached to the existing completion receipt, preserving the authenticated caller, effective scope and correlation ID. No visitor note, owner reason, PIN, token or link is copied into a change record.

The mutation journal rotates separately at 1,024 entries or 1 MiB. Its retention does not follow the HTTP log's day/byte settings. It uses bounded lock waits and atomic replacement; corrupt or unsafe journals cause an explicit query error. Registry publication and its lifecycle receipt are separate commits. A post-commit audit failure reports that the authority change committed; it does not undo that change. A crash between those commits can leave an audit gap. An MCP intent without completion likewise means an unknown outcome. Daemon access-log health describes its own queue/segments, not this separate mutation journal.
