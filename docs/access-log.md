# Access history

TSLink works with Tailscale and is an independent project. Local access history helps an app owner check who opened an app and when, including requests denied after a person's grant expired.

```sh
tslink access log --app photos --since 24h
tslink access log --who alice@example.com --decision denied --limit 50 --json
tslink access log --since 2030-01-01T00:00:00Z --until 2030-01-02T00:00:00Z --json
```

`--who` matches an account login (ASCII case-insensitive), an exact node name, or a tag. `--since` accepts a positive Go duration such as `24h`, or an RFC3339 timestamp; `--until` accepts RFC3339. Both timestamp bounds are inclusive. Events are returned newest first. The default limit is 100, with a maximum of 10,000. Summaries cover every matching retained event, even if the returned list is truncated. They include counts per person and per app, allowed/denied counts, and each app's last allowed access. A denial does not advance last seen. TCP contributes two events per connection; counts are event counts, not unique people or visits.

The CLI uses the usual versioned result envelope, with `events`, `summary`, and `truncated` under `data`. The read-only MCP tools `access_log` and `access_summary` accept the same filters and return these payloads directly. Both declare `readOnlyHint`; they require no credential, contact no remote service, and create no files or locks. F6 can grant these named tools to `viewer`.

## Recorded metadata and privacy

Each HTTP request that reaches the service handler produces one `kind: http` event. File services use the same chain. TCP emits `tcp_open` and `tcp_close`, with a connection ID, timestamps, identity and byte counts. Events carry schema version 1, UTC time, app/service, method, status, bytes in/out, duration in milliseconds, allowed/denied decision, optional denial reason and optional matched grant (`person` or `legacy_allow`). The shared typed writer also accepts `mcp` and `guest` kinds for future audit producers.

Identity is attested by WhoIs: account login, node name and tags. Tagged nodes have no account login. Public Funnel connections use the `public` login and never retain an IP address. Tailnet connections to the same listener retain their WhoIs identity. Classification uses the trusted `ipn.FunnelConn` transport marker, including TLS/HTTP2 and TSLink connection wrappers; headers cannot select an identity. An unknown private caller has only a coarse IPv4 /24 or IPv6 /48 prefix. Identity enrichment for otherwise unprotected requests occurs in the background with a 250 ms lookup budget; logging does not add an identity lookup to request serving. People/ACL decisions supply their fresh authorization identity to the record.

Path recording defaults to on. Paths contain no query or fragment. Known guest/invite/auth/token/link bearer routes, credential-shaped segments and long segments are redacted. No headers (including User-Agent), cookies, bodies, bearer links, tokens or full URLs are fields of the record. Path names can still contain app-specific sensitive names; turn path recording off for such an app:

```sh
tslink access path photos false
tslink access path photos inherit
tslink config set access-log-path false
```

A global path opt-out takes precedence over per-app settings. Per-app changes reconcile through the registry watcher; global options take effect after restarting `serve`. Disable the entire writer with `tslink config set access-log-enabled false`. Existing retained history remains queryable until it is evicted.

`acl` denotes TSLink's legacy HTTP allow-list; `people` denotes people authorization or an unavailable people registry/identity; `expired` denotes a matched person's expired grant; `limits` denotes HTTP upload/read protections or a TCP connection-cap refusal; `preserve_host_unavailable` denotes the canonical-host refusal. An app's own 403 is still an allowed gateway request. Tailnet-policy packets and incomplete headers rejected before HTTP handler dispatch cannot become HTTP request records. F2 health probes contact the local backend directly and do not enter this chain; a person opening `/health` through the gateway is logged normally.

HTTP byte counts measure body bytes actually read and response bytes written, excluding HTTP headers and framing. They do not infer the unread part of an early-rejected upload. Upgraded HTTP connections count plaintext stream bytes after hijack, including the upgrade response handshake. TCP counts actual stream bytes. Long-lived streams publish their completed HTTP/close event when the handler/connection finishes.

## Bounded local storage and health

History lives under the config directory in `access-log/YYYY-MM-DD-NNNNNN.jsonl`, with POSIX 0700/0600 modes using TSLink's existing helpers. Windows inherits directory ACLs from the owner's config directory; TSLink does not claim to validate a user-only DACL. Only one daemon writer may own that directory; a second writer refuses its lock without waiting. CLI/MCP reads never change permissions or repair files.

Defaults and validated configuration:

| CLI config key | `config.json` key under `access_log` | Default | Valid values |
|---|---|---|---|
| `access-log-enabled` | `enabled` | true | true/false |
| `access-log-path` | `record_path` | true | true/false |
| `access-log-retention-days` | `retention_days` | 30 | 1–3650 |
| `access-log-max-bytes` | `max_bytes` | 67108864 | 65536–1073741824 |
| `access-log-queue-size` | `queue_size` | 1024 | 1–65536 |

An empty CLI value resets the option. Omitted numeric JSON keys, or zero, select the default. Unknown keys, invalid types, negative numbers and values outside the bounds are refused on config load/save. The service key is `access_log_path` (boolean or omitted/null to inherit).

Segments rotate at at most 1 MiB (or one quarter of a smaller size cap), and oldest segments are deleted first to maintain the cap. Retention counts UTC calendar days including today: 30 means today plus the previous 29 dates. Eviction runs on startup, before append and every 250 ms even while idle. The cap covers JSONL data; a small health file and writer lock are additional.

Appends are one newline-delimited JSON write followed by file sync; a newly created segment also syncs its directory on macOS/Linux. Startup removes an incomplete final record before new appends. Complete corrupt records produce an explicit unhealthy state and dropped events; queries return an error rather than silently hiding corruption. Windows flushes file contents using `File.Sync`; directory syncing follows the existing atomicfile Windows boundary. Abrupt process death can lose events still in the memory queue, and hard power loss has the OS/filesystem's durability limits.

Serving only enqueues into a bounded queue. A full queue drops the event immediately. Disk errors also count as drops and preserve request serving. `tslink status` (including `--urls`) and `tslink doctor` expose `access_log` with `enabled`, `last_write`, `drops`, `size_bytes`, `updated_at`, and a stable `error` when unavailable. Doctor warns on known drops or I/O failure. A separate background publisher exposes drops even while append/enrichment is stalled. Health is published every 250 ms and at drain; it is a snapshot, not a proof that a stopped daemon is writing. Drops survive ordinary restarts, but an abrupt crash can lose the counters since the last snapshot. Shutdown gives draining a bounded one-second wait. Logs are kept locally; there is no external shipping or flow-log integration.

## Writer contract for integrations

`internal/accesslog.Writer.Record(Event) bool` is the shared producer seam. It is nonblocking; false means a counted drop. Producers pass typed metadata, a supported `kind`, a decision and stable reason, never a payload or credential. The store assigns a missing UTC event timestamp and applies common sanitization. `Store.RecordResolved` is the daemon's optional asynchronous identity-enrichment seam; its callback must be bounded and capture only identity/address metadata. Integrations obtain the owning daemon writer through `Server.AccessLogWriter`; only the owning daemon should create a store. Read-only tools call `Query`, without creating a writer.
