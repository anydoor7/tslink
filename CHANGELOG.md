# Changelog

All notable changes to TSLink are documented here. The public compatibility
surface for the 0.x series is defined below; TSLink is a CLI, not a Go library.

## [0.1.0] - unreleased

First public release. TSLink registers local HTTP, file, and raw TCP services as
separate Tailscale `tsnet` nodes. Services are private to a tailnet unless a
proxy service is explicitly published through Funnel.

### Security

- `tslink share <file>` serves that file alone. The registry stores its name in
  `file`; `GET /` redirects to `GET /<file>`, and other paths return 404.
  Directory shares remain explicit.
- The daemon creates logs with mode 0600, narrows an existing wider log on
  startup, and keeps rotated archives owner-only. Logs may contain access
  records, invitation recipients, and authorization URLs.
- `add --dir`, `share`, templates, and the daemon refuse a file root that is,
  contains, or lies inside TSLink's config directory with
  `path_exposes_config_dir` (exit 2 for CLI registration). With the default
  config location, sharing the home directory is refused. A home share with a
  separate `TSLINK_CONFIG_DIR` receives `file_root_home_directory`.
- Proxy and TCP registration refuses literal link-local addresses
  (`169.254.0.0/16`, `fe80::/10`), unspecified addresses (`0.0.0.0`, `::`),
  non-canonical numeric spellings of them, and `metadata.google.internal`
  with `link_local_target_refused` (exit 2). A registered service with that
  target is isolated and reported by `status` and `doctor`. Validation does
  not resolve hostnames or check resolved addresses at connection time.
- A credentialed daemon refuses a non-Tailscale control URL before sending a
  Tailscale-derived auth key. This covers a service `control_url`, global
  `control-url` or `--control-url`, `TS_CONTROL_URL`, and the MCP node. The
  affected service reports `credential_control_url_mismatch` (exit 4). A
  legacy `authkey` file is used as supplied.
- If a changed public service fails Funnel policy preflight, tag update,
  credential-mode transition, or identity-record loading, its old Funnel
  listener closes. Other services continue. Policy failures retry on the next
  30-second tick, then after 1, 2, 4, and 8 minutes and every 15 minutes;
  identity-record failures retry every tick. Each policy retry makes one
  Funnel policy request and, with `serve --manage-acl`, a tag ensure.
- Acknowledged Funnel services auto-provision the shared
  `tag:tslink-funnel` owner and `nodeAttrs` grant in the tailnet policy by
  default. `--no-auto-provision` on `add --funnel`, `serve`, or `install`
  disables the corresponding service or daemon setup path. Ordinary tag ACL
  writes require `--manage-acl`.
- `tags delete-remote tag:tslink-funnel` treats each active local Funnel as
  using the shared tag and refuses deletion even with `--force --manage-acl`.
  The JSON path reports `conflict` (exit 4); the human path reports an error
  (exit 1). An expired Funnel does not
  block this command. `cleanup --manage-acl` and `serve --manage-acl` retain
  the shared grant because this host cannot prove other hosts have stopped
  using it; `cleanup --json` reports `acl_action` as `not_requested`,
  `still_in_use`, or `skipped`. To revoke the grant, inspect every host and
  device, then use `tags delete-remote ... --force --manage-acl`.
- Remote device deletion requires recorded NodeID ownership. `remove`
  records retirement before unregistering a service and refuses an unreadable
  ownership ledger with `internal_error`. While a daemon runs, it removes
  retired node state after confirming remote deletion and that no node holds
  the state; without a daemon, `remove` performs that work. A service merely
  absent from `registry.json` keeps its node state. A missing service with an
  unretired ownership row blocks only that service's device deletion, which
  `doctor` reports as `device_cleanup_blocked`.
- MCP user invitations with a role other than `member` and device invitations
  with `allow_exit_node: true` require `mcp.allow_elevated_invites: true` in
  `config.json`. Without that opt-in they return `mcp_elevated_invite_refused`
  (mapped to exit 3). The setting is off by default and CLI invitations do not use it.
