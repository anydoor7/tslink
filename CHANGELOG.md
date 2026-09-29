# Changelog

All notable changes to TSLink are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - unreleased

Initial public release.

### Security

- `tslink share <file>` now serves only that one file. The registry records
  the served file in a new `file` field; the daemon answers `GET /<file>`,
  redirects `GET /` to it, and returns 404 for every other path, so sibling
  files and directory listings are no longer reachable through a single-file
  share. Shares created by earlier releases have no `file` field and keep
  serving their parent directory until they are shared again; re-run
  `tslink share <file>` for any existing single-file share you rely on.
- The daemon's own log files are created 0600, an existing wider log is
  narrowed on the next start, and rotated archives never carry more than
  owner-only permissions. Logs contain access records, invite recipients and
  authorization URLs.
- `tslink remove` deletes the service's local node identity directory once the
  remote device deletion is confirmed and no daemon holds that node; the
  daemon's reconcile pass does the same for orphaned identities it can prove
  stale. Directories left behind by earlier releases are not swept.
- Proxy and TCP targets that point at a link-local address (`169.254.0.0/16`,
  `fe80::/10`), an unspecified address (`0.0.0.0`, `::`), the
  `metadata.google.internal` hostname, or a non-canonical numeric spelling of
  such an address (`0xA9FEA9FE`, `169.254.43518`, ...) are now refused at
  registration with the stable error code `link_local_target_refused`.
  Registered services that already target such an address are excluded when
  the daemon loads the registry and reported as a service issue with the same
  code by `tslink doctor` and `tslink status`; if an entry such as
  `http://169.254.10.10:80` was a deliberate directly-attached device, change
  its target in `registry.json` to an address the daemon should reach instead.
  Validation never resolves DNS: a hostname that resolves to such an address
  (for example via a public wildcard DNS service) is still accepted, and this
  boundary is documented in SECURITY.md.
- When the daemon cannot finish applying a registry change to a public
  service, it now closes that service's old Funnel listener instead of
  leaving the previous target reachable from the internet. This covers a
  failed Funnel policy preflight or tag-policy update, a failed
  credential-mode change and an unreadable node identity record; public
  services that did not change keep serving. The daemon retries a blocked
  service by itself, so a service withdrawn during a transient Tailscale API
  outage comes back without a registry change or a restart. A service
  blocked on a failed preflight is retried on the next 30-second lifecycle
  tick, then after 1, 2, 4 and 8 minutes, then every 15 minutes; each retry
  makes one Funnel policy request, plus the tag ensure when
  `serve --manage-acl` is set, and a registry change or a sync without a
  policy failure restarts the schedule. A service blocked on an unreadable
  identity record is retried on every tick.
- `tslink tags delete-remote tag:tslink-funnel` now counts every local
  service with an effective Funnel as a user of the tag and refuses with the
  in-use error (`conflict`, exit 4 with `--json`), even with `--force
  --manage-acl`. The Funnel tag is derived and never stored in a service's
  `tags`, so the command used to delete the tailnet-wide Funnel tag owner and
  grant while this machine's own public services still relied on them. A
  Funnel whose deadline has passed does not block the command.
- `golang.org/x/crypto` moved to v0.57.0, past the `x/crypto/ssh` advisories
  GO-2026-6355, GO-2026-6354 and GO-2026-6303 (none were reachable from TSLink
  code).

### Added

- `TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1` makes `tslink doctor` skip its read of
  the local `tailscaled` for the Tailscale SSH check. The state is then
  `unknown` and the `tailscale_ssh_unknown` finding says the check was
  skipped; doctor's status and exit code are unchanged.
- `.github/workflows/ci.yml` runs the Release Candidate gate on every pull
  request and on every push to `main`.
- The `share` result (`tslink share --json` data and the MCP `share` tool)
  has two new fields. `funnel_expires_at` is the Funnel deadline of the share
  actually returned, which for a reused share can be sooner than the one
  requested. `funnel_rearmed` is `true` when the call re-armed an expired
  Funnel share. Both are omitted for a tailnet-only share, and
  `funnel_expires_at` is also omitted for a Funnel that never expires. The
  CLI `share` command cannot create or reuse a Funnel share, so its output is
  unchanged in practice.
