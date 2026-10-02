# Sharing

For sharing several registered apps with a person for a chosen time, use [people](people.md): `tslink people add alice@example.com --apps photos,finance --for 7d`. Existing tailnet members need no token. Add `--invite --print-links` for a single message containing per-app device invites for an outsider; invitation creation requires a user-owned API token. `people remove` denies that login on subsequent private HTTP/file requests even if accepted network shares remain. TCP and public Funnel cannot be person-scoped. Existing `share`, `add --allow` and `invite` commands remain available.

People removal saves local denial before cleaning up recorded pending invitations; no-token or partial cleanup reports `complete: false` and `cleanup`. Invitation retries reuse completed IDs; unknown POST outcomes require explicit reconciliation, with no exactly-once guarantee. See the people guide for recovery.
## Share in one command

`tslink share` infers whether its argument is a directory, a regular file, a
bare port, or `host:port`. It registers the service without overwriting an
existing name, starts the daemon when needed, waits for an exact runtime URL,
and prints only that URL to stdout. Shares use ephemeral nodes by default.

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink share ./tslink-demo/index.html --name demo-file  # serves only this file
tslink share ./tslink-demo --name demo-persistent --ephemeral=false
```

To share a port instead, start a local app listening on that port first, then run
`tslink share 3000` or `tslink share localhost:8080 --name preview`.

The two path forms differ in reachable surface, and the difference is the
service's own boundary rather than a listing preference. A directory target
serves every file under it and directories without an `index.html` render a
listing. A regular-file target serves that one file: its URL is the file, the
service root redirects to it, and every other path answers 404, including the
file's siblings in the same directory. The registry records the narrowing in
the file service's `file` field; an entry without that field is a directory
share.

Neither form adds an HTTP caller restriction: tailnet policy governs reachability,
and `tslink share` has no `--allow` flag. To limit readers of
a directory, register it with `tslink add <name> --dir <directory> --allow
<principal>` instead, or pass `allow` to the MCP `share` tool.

TSLink refuses to serve its own configuration directory, a directory inside
it, or a directory that contains it (with the default layout that includes your
home directory), with `path_exposes_config_dir`; `add --dir`, `share`, the MCP
tools and the daemon all apply the check. It compares paths after resolving
symbolic links, so a hard link that you place outside the configuration
directory is served like any other file.

On a credential-free first run, the one stdout line is the Tailscale
authorization URL and stderr gives the exact `tslink url <name> --wait`
continuation. With `--json`, this is a successful `status:"needs_login"`
result containing `auth_url`, not an authentication error. Retrying the same
target reuses its existing service instead of creating suffixed orphan nodes.

## Photo and video uploads

Choose a finite limit large enough for a whole upload request, including its
multipart metadata. For a photo app or video library:

```bash
tslink add photos --proxy localhost:2283 --max-request-body 20GiB --request-read-timeout 2m
tslink share 2283 --name photos --max-request-body 20GiB --request-read-timeout 2m
tslink status --urls
tslink list --verbose
```

Services without settings keep a 32 MiB body cap, 10s request header timeout,
30s upload inactivity window and 60s keep-alive idle timeout. Increase the
header window with `--request-header-timeout`, and the keep-alive window with
`--idle-timeout`. To deliberately remove the size cap, use both
`--max-request-body unlimited --ack-unlimited-request-body`.

Uploads stream directly through the reverse proxy; TSLink never collects the
whole upload in memory. Each body read has a fresh inactivity deadline. Time
spent waiting for the backend to accept the preceding bytes is excluded, and
successful reads let an upload continue indefinitely. Response streams and
WebSocket upgrades have no upload deadline. A phone sending data regularly can
therefore take longer than 30 seconds; a stalled proxy upload receives a clear 408.
Oversized bodies receive 413, including unknown-length chunked uploads. An
incomplete HTTP/1 request header receives 408 before the handler starts.
If a handler rejects or ignores a body, its remaining HTTP/1 body is discarded
with an absolute deadline of at most 1s (or the shorter read inactivity window).
An unfinished body then closes the connection; this cleanup does not extend for
progress and does not impose a total timeout on accepted uploads or downloads.
An early backend rejection also interrupts an in-flight body read: HTTP/1 uses
that cleanup bound, and HTTP/2 closes the request-body stream immediately. Reads
finishing during cleanup cannot renew its deadline or generate an upload-timeout
warning. Completed bodies keep the usual connection reuse behavior.

The owner gets a structured service/limit log and a runtime warning in
`status --urls` and `list --verbose`; `doctor` points to the flag to adjust.
Warnings last until the service node restarts. Re-run add with the complete
service configuration when changing limits, because omitted add flags reset.
A share will not reuse an existing service with different effective limits.
The conflict lists each differing limit, its existing/requested value and the
flag to use when reconfiguring with add.
Backends must handle partial uploads on failure; their own limits still apply.

For agents, the CLI JSON results expose effective `request_limits` in bytes
and duration strings. MCP add/share accept `request_limits` with `max_body`,
`unlimited_ack`, `header_timeout`, `read_timeout` and `idle_timeout`.
Application recipes can call `registry.RecommendedUploadLimits()` to recommend
20 GiB and a 2m inactivity window for Immich, Nextcloud or Jellyfin; recipe
wiring is separate from these limits and must retain the app's own requirements.

## Share for a chosen time

People `--for`/`--until`, Funnel `--funnel-ttl` and `extend` use the [unified durations](durations.md). Minimum 1h; guest/public maximum defaults to 7d. New grants default to 24h. `never` needs `--ack-never` and a tailnet-member login. Use `tslink extend photos --person alice@example.com --for 36h` for one app, or omit `--person` for a [Funnel TTL](funnel.md). Expired grants require explicit `--regrant`.