- `tags delete-remote` refuses with `config_load_failed` if `config.json`
  cannot be read strictly. A single-file share inside the config directory is
  refused even before the file exists. `login --client-secret` validates with
  Tailscale's control server regardless of `TS_CONTROL_URL`.

### Services and sharing

- `add` registers one proxy, directory, or TCP service. Bare ports such as
  `--proxy 8080` and `--tcp 8080` mean `localhost:8080`; a digits-only value
  outside 1-65535 is a usage error (exit 2). It waits up to 30 seconds for a
  URL or enrollment URL by default; `--wait=0` saves configuration without
  waiting. `--no-daemon-install` saves configuration without setting up a
  background service.
- Repeating `add` with an existing name replaces the service. Its JSON result
  includes sorted `replaced_fields` (an empty array for a new service), and
  `access_changed_on_replace` or `identity_reset_on_replace` warns when
  applicable. The default tag is `tag:tsmain`; Tier 1 nodes advertise no
  tags, while stored-credential Tier 2 nodes use configured tags.
- Public Funnel is proxy-only, requires `--funnel --public`, and defaults to a
  24-hour lifetime. `--funnel-ttl` accepts `1h`, `8h`, `24h`, `72h`, `7d`, or
  `never`. `registry.json` stores a deadline or the explicit string `never`.
  A Funnel entry with no lifetime is refused as `funnel_expiry_required`
  (exit 2 by `registry check`; as a service issue in the daemon). `list` and
  `status` do not report such an entry as never expiring.
- The MCP `share` tool reuses an existing service only when explicit tags
  match and its Funnel deadline fits the requested `funnel_ttl`. A mismatch
  is `conflict`. An identical expired timed share is re-armed; the result
  includes `funnel_rearmed: true`. `funnel_expires_at` is the actual returned
  deadline and is omitted for tailnet-only or never-expiring shares. CLI and
  MCP share results include `exposure` (`kind`, `display`, `public`) and may
  include `warnings`; CLI `share` creates tailnet-only shares.
- Reserved registry fields `domain`, `acme_email`, and `middleware` are
  refused with `unknown_config_key` (exit 2). Custom-domain ACME and
  middleware are roadmap features, not active runtime controls.
- A recreated registry with existing node state produces
  `registry_recreated_with_node_state` in `add` and `share` results and
  names the affected services.
- `serve` isolates a service entry with a missing directory, refused target,
  invalid tag, or invalid Funnel combination. `status` and `doctor` report
  the service's code while other services run. Invalid registry JSON and
  registry-level errors still stop startup. An expired Funnel beside an
  invalid service is withdrawn in memory; the registry rewrite waits until
  the invalid entry is repaired or removed.
- The daemon stores per-service startup identity in
  `node-identities/<name>.json`. A Tier 2 tag, Funnel, ephemeral, or control
  URL change clears the old local identity, deletes its recorded tailnet
  device by NodeID, and enrolls a replacement. On Tier 1, only ephemeral or
  control URL changes reset enrollment; `tags set` and `tags add` do not
  clear Tier 1 node state. An unreadable identity record fails
  its service with `internal_error`, keeps node state, and is retried after
  repair. A record from a newer version is left untouched.
- Service names use DNS-label syntax and refuse `con`, `prn`, `aux`,
  `nul`, `com1`-`com9`, and `lpt1`-`lpt9` on every platform with
  `invalid_service_name`. `share ./aux` uses `aux-share`.
- `config.json` accepts only known keys. Malformed JSON, unknown keys, or
  trailing data produce `config_load_failed` (exit 2) for settings writers;
  `doctor` reports it. `serve` logs the error and uses its default control
  URL without MCP. `config set` and `tags set-default` serialize through
  `config.json.lock`. Credential changes use owner-only lock files in the
  config directory and, with the default keychain, in the OS account home;
  a five-second lock conflict makes `login` or `logout` return `conflict`
  (exit 4).
- `runtime.json` has a private integer `schema_version` of 1. A malformed
  or incompatible snapshot reports `runtime_snapshot_unreadable`; a
  well-formed snapshot that does not match the registry reports
  `runtime_snapshot_stale`. The CLI, daemon, and invite views fingerprint
  the registry consistently, including isolated bad entries.