- Credential changes are now serialized across TSLink processes by lock
  files. With the system keychain enabled, which is the default, a writer
  takes `.tslink/credentials.lock` in the OS account's home directory (it
  ignores `$HOME`; `%USERPROFILE%\.tslink\` on Windows), then
  `credentials.lock` in the config directory. With
  `TSLINK_DISABLE_KEYRING=1` it takes only the config-directory file.
  `login`, `logout` and `doctor --probe-remote` take the lock. So do `serve`
  and the commands that report credential status, such as `status`, `list`,
  `url`, `share` and `doctor` and the matching MCP tools, but only when they
  must record metadata for a stored credential that has no matching record
  yet, or when `serve` finds a legacy `~/.config/tslink/apikey` file.
  With no stored credential they create no lock file. The files are empty,
  mode 0600, and are not removed by `logout` or `uninstall`. When another
  TSLink process holds the lock for the whole 5-second wait, `login` and
  `logout` exit 4 with `error.code` `conflict` and a message that names the
  lock path. On a filesystem that cannot `flock` (for example NFS without
  lockd), TSLink logs one warning and serializes credential changes only
  within the process.
- The daemon keeps one identity record per service in
  `node-identities/<name>.json` in the config directory: the tags (including
  the derived `tag:tslink-funnel`), ephemeral setting and control URL the
  service's node was prepared with. When they no longer match the service,
  the daemon removes the node's local state before it starts the node again,
  so the node enrolls with its new identity. On Tier 1, where a node enrolls
  interactively and advertises no tags, only a change of the ephemeral
  setting or the control URL counts. A record that cannot be read
  safely fails only its own service: `status` reports `internal_error` with
  the record's path and recovery steps, the node state is kept, and the
  service recovers on the next lifecycle tick once the file is moved aside.
  A record written by a newer TSLink is left untouched and its node starts
  over its existing state. A removed service's record is deleted once
  `registry.json` is present and valid and the service's `nodes/<name>/`
  state is gone. If `config.json` cannot be loaded, `serve` logs a warning
  and uses the default control URL, and that fallback never resets a node on
  its own.

### Changed

- `add` now waits up to 30 seconds by default. Scripts that only need to
  register configuration should pass `--wait=0`.
- Windows reports `windows-startup` when its Startup registration matches the
  current config. `doctor` warns that crash restart is unavailable on that
  platform instead of asking for a reinstall that cannot fix it.
- `tslink cleanup --manage-acl` and `tslink serve --manage-acl` no longer
  delete the shared Funnel ACL grant (the `tag:tslink-funnel` tag owner and
  its `nodeAttrs` grant). Every TSLink installation on the tailnet shares
  that grant, and one machine's registry cannot prove that no other host
  uses it. `acl_action` in `cleanup --json` and in the daemon's lifecycle
  log now takes only the values `not_requested`, `still_in_use` and
  `skipped`; `would_delete_if_canonical` and `deleted` are no longer
  produced. With no active local Funnel service, `cleanup --manage-acl`
  reports `skipped` with a warning whether or not `--dry-run=false` is
  given. `serve --manage-acl` does the same on its first lifecycle tick,
  when the last local Funnel service goes away and when a Funnel expires,
  and reports `not_requested` on the ticks in between. When the check runs
  while `registry.json` is missing or empty, the result is also `skipped`
  with a warning. To revoke the grant, verify every host and device yourself
  and run `tslink tags delete-remote tag:tslink-funnel --force
  --manage-acl`. The `cleanup` help for `--manage-acl` and `--dry-run`
  describes the new behavior.
- The MCP `share` tool now takes `tags` and `funnel_ttl` into account when a
  call matches a target that is already shared. It used to reuse the
  existing service whatever tags or Funnel lifetime the call asked for, so a
  caller could get back a node with other tags or a Funnel that outlived the
  requested `funnel_ttl`. Explicit `tags` must now equal the existing
  service's tags as a set. An existing Funnel share is reused only while its
  deadline has not passed and is no later than the requested one, and one
  that never expires only for `funnel_ttl` `never`. Anything else fails with
  a `conflict` error. A call without `tags`, and every CLI `tslink share`
  retry, still reuses the service after its tags or the default tag
  changed. Re-running an identical Funnel share whose deadline has passed
  re-arms it with the requested `funnel_ttl` and turns Funnel back on,
  including after the daemon turned it off, and the result carries
  `funnel_rearmed: true`. For an expired share, a `never` request or any
  other posture difference still conflicts.
- The MCP `url` tool's `wait` is capped at 5m; a larger value is refused as
  a usage error (`wait "10m" exceeds the maximum of 5m0s`). After the client
  closes stdin, `tslink mcp` still answers the calls it has already read,
  but a call still running 6m after stdin closed is cancelled, and after a
  5s grace the command exits 1 with a single `Error: mcp stdio: in-flight
  MCP calls did not finish after end of input: ...` line. A response still
  blocked on a stdout the client no longer reads delays that exit by at most
  another 5s. It used to wait for such a call indefinitely.
- SIGINT and SIGTERM now cancel a `tslink mcp` session instead of killing
  the process. In-flight calls see the cancellation, so a share still
  waiting for its URL is rolled back instead of staying registered, and the
  command exits 1 with an error line that names the signal. A handler that
  ignores cancellation is abandoned after 5s, and a second signal terminates
  the process at once. `tslink mcp --help` now describes these limits and
  the inputs that end a session.
- On Linux, `tslink uninstall` now fails and keeps the unit file when
  `systemctl --user stop` fails and TSLink cannot confirm that the service
  has shut down (an `inactive` or `failed` state with no main PID). It used
  to delete the unit and exit 0 with a warning while the daemon could still
  be running. When `systemctl --user` cannot reach the user manager (for
  example under `su`, `sudo -u`, or SSH without a login session), the error
  says to rerun from a login session of this user that has the user bus
  (`XDG_RUNTIME_DIR=/run/user/$UID`), or to remove the unit file by hand if
  `systemctl --user` is permanently unavailable. If the unit file is already
  gone but systemd still runs `tslink.service`, or the
  `default.target.wants/tslink.service` link remains, `uninstall` now fails
  and names the commands that finish the removal instead of printing
  `systemd user service not installed`. `uninstall --help` describes both
  cases.
- On Tier 2, turning Funnel on or off for a service now re-enrolls its node
  as a new device, because a public service's node carries
  `tag:tslink-funnel`. This includes the daemon turning an expired Funnel
  off. Before, the old enrollment was reused with its old tags. A change of
  tags, ephemeral setting or control URL made while the daemon was not
  running now takes effect the same way at the next start. On Tier 1 a node
  advertises no tags, so Funnel and tag changes keep its enrollment. Nodes
  enrolled before this release are adopted as they are, without a reset.
- `tslink doctor` on a fresh default-tier install (no stored credential, no
  services) now exits 0, and `doctor --json` reports `health_exit_code: 0`;
  it used to exit 64. The `credential_none` finding now has severity `info`
  in every state.
- A single-file share now reports `backend.kind: "file"`, with
  `backend.display` set to the file's full path, in `list --verbose`,
  `status --urls`, `access explain` and the matching MCP tools. It used to
  report `"directory"` with the parent directory.
- `tslink add <name> --proxy 8080` and `--tcp 8080`, and the MCP `add` tool,
  now read a bare port as `localhost:8080`, as `tslink share 8080` does. A
  bare `--proxy` port used to be refused as a link-local or cloud-metadata
  target (`link_local_target_refused`, exit 1), and a bare `--tcp` port as a
  missing port. A digits-only value outside 1 to 65535 is now a usage error
  (exit 2).
- On macOS and Linux, `install` and `uninstall` now bound every `launchctl`
  and `systemctl` call: a read-only query (`launchctl print`,
  `systemctl --user show`) gets 2 seconds and is retried once after a
  timeout, and a state change gets 120 seconds, so a hung service manager no
  longer hangs the command. A query that times out twice is treated as
  unknown, never as a stopped or unowned daemon. If that happens while
  `install` checks who owns a running daemon, it stops with `could not tell
  whether launchd owns the running TSLink daemon: ...; nothing was changed,
  retry 'tslink install'` (`systemd` on Linux).

### Fixed

- A service node or the `--mcp` control-plane node whose tsnet start fails
  very early (for example on Linux when `/proc/self/exe` cannot be read) no
  longer crashes the daemon with a nil-pointer panic in tsnet's `Close`. The
  failure is reported as that node's start error; a control-plane failure
  still stops the daemon, as before. `tslink login` client-secret validation
  reports the same failure as a validation error instead of a panic trace.
- `go test ./...` no longer reaches anything real on the machine that runs
  it: not the contributor's TSLink config or credentials, their Tailscale
  daemon or its LocalAPI token, their LaunchAgent, systemd unit or Startup
  script, or Tailscale's servers. Exported `TSLINK_*` variables no longer
  change test results.
- On Tier 1, `tslink tags set` and `tags add` no longer clear the service's
  node state. A Tier 1 node advertises no tags, so the reset changed nothing
  on the tailnet and only forced a new browser authorization. The new tags
  take effect once the daemon runs on Tier 2 after `tslink login`.
- On Windows, `tslink stop` now removes the PID file and identity artifacts
  of a daemon that has already exited, even while another process still
  holds a handle to it. It used to read such a daemon as running and keep
  them.
- When a backend sends an informational response such as `103 Early Hints`
  or `100 Continue` before an error, the client now receives that error (for
  example `503`) instead of an implicit `200`, and the access log and
  `tslink_requests_total` record the final status. WebSocket and other
  upgrades proxied through a service are logged and counted as `101` instead
  of `200`.
- Credential metadata in `credential-meta.json` is now written under the
  credential lock. A metadata backfill by `status`, `doctor` or `serve` no
  longer replaces the record a concurrent `login` has just written, so a
  stated `--expires-in` and `last_verified_result` survive. `logout` removes
  values and metadata in one transaction and no longer deletes the metadata
  of a credential stored while it ran. `doctor --probe-remote` no longer
  records its verdict on a credential that was rotated during the probe.
- `serve` no longer overwrites an API key in the system keychain with a
  different value from a legacy `~/.config/tslink/apikey` file, which after
  a rotation is usually the stale one. It keeps the keychain value, leaves
  the file in place and logs `legacy API key file differs from the keyring
  credential; kept the keyring value and left the file in place`. It also
  leaves the file alone when the keychain cannot be read. A `login` that
  rolls back now restores an `apikey` or `clientsecret` file that its
  keychain write removed.
- On Windows, the daemon's stderr log `tslink.err.log` is now rotated at the
  same 8 MiB cap as on macOS and Linux, keeping one `.1` archive; it used to
  grow without bound. Rotation needs the append-only handle that
  `serve --daemon` opens. A log opened for writing at its own offset is
  refused as not opened with `O_APPEND`, as on Unix.
- On Windows, `tslink install` writes the Startup script `tslink.vbs`
  atomically, including into a Startup folder that is a directory junction.
- The error for an unreadable node ownership ledger (`node-ownership.json`)
  and its two recovery steps now quote the path verbatim. On Windows they
  used to show every backslash doubled.
- `tslink mcp` refuses a JSON-RPC batch wherever a top-level JSON value
  begins, not only at the start of a line, at every protocol revision. A
  refused batch or an oversize record ends the session before any of it is
  dispatched, and a `[` that begins a line inside a multi-line JSON object
  is no longer mistaken for a batch.
- The MCP `unshare` tool and `tslink remove` pass their request context to
  remote device cleanup, so a cancelled call or an ended `tslink mcp`
  session interrupts a Tailscale API request that never answers. The service
  is still removed, and `device_warning` reports the cancellation.
- The MCP `share` and `add` tool descriptions no longer imply that every
  link-local or cloud-metadata target is refused. They now say that the
  check covers literal IP addresses and the `metadata.google.internal`
  hostname only, that hostnames are not resolved, and that nothing is
  checked at connect time.
- `scripts/release-verify.sh` reports a `BLOCKED` gate when there are no
  archives to check. It used to report a vacuous pass on bash 4.4 and later
  and abort with `archives[@]: unbound variable` on macOS bash 3.2.
