# App health, expiry and owner alerts

TSLink checks the app behind each registered service while the daemon runs. A
listening port alone does not prove that a web app works. Configure a small,
read-only endpoint that checks the dependencies your app needs:

```sh
tslink add photos --proxy localhost:3000 --health-path /ready \
  --health-status-min 200 --health-status-max 299 --health-body ready \
  --health-timeout 5s --health-interval 1m
tslink status --json
tslink list --verbose
tslink doctor --json
```

HTTP defaults are GET `/`, status 200 through 299, no body assertion, 5-second
timeout and a 1-minute interval. The path is joined to the backend base path
with the same rules as the reverse proxy; any backend query is preserved. The
probe uses that backend directly, without Tailscale caller identity. HTTP and
TCP probes enforce the registry's target-safety refusals before doing any I/O;
refused targets are excluded from scheduling. Enrollment and policy failures
still permit backend checks. Redirects
are not followed: configure the expected range or a suitable endpoint if the
app redirects. An endpoint requiring caller authentication may need a separate
local readiness path. This observation does not prove remote access, ACLs or
TLS on the Tailscale frontend.

For `preserve_host` services, probes send the same canonical Host and
X-Forwarded-Host as the proxy. Missing or invalid trusted runtime names produce
`health_canonical_host_unavailable` before backend I/O. Doctor obtains names
only from a verified, current snapshot matching the registered endpoint.
Probes validate request-limit configuration and send bodyless GETs, which fit
every valid request-body bound. Client upload/header/idle limits do not limit
backend response reads; health timeout and the 64 KiB response cap still apply.
New recipe registrations default to their catalog health path; explicit health
options override it, and recipe reuse preserves the existing settings.

`--health-body` matches a substring in the first 64 KiB. Timeout must be
100ms through 30s; interval must be 10s through 1d and at least the timeout.
HTTP path/status/body options are valid only for proxy services. TCP services
keep TCP connection checks, and file services check that the registered
directory or single file can be opened with the expected type. File checks
inspect the type before opening, then verify the opened object's type and
identity. Unix opens use nonblocking mode to reject FIFO replacements; Windows
opens follow supported directory junctions and symlinks as registry admission
does, including junctions and mount points reported as irregular by the OS
when they resolve to readable directories. They reject other irregular objects/device handles and compare identities
captured from handles before and after opening to reject replacements. Timeout and interval
options also apply to those services. Uninterruptible filesystem calls retain a
worker slot, but cannot hold up other results or monitor shutdown; at most four
backend calls can remain in progress across cycles. Paths may not contain a
query, fragment or another host. Use a non-sensitive substring; secrets do not
belong in CLI arguments. Registry `health` and the MCP `add` tool accept the
same settings (`path`, `status_min`, `status_max`, `body_contains`, `timeout`,
`interval`). Re-adding an existing name replaces its health settings too.

A successful check is `healthy`. One or two consecutive failures are
`degraded`; three failures are `down`. A success clears the failure count.
`health` includes `last_checked`, `last_error` (a stable code), and
`consecutive_failures` in CLI JSON, MCP `status`/`list`, and `/events` state.
An absent check is `unknown`; an observation older than twice its configured
interval plus its timeout is shown as unknown. The monitor wakes connected
`/events` clients when an observation crosses that boundary, even if no new
check can be attempted. Node authorization and endpoint
readiness remain separate from app health. `doctor` performs a fresh HTTP
business check in addition to its TCP connection check, with its existing
external-target opt-in. Background checks use up to four concurrent workers
and start after the initial registry synchronization. A 10-second scheduler
selects due checks, so the effective interval can be up to 10 seconds longer,
and longer while a large batch of slow backends is being checked. Completed
checks are published in batches of ready results, with up to 50 ms to collect
a batch, while slow checks continue. Each batch commits the journal and
publishes the snapshot at most once. Failed writes remain pending and retry on
a later scheduler cycle, including when no new check is due. Once persistence
succeeds, unchanged state causes no writes.

Each service has at most one actual backend read in flight. The I/O timeout
starts after acquiring a worker slot. Queue admission has a separate five-second
limit. A check that cannot acquire capacity, times out in the queue, or still
has a previous read in flight is **not attempted**: it preserves the failure
streak and `last_checked`. An admitted read that exceeds its I/O budget is a
checked timeout; an uninterruptible call retains its slot until it actually
exits and cannot publish a late result.

If all four slots in either pool are retained by timed-out calls,
`alerts.monitor_error` reports `health_monitor_saturated`. The journal records
one monitor-level `monitor_saturated` event per episode and `monitor_recovered`
when capacity returns. `doctor` reports a warning (exit 64 unless another
finding is critical). A full pool prevents more reads in that pool; it never
turns an unattempted app check into a down alert.

## Expiry early warning

Each service reports `node_key`: expiry time, completed 24-hour days left,
source, warning and next steps. The embedded LocalClient reports the actual
deadline; TSLink never guesses it from the service creation date or a default
lifetime. Warnings use the remaining duration: `warning_14d` at 14 days,
`critical_3d` at 3 days, and `expired` at the deadline. `doctor` returns warning
exit 64 for the 14-day node warning and critical exit 65 for the 3-day or
expired node warning. `status` remains an informational command.

