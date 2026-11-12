# Sharing

For sharing several registered apps with a person for a chosen time, use [people](people.md): `tslink people add alice@example.com --apps photos,finance --for 7d`. Membership is verified against the network directory. Use `--print-links` to disclose prepared invitations: outside recipients accept one gateway invitation when the gateway is enabled, or one invitation per app when disabled; invitation creation requires a user-owned API token. `people remove` denies that login on subsequent private HTTP/file requests even if accepted network shares remain. TCP and public Funnel cannot be person-scoped. Existing `share`, `add --allow` and `invite` commands remain available. `add --allow` (and the MCP `add`/`share` `allow` field) keeps its permanent meaning: a person named there keeps access until removed, with no `--ack-never`, unless `--for`/`--until` limits it. The 24h default belongs to `people add`.

People removal saves local denial before cleaning up recorded pending invitations; no-token or partial cleanup reports `complete: false` and `cleanup`. Invitation retries reuse completed IDs; unknown POST outcomes require explicit reconciliation, with no exactly-once guarantee. See the people guide for recovery.
## Share once through the gateway

Use [gateway mode](portal.md#gateway-mode-one-device-share-for-routed-apps) when
an outside-tailnet person needs several HTTP/file apps. Each app has a stable
HTTPS port on the gateway node. `add --portal-only` avoids a separate app node;
`add --remote https://node.example.ts.net[:port]` uses a tagged gateway's tsnet
connection to an app in its own tailnet. The person accepts one gateway share,
then visits the dashboard. Later people-grant changes need no further acceptance.
Gateway mode is optional; ordinary `share` retains its per-app node behavior.

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
service root redirects to it with HTTP 302, and every other path answers 404, including the
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
multipart metadata. For a photo app or video library on the tailnet:

```bash
tslink add photos --proxy localhost:2283 --max-request-body 20GiB --request-read-timeout 2m
tslink share 2283 --name photos --max-request-body 20GiB --request-read-timeout 2m
tslink status --urls
tslink list --verbose
```

These limits size one request; they do not make every path fast. Through
Funnel (guest links and public URLs), one upload stream measured about 0.65 to
1.5 MB/s on 2026-11-11 on one host, against about 35 MB/s over the tailnet on
the same path: 40 GB would take 11 hours or more. One host on one day, not a
guarantee; Tailscale documents non-configurable Funnel bandwidth limits. Send
very large files over the private tailnet path ([people grants](people.md),
with a device invitation for an outsider) on an app without guest links.

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
Oversized bodies receive 413, including unknown-length chunked uploads. A
refused upload gets TSLink's JSON error envelope with code `request_body_limit`
(or `request_read_timeout` for a stalled one) and a message that states the
limit, so an upload client can tell what happened; it names neither the app's
service name nor an owner flag. An
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
`status --urls` and `list --verbose`, repeated as a `doctor` finding. The
warning says the app is rejecting requests, counts the rejections since the
app's node last started (and when that was), gives the most recent time and the
`tslink limits set ...` command that raises the limit. Changing limits restarts
the node, so the count resets when the limit is changed; every refused upload
stays in `tslink access log --app <app>` with its 413 or 408 status. A
connection that times out before sending any request byte (Funnel and edge
relays open idle ones) still gets the 408 and an Info-level log line, but is
not counted as a rejected request. The first rejection is published at once;
later ones within 30 seconds are published together at the end of that window.
Warnings last until the service node restarts. Change limits with
`tslink limits set <app> --max-request-body 4GiB` (MCP `limits_set`): it changes
only the named limits, keeps every other setting and works on apps with guest
links. It restarts the app's node: open connections and uploads in flight end and
guests must reopen their link, so set limits before sending a link. Re-running add instead replaces the whole service, so omitted add flags
reset. A share will not reuse an existing service with different effective
limits. The conflict lists each differing limit, its existing/requested value
and the flag to pass to `tslink limits set`.
Backends must handle partial uploads on failure; their own limits still apply.

For agents, the CLI JSON results expose effective `request_limits` in bytes
and duration strings. MCP add/share accept `request_limits` with `max_body`,
`unlimited_ack`, `header_timeout`, `read_timeout` and `idle_timeout`.
A recipe in the catalog can carry finite `request_limits` (the same fields as
MCP `request_limits`); a new service from that recipe uses them unless the
request-limit flags (MCP `request_limits`) are given. Recipes never select an
unlimited body. `registry.RecommendedUploadLimits()` returns 20 GiB and a 2m
inactivity window as a starting point for such a recipe.

## Share for a chosen time

People `--for`/`--until`, Funnel `--funnel-ttl` and `extend` use the [unified durations](durations.md). Minimum 1h; guest/public maximum defaults to 7d. New `people add` grants default to 24h; `add --allow` recipients have no expiry unless `--for`/`--until` limits them, and `invite device` (no lifetime flag) gives a new grant no expiry and leaves an existing one alone. `never` needs `--ack-never` for a member or device-invited guest; browser links and Funnel remain finite. Use `tslink extend photos --person alice@example.com --for 36h` for one app, or omit `--person` for a [Funnel TTL](funnel.md). Expired grants require explicit `--regrant`.