- `status` reports `authenticated` and `auth_status: "authenticated"` only
  after a service node is authorized. `credential_stored` reports a stored
  credential separately. A missing PID file yields `daemon_state: "absent"`;
  status distinguishes `running`, `absent`, and `unknown`. Without a credential
  or authorized node, `next` is `tslink install` for a registered service with
  no daemon, `tslink status --json` while a running daemon enrolls it, or empty
  if no service is registered. MCP status and events include `daemon_state`.
- The public view `schema_version` inside result `data` is the integer `1`.
  `doctor --json` sets envelope `code` to its diagnostic exit code (0, 64, or
  65). `enrollment_required` exits 3.
- MCP `logs` `since`, MCP `url` `wait`, `login --expires-in`, and
  `events_keepalive` accept Go duration syntax plus `d` for days. MCP
  `funnel_ttl` accepts equivalent spellings of its five timed lifetimes and
  `never` (`168h` equals `7d`); each option keeps its own bounds. CLI `--wait`
  and `--funnel-ttl` retain their existing syntax. `funnel_remaining` uses Go
  duration spelling, `0s` after a deadline, and `never` without one.
- `doctor` on an empty, credential-free install exits 0 and reports
  `credential_none` as info. Set `TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1` to skip
  its local Tailscale SSH check; the finding is `tailscale_ssh_unknown` and
  the diagnostic status and exit code do not change. A single-file share is
  displayed as `backend.kind: "file"` with its full path.
- An early tsnet node start failure is reported for that service instead of
  crashing the daemon; MCP control-plane startup failure still stops it.
  `login` client-secret validation reports an early failure as a validation
  error.
- The proxy relays final backend statuses after informational responses and
  counts WebSocket upgrades as 101 in access logs and request totals.

### Agents and MCP

- Every command except the stdio `tslink mcp` server accepts `--json` and
  returns a versioned result envelope. The local MCP server and optional
  tailnet-only `serve --mcp` control plane expose the same 19 tools. Remote
  access requires `mcp.allow`; a `tag:` principal authorizes every machine
  carrying that tag, so a login email or dedicated tag is safer.
- The MCP `url` tool caps `wait` at five minutes. On stdin EOF, `tslink mcp`
  finishes calls already read but cancels a call still running after six
  minutes, then exits after a five-second grace period. SIGINT and SIGTERM
  cancel in-flight calls; a second signal terminates immediately.
- Credential metadata writes use the credential lock, so concurrent status
  backfills, login, logout, and `doctor --probe-remote` do not replace a
  newer credential record. If a legacy `apikey` file differs from the
  keychain value, `serve` keeps the keychain value and leaves the file for
  inspection; an unreadable keychain also leaves the file untouched.
- A JSON-RPC batch or oversized record ends an MCP stdio session before any
  part is dispatched. A cancelled MCP `unshare` or CLI `remove` interrupts
  remote cleanup and reports a `device_warning` while keeping the local
  removal.
- The `share` and `add` tool descriptions specify that target blocking
  checks literal IPs and `metadata.google.internal`, without DNS resolution
  or a connect-time check.
- A refused MCP call returns `isError: true` with one text item containing a
  JSON failure object (`code`, `message`, `next`, `data`) and no
  `structuredContent`. Invalid tool arguments return a `usage_error` tool
  result naming the tool and invalid field.
- MCP `unshare` returns the `tslink remove --json` result, including
  `node_state_kept_reason` when applicable. It deletes the service and,
  subject to ownership checks, its recorded tailnet device and local node
  state. The server instructions ask clients to confirm the deletion.
- Every MCP tool declares `readOnlyHint`, `destructiveHint`, `idempotentHint`,
  and `openWorldHint`. MCP `share`, `add`, and `template_apply` include
  `daemon_installed` (`manager`, `path`, `undo`) when their call installed the
  background service. The `add` tool describes how a custom `control_url`
  keeps the stored Tailscale credential and keys minted from it off that
  server.