Node deadlines are refreshed once per minute, independently of the backend
health interval, with a separate pool of up to four LocalAPI calls. The same
per-service guard and separate queue-admission limit apply; admitted LocalAPI
reads have a full five-second I/O budget. Unattempted reads preserve the cached
deadline and polling timestamp; an attempted failed read reports unknown. Replacing
a service node immediately invalidates its cached deadline; it remains unknown
until the new node reports its own deadline. Sharing edits retain the backend's
failure streak.

The pinned `tailscale.com v1.102.4` exposes
[`PeerStatus.KeyExpiry` in ipn/ipnstate/ipnstate.go:336-338](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnstate/ipnstate.go#L336-L338).
The self status calls `peerStatusFromNode` at
[`ipn/ipnlocal/local.go:1517-1530`](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnlocal/local.go#L1517-L1530),
which copies nonzero `Node.KeyExpiry` at
[`local.go:1654-1656`](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnlocal/local.go#L1654-L1656).
[`LocalClient.StatusWithoutPeers`, client/local/local.go:779-788](https://github.com/tailscale/tailscale/blob/v1.102.4/client/local/local.go#L779-L788)
reads `/localapi/v0/status?peers=false`. A missing self/deadline or failed local
read is `unknown`, including when expiry might be disabled. Do not treat it as
proof of a permanent key.

Stored credentials retain their existing metadata semantics. API tokens with
operator-supplied expiry keep source `user`; a deadline estimated from the
documented maximum keeps `assumed_max`. `credentials.*.early_warning` adds the
same 14-day/3-day thresholds without relabeling an estimate as a reported
deadline. The 3-day credential warning makes `doctor` critical. OAuth client
secret metadata identifies no scheduled expiry; revocation can still occur.
A legacy auth key or unreadable/unmatched metadata has no trustworthy deadline.
Generated OAuth access tokens are renewed by the SDK; their temporary expiry is
not the stored client secret's expiry. Run `tslink doctor --json`, review the
service node in the admin console, or rotate a stored token using
`printf %s "$TOKEN" | tslink login --api-key-stdin --expires-in <actual-duration>`.
TSLink does not silently disable expiry or reauthenticate nodes.

[Tailscale key expiry](https://tailscale.com/kb/1028/key-expiry) documents the
180-day default for new domains and the admin-console controls. An enrollment
auth key's expiry is distinct from the enrolled node's expiry
([auth keys](https://tailscale.com/kb/1085/auth-keys)). Sources checked 2026-10-01.

## Optional notifications

There is no external notifier by default. Down, recovery and expiry-threshold
events are still recorded in `health-alert-state.json` and exposed in
`status` JSON, MCP and `/events` snapshots/updates. The file retains the latest
100 events plus dedup and monitor state. Status and doctor read the durable
journal, including when
the daemon stops or its runtime snapshot is older than the journal. A live,
usable snapshot can supplement a journal write error that could not be saved;
it cannot replace durable events, monitor state or current journal errors. The event
stream requires the existing opt-in MCP control plane and its
authorization rules; notifications do not create a new network listener.

To opt in, create an owner-controlled `alerts.json` in TSLink's config directory
(0600 on POSIX), then restart the daemon. Use exactly one channel:

```json
{"command":["/absolute/path/to/notify","owner-channel"]}
```

or

```json
{"webhook":"https://your-notifier.example/owner-hook"}
```

Commands run directly as argv, without a shell. They receive the event JSON
on stdin and in `TSLINK_ALERT_JSON`, plus `TSLINK_ALERT_KIND` and
`TSLINK_ALERT_SERVICE`; they run with the daemon's environment and permissions.
Webhooks receive a JSON POST with `Content-Type: application/json`. Redirects
are refused. Delivery runs on one worker with a queue of up to 16 events, so
monitoring does not wait for a notifier. Each invocation has a 10-second I/O
deadline; commands allow up to another 250 ms to finish pipe cleanup. Unix
commands run in a separate process group, which is killed on cancellation and
after the direct command exits. On Windows, cancellation terminates the direct
command; descendants may continue, but cannot hold an output pipe open because
output goes directly to the null device. A child that deliberately leaves its
Unix process group can also outlive the command. Cancellation is a failed
delivery, including during daemon shutdown. Command output and webhook response
bodies are discarded. Destinations are `[redacted]` in
diagnostics; notification errors contain stable codes only. Config and command
arguments remain private input on disk and in the launched process's argv.

Shutdown allows one second for the canceled delivery worker to finish. If an
OS call cannot be interrupted, monitoring can still stop; a late worker return
cannot change the journal or mark the canceled event sent.

All transitions enter the local journal. External delivery is limited to one
per minute globally and one per service/event-kind/subject per five minutes.
Suppressed deliveries are marked `rate_limited`; they are not queued or retried.
Eligible deliveries remain `pending` until completed, then become `sent` or
`failed`. Queue saturation and shutdown cancellation are recorded as `failed`;
these deliveries are not retried.
Repeated down checks do not create another down event, and each expiry
threshold emits once per recorded deadline/source. A renewed deadline starts a
new expiry cycle. Events and delivery reservations are saved before invocation,
so restart does not resend committed alerts. Delivery is best effort and at
most once: a crash between save and send may lose an external notification.
An unwritable state file prevents external delivery and reports
`alert_state_write_failed`. Malformed notifier configuration disables the
notifier and exposes its error code. Monitoring does not supervise or restart
the backend app; configure app supervision separately.