- The manifest derives `error_codes` from the same table as process exits and
  service isolation. Its codes include `daemon_not_running`,
  `daemon_setup_failed`, `daemon_supervision_unverified`,
  `enrollment_required`, `invalid_service_config`,
  `link_local_target_refused`, `registry_reload_invalid`,
  `runtime_snapshot_missing`, `runtime_snapshot_stale`,
  `runtime_snapshot_unreadable`, and `mcp_elevated_invite_refused`.
  The manifest describes the binary without a `release` block or
  `toolchain.goreleaser_version` and `toolchain.homebrew_artifact` fields.
  `high_risk_operations` includes
  `tslink remove`, MCP `unshare`, and daemon lifecycle deletion.

### Platforms and release checks

- macOS uses a LaunchAgent, Linux a systemd user unit, and Windows a Startup
  script. `install` and `uninstall` bound service-manager queries to two
  seconds with one retry and state changes to 120 seconds; an uncertain
  supervisor state is never treated as stopped or unowned.
- Linux `uninstall` keeps the unit and fails if `systemctl --user stop` fails
  and shutdown is unconfirmed. It also reports a running unit or enabled
  link left behind after its unit file disappears, with manual recovery
  commands. Windows reports `windows-startup` and warns that Startup has no
  crash restart; `stop` clears stale daemon identity artifacts, `install`
  writes `tslink.vbs` atomically, and `tslink.err.log` rotates at 8 MiB with
  one archive.
- Ownership-ledger read errors report the ledger path literally, including
  Windows backslashes.
- Windows uses `%AppData%\tslink\` for configuration. An old
  `%USERPROFILE%\.config\tslink\` is never moved automatically: if it is
  the only directory, commands refuse with `legacy_config_dir_present`
  (exit 4) and provide a `move` command; if both exist, they refuse to pick
  one. macOS and Linux use `~/.config/tslink/` by default.
- Windows legacy-directory guidance provides a runnable `move` command.
  An invalid registry entry is isolated without inheriting fields from the
  previous entry; `doctor` reports `registry_service_invalid` for it.
- `go run ./tools/gen-manifest` writes the same committed manifest on macOS,
  Linux, and Windows, and `-check` detects a stale fixture on each platform.
  `scripts/check.sh` runs the portable release checks.
- CI runs the Release Candidate gate on pull requests and pushes to `main`;
  isolated tests use test-owned state rather than a contributor's TSLink
  configuration, Tailscale LocalAPI, or service manager.
  The local release verifier reports `BLOCKED` when it has no archives.
  `golang.org/x/crypto` is at v0.57.0, beyond GO-2026-6355,
  GO-2026-6354, and GO-2026-6303; those advisory paths are not reachable
  from TSLink code.

### Compatibility (0.x)

For later 0.x releases, these are the public automation and data contracts:

- `--json` keeps the `tslink.result` envelope and its `type`, `ok`,
  `schema_version`, `command`, `code`, `data`, and `error` fields. Optional
  `data`, `error`, and `error.next` stay optional; fields may be added.
- Documented command `data` fields keep their meaning; fields and warnings
  may be added. Public view `data.schema_version` stays an integer.
- Published error codes keep their meanings and exit-code mapping. Codes may
  be added; exit classes 0, 1, 2, 3, 4, 5, 64, and 65 stay as documented.
  `tslink manifest --json` derives `error_codes` from this mapping.
- `registry.json` schema 1 and known `config.json` keys remain readable;
  fields may be added. Run `registry check` before an upgrade.
- The 19 MCP tool names and output schemas remain available with additive
  fields; tools and optional fields may be added. Refusals remain tool results
  with `isError: true`, one JSON text failure object (`code`, `message`,
  `next`, optional `data`), and no `structuredContent`; schema argument
  errors use `usage_error`. The four behavior annotations remain present on
  every tool.
- Human output, error messages, and logs are not parsing interfaces. Use
  `--json` instead. `runtime.json` and other daemon/CLI state files are private.
- Go packages are not a library API. A future incompatible 0.x change needs
  a migration note; private pre-release behavior is not a baseline.
