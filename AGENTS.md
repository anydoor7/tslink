# AGENTS.md

This file is the operating manual for AI agents working on or with TSLink.
Human contributors should start from [CONTRIBUTING.md](CONTRIBUTING.md).

## Project Overview

TSLink is a private Tailscale gateway that exposes local proxy, file, and TCP services through embedded tsnet nodes. It implements the shipped core with per-service microsegmentation, identity-aware HTTP proxying, WireGuard encryption, registry-backed hot reload, and system keychain credential storage. Admin, Docker discovery, middleware, cluster sync, custom-domain ACME, and an exposed `/metrics` endpoint are roadmap items with no shipped package.

## Build & Development

```bash
go build ./...        # Build all packages
go vet ./...          # Static analysis
go test ./...         # Run tests
go install .          # Install to $GOPATH/bin
```

Ensure `$HOME/go/bin` is in PATH to use the `tslink` binary.

## Architecture

```
cmd/           → Cobra CLI commands (login, serve, add, remove, list, status, stop, install, logout, tags)
internal/
  config/      → Paths: ~/.config/tslink/{registry.json, nodes/, logs/, tslink.pid}
  credentials/ → Unified credential manager (Keychain + file fallback + auth key derivation)
  daemon/      → PID management, daemonize, signal handling
  registry/    → Service registry (JSON load/save, add/remove)
  server/      → Multi-node tsnet server, proxy handler, file handler, TCP handler, hot-reload
  tailapi/     → Tailscale API device management (delete stale nodes)
```

### Credential System

- **Tier 1 default**: no stored credential. `serve` starts each fresh tsnet node with an empty `AuthKey`, no `AdvertiseTags`, and no remote ACL/device API calls. It captures the interactive URL from stable `LocalClient.Status().AuthURL`; `--json` keeps a daemon child alive and returns a versioned `needs_login` handoff immediately. User-owned node keys eventually expire and may require re-authentication.
- **Tier 2 opt-in**: stored credential for tagged, durable multi-service installs. This path preserves the prior per-service auth-key, tag, ACL opt-in, and cleanup semantics.
- **Two credential types**: API access token (`tskey-api-*`) with periodic renewal, or OAuth client secret (`tskey-client-*`) that does not expire
- **Storage**: System keychain via `go-keyring` (macOS Keychain / Linux secret service / Windows Credential Manager, service: `tslink`). On macOS and Linux, `~/.config/tslink/apikey` or `~/.config/tslink/clientsecret` (0600) is a conditional fallback: TSLink must prove any stale keychain credential absent or removed before reporting file success. A completely unreachable or uncertain keychain causes explicit login failure; restore keychain access and retry. Headless operation alone does not guarantee fallback. Windows has no file fallback (`internal/credentials/credentials.go`, `fileCredentialFallbackEnabledFunc`), because TSLink cannot prove a user-only DACL locally, so `tslink login` fails there when Credential Manager is unavailable
- **Auth key derivation**: For API tokens, `credentials.GetAuthKey()` calls the Tailscale `CreateKey()` API at service start. The serve path resolves per-service auth material with that service's tags, ephemeral setting, and service-scoped description. It no longer derives a process-wide union key.
- **OAuth direct auth and API access**: For OAuth client secrets, tsnet authenticates directly and `credentials.NewTailscaleClient()` uses OAuth for REST operations. The client's granted scopes still determine whether tag, policy, settings, and device operations succeed; missing policy access degrades ordinary tag ensure instead of blocking every service.
- **Legacy support**: If no API access token or OAuth secret exists but `~/.config/tslink/authkey` is present, it's used directly (backward compat)
- **Migration**: `credentials.MigrateFromLegacy()` moves file-based credentials into Keychain automatically on `serve`

### Tag System

- **Default tag**: `tag:tsmain` is auto-applied to every service when `--tags` is not specified on `tslink add`
- **Funnel tag**: `tag:tslink-funnel` is derived additively for Funnel auth and tsnet node construction; it is not auto-persisted in `registry.json`, and removing it with `tags set` does not disable the derived runtime identity
- **Configurable default**: change the default tag with `tslink tags set-default <tag>`
- **ACL auto-creation**: API access tokens and OAuth client secrets can create the default tag and ensure registry tags before startup when their scopes permit it.
- **Tag grammar**: Tags must match `tag:<lowercase-hyphen-name>` with lowercase letters, numbers, and hyphens. Migrate legacy values with `tslink tags set <service> tag:<lowercase-hyphen-name>` or by editing `registry.json`.
- **Hot-reload**: tag, ephemeral, and effective control URL changes restart affected nodes with local state removed and fresh per-service auth material. Credential mode swaps and legacy `authkey` file changes still require a `tslink serve` process restart.
- **Tag subcommands**:
  - `tslink tags list` — list all services and their assigned tags
  - `tslink tags pull` — fetch remote tags currently defined in Tailscale ACL with an API access token or OAuth client secret; insufficient OAuth policy scope returns the remote authorization error
  - `tslink tags add <service> <tag>` — append a tag to a service (node restarts)
  - `tslink tags set <service> <tag>` — replace a service's tags entirely (node restarts)
  - `tslink tags set-default <tag>` — change the default tag applied to new services
  - `tslink tags delete-remote <tag> --force --manage-acl` — remove the ACL tag owner rule globally after local safety checks and explicit remote ACL opt-in

### Service Flow

1. `tslink add <name> --proxy host:port` → default tag `tag:tsmain` remains in registry when `--tags` is not specified → writes registry.json first, then verifies the live daemon's supervision or installs and settles the OS background service when absent, and waits for an exact URL or enrollment URL. `--no-daemon-install` saves configuration only; offline output has no green check.
2. Default `tslink serve` with no credential → starts each untagged user-owned tsnet node → emits one browser URL per fresh service node → waits through `LocalClient.Status()` until `Running`; no ACL/device API is called.
3. Optional `tslink login` → interactive administrative credential selection (API access token or OAuth client secret) → stored in Keychain. Ordinary remote Tailscale ACL mutation is default-off and requires explicit `--manage-acl`; default-on Funnel provisioning has the documented `--no-auto-provision` kill switch.
4. Credentialed `tslink serve --daemon` → resolves per-service auth material → starts tagged tsnet nodes → hot-reload watches registry and restarts changed services. API tokens and appropriately scoped OAuth clients can ensure tags and attempt ownership-safe device cleanup.
5. `tslink remove <name>` → removes from registry + attempts remote cleanup only when exact ownership proof is available

### Key Dependencies

- `tailscale.com v1.102.4` — tsnet (embedded nodes) + `client/tailscale` (LocalClient for identity verification)
- `tailscale.com/client/tailscale/v2 v2.10.1` — Tailscale REST API client (ACL management, device management, auth key derivation)
- `github.com/zalando/go-keyring v0.2.6` — cross-platform keychain
- `github.com/spf13/cobra` — CLI framework
- `github.com/fsnotify/fsnotify` — registry hot-reload

### Toolchain notes

**Go version (`go.mod`, `go 1.26.6`)**: the module floor is 1.26.6 and there is no
separate `toolchain` line. `tailscale.com@v1.102.4` itself requires `go 1.26.6`, so
`go mod tidy` raised the floor to match and dropped the now-redundant toolchain
directive. Raise the floor only when a dependency or a stdlib CVE forces it;
`toolchain` is a floor, not a pin, so a newer local Go is used as-is.

**Builds on Go 1.27+ natively.** If a compile error appears inside the module cache
referencing `json.SkipFunc` or `json.DiscardUnknownMembers`, check the transitive
`go-json-experiment/json` version before reaching for a `GOTOOLCHAIN` override. Those
are `encoding/json/v2` experimental API that 1.27 changed, and the fix is upgrading
`tailscale.com`, which carries a newer transitive pin. The error names the module
cache rather than this repository, so it reads like a local toolchain problem when it
is a dependency problem.

**`staticcheck` must be built with the same Go release it analyzes.** A staticcheck
compiled by 1.26 reports `export data version 4 is greater than maximum supported
version 2` against 1.27 sources, and one that is merely old reports the same thing —
two different causes, one message. Install it with the toolchain you build with and
without a `GOTOOLCHAIN` override, pinned to the version CI uses
(`STATICCHECK_VERSION` in `.github/workflows/release-candidate.yml`):
`go install honnef.co/go/tools/cmd/staticcheck@v0.7.0`. Note the release name and the
module version differ -- release 2026.2.1 is module `v0.8.1` -- so a version reported
by `staticcheck -version` reads as two numbers for the same build.

**macOS: `git` belongs to Xcode.** After an Xcode major upgrade the license resets and
`/usr/bin/git` exits 69 until `sudo xcodebuild -license accept` is run. `go build`
shells out to git for VCS stamping, so this surfaces as `error obtaining VCS status:
exit status 69` — a build failure with no obvious connection to Xcode.



---

## For AI Agents: Self-Installation & Usage Guide

This section teaches an agent how to install, configure, and operate tslink without human intervention.

### Prerequisites

- Go 1.26.6+ installed
- A Tailscale account. The default zero-credential path needs no admin-console token.
- Optional, only for the durable Tier 2 path, one of:
  - API access token ([generate here](https://login.tailscale.com/admin/settings/keys)) — expires periodically
  - OAuth client secret ([generate here](https://login.tailscale.com/admin/settings/oauth)) — does not expire, but validate tag/device automation before relying on it unattended
- Tailscale app installed on devices that will access exposed services

### Step 1: Install tslink

```bash
# Clone and install
git clone https://github.com/anydoor7/tslink.git /tmp/tslink-install
cd /tmp/tslink-install && go install .

# Ensure binary is in PATH
export PATH="$PATH:$HOME/go/bin"
# Add to shell profile for persistence:
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.zshrc  # or ~/.bashrc
```

Verify: `tslink --help` should show the command list.

### Step 2: Authenticate (optional Tier 2)

Skip this step for the default user-owned-node path. `tslink login` stores an administrative credential for tagged, durable multi-service installs and supports both interactive and non-interactive credential input.

```bash
# Normal path:
tslink login

# Non-interactive automation: inject secrets from a secret manager into
# environment variables or stdin so they do not appear in argv or shell history.
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
TSLINK_API_KEY="$TSLINK_API_KEY" tslink login
TSLINK_CLIENT_SECRET="$TSLINK_CLIENT_SECRET" tslink login

# JSON output for programmatic consumption:
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin --json
```

Credential source contract: provide exactly one explicit credential source, either the recommended stdin flag (`--api-key-stdin` or `--client-secret-stdin`) or the compatibility argv flag (`--api-key` or `--client-secret`). A single explicit source wins over `TSLINK_API_KEY` / `TSLINK_CLIENT_SECRET`; environment variables win over interactive fallback. In `--json` mode, interactive login is disabled. The compatibility `--api-key` and `--client-secret` argv flags remain accepted, but do not use them in official automation guidance because argv can leak through shell history or local process inspection. Legacy file-based credentials are read for compatibility and may be migrated to Keychain, but direct `echo`/`printf` writes to credential files are not recommended.

### Step 3: Register Services

```bash
# Expose a local web server
tslink add myapp --proxy localhost:3000

# Expose a file directory
tslink add docs --dir ~/Documents/shared

# Public exposure via Tailscale Funnel requires explicit acknowledgement.
# Existing legacy Funnel entries must be re-added with --public or edited to set "public_ack": true.
tslink add public --proxy localhost:3000 --funnel --public

# List registered services
tslink list
```

### Step 4: Check the Gateway

```bash
# add already installed the background service; inspect liveness and supervision
tslink status

# View logs (built-in)
tslink logs
tslink logs --last 20 --level error
```

### Step 5: Access from Other Devices

Each service gets its own hostname: `https://<service-name>.<tailnet-name>.ts.net`

To find the exact URL, check the daemon log output for lines like:
```
node "myapp" ready: https://myapp.tail12345.ts.net
```

### Common Operations

```bash
# Remove a service (remote cleanup is protected unless ownership is proven)
tslink remove myapp

# Stop the daemon (Windows currently force-terminates the process)
tslink stop

# Auto-start on macOS login
tslink install

# Full reset
tslink stop && tslink logout
```

### JSON Output & Exit Codes

All commands support `--json` for structured output:

```bash
tslink status --json | jq .
tslink list --json | jq '.data.services'
tslink add testapp --proxy localhost:3000 --json | jq '.data.created'
tslink logs --last 10 --json
```

Every `--json` command shares one envelope:

```json
{
  "ok": true,
  "schema_version": 1,
  "command": "status",
  "code": 0,
  "data": {
    "daemon_running": true,
    "daemon_pid": 4242,
    "authenticated": true,
    "auth_status": "authenticated",
    "service_count": 1,
    "services": [{"name":"myapp","status":"up"}]
  }
}
```

A zero-credential `tslink serve --json` returns immediately while its daemon child waits for the browser round trip:

```json
{"type":"tslink.result","ok":true,"schema_version":1,"command":"serve","code":0,"data":{"status":"needs_login","auth_url":"https://login.tailscale.com/a/example","expires_at":"2026-08-15T12:00:00Z","poll":"tslink status --json"}}
```

Action-specific fields live under `data`, for example `data.services`, `data.status_urls`, `data.template_plan`, or `data.template_apply`. Failures use `error.code` for stable machine handling and `error.message` for human text.

Semantic exit codes for programmatic error handling:

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error |
| 2 | Usage error (invalid arguments) |
| 3 | Authentication error |
| 4 | Conflict (e.g., already running) |
| 5 | Not found (e.g., service doesn't exist) |
| 64 | Diagnostic warning threshold |
| 65 | Diagnostic critical threshold |

`launchctl_domain_unavailable` is a stable `error.code` at exit 1, not an unexpected internal failure. On the conservative macOS install/uninstall refusal, failure `data` includes `unavailable_domain`, `force_available`, the exact `force_command`, and `force_risk`.

### Troubleshooting

| Symptom | Fix |
|---|---|
| `not authenticated` | Run `tslink serve`; open the emitted URL or use `--no-browser` and open it elsewhere. For the optional durable Tier 2 path, pipe a secret-manager-provided value into `tslink login --api-key-stdin` or `tslink login --client-secret-stdin`; do not write credential files directly. |
| `already running` | `tslink stop` then retry |
| macOS LaunchAgent restarts after `tslink stop` | Expected with launchd `KeepAlive`; restart is deterministic and throttled by `ThrottleInterval=30`. Run `tslink uninstall` before stopping when autostart should be disabled |
| macOS has no desktop (Aqua) session for the uid, so `gui/$(id -u)` is unavailable | GUI-domain availability follows the uid's Aqua session, not whether the caller used SSH. A first install falls back to `user/$(id -u)`; an upgrade refuses if a prior domain cannot be checked and reports the structured `--force` recovery contract. |
| Linux user service stops after logout | `loginctl enable-linger "$USER"` for headless systemd user services; after uninstall, run `loginctl disable-linger "$USER"` if lingering was enabled only for TSLink |
| Stale/duplicate nodes in tailnet | `tslink stop && tslink serve --daemon` (attempts ownership-safe stale node cleanup on startup) |
| Binary not found | `export PATH="$PATH:$HOME/go/bin"` |
| Keychain access denied | Restore keychain access, then retry `tslink login` with the stdin login flags if needed. On macOS/Linux, file fallback requires proof that any stale keychain credential is absent or removed; an unreachable or uncertain keychain makes login fail even in a headless session. |
| Need structured output | Use `--json` flag: `tslink status --json` |

### What the Agent Should Maintain

1. **Credential validity**: API access tokens expire periodically and must be renewed from the Tailscale admin console when auth errors appear. OAuth client secrets do not expire, but their configured scopes bound REST automation.
2. **Service lifecycle**: When the local service (e.g., `localhost:3000`) stops, tslink keeps the node alive but requests will fail. Ensure the upstream service is running.
3. **Daemon health**: Check `tslink status` and its `supervision` fields. A missing daemon is repaired with `tslink install`; `manual` means restart ownership is unverified. Linux boot before login requires lingering. Windows Startup has no crash restart. `--no-daemon-install` is the explicit CLI escape hatch; CI and non-TTY use the same automatic installation policy.
4. **Registry consistency**: `~/.config/tslink/registry.json` is the source of truth. Don't edit it manually — use `tslink add/remove`.

## Known Pitfalls

- **Login is dual-slot by default; `--retire-other` is the only path that deletes the other credential.** `tslink login --api-key-stdin` fills the api-key slot and leaves the existing OAuth `client-secret` slot untouched (`cmd/login.go`, `loginReplaceOptions.RetireOther` defaults to false); logging into the same slot again rotates it. `--retire-other` deletes the other slot only after the new value is verified and committed. Earlier builds retired the other slot unconditionally, which is why this entry exists. Before telling anyone "A won't affect B" for a credential-storage claim, verify it against the code path that actually executes on write/delete, not against the fact that the storage keys look distinct. OAuth client secrets are shown once at creation time and are not recoverable after creation — losing one to an unintended `--retire-other` means creating a new client in the admin console.

- **HTTP services default to a 32 MiB upload cap, 10s header timeout, 30s upload read inactivity window and 60s keep-alive idle timeout.** Per-service `request_limits` and the add/share flags override them. Uploads stream without a total-duration timeout; `read_timeout` measures inactivity while reading the body, and backend backpressure is excluded. `unlimited` requires explicit acknowledgement. These protections do not cap response streams or download sizes. See `docs/sharing.md`.

- **`docs/cli-manifest.json`'s `next[]` field is not populated by `tools/gen-manifest`.** Counting "how many entries still need manual follow-up" against this file's `next[]` will always read `0` — that is not evidence that zero manual steps remain, it is evidence the field is empty by construction. The actual per-error-code follow-up guidance lives at runtime in `registry.CodedError.Next` (`internal/registry/registry.go`), not in the generated manifest. Before treating any "count is 0 / all pass / no hits" result from a generated artifact as a conclusion, confirm the field you are counting can actually be non-zero in that artifact.


- Default `add` refuses an already-live daemon with `daemon_supervision_unverified` (exit 1) when supervisor ownership, installation, autostart, or the platform-required restart policy cannot be verified; liveness alone is insufficient. It does not take over the process. launchd/systemd require restart-on-exit; Windows Startup retains its documented sign-in-only/no-crash-restart limitation. `--no-daemon-install` explicitly bypasses this supervision gate.
- CLI `add`/`template apply --yes` and MCP `add`/`template_apply` retain registry configuration on `daemon_supervision_unverified` and `daemon_setup_failed`. At these retaining call boundaries, the human/structured error says "Configuration remains in the registry" and points to `tslink list`. This covers both newly saved services and an all-existing template that skips every entry without writing; it does not claim this invocation saved anything. The error code and recovery commands remain unchanged; inspect `tslink doctor`, resolve the reported supervisor issue, and for a manual daemon run `tslink stop`, then `tslink install`, then retry the original command. A repeated `add` reuses the saved service. `share` has separate existing behavior: it only requests setup when its liveness check says stopped, and rolls back a newly created registration on failure; neither the low-level setup error nor the share rollback path promises a new configuration was saved or retained. Do not infer an unconditional live-supervision check for `share`.
- Bootstrap persists add/template configuration before installation; a missing registry is a valid empty startup. MCP add/template_apply use the same registry-first setup policy with no_daemon_install; share uses the startup and rollback behavior described above. MCP add returns current evidence without a second URL wait.
- Bootstrap judges setup on the supervisor owning a verified, stable process. A missing first business artifact is a statement about the Tailscale coordination server, not a failed install, so it leaves `add` successful with `url_pending`. Installation or supervision-settle failures report `daemon_setup_failed`; after supervision settles, losing or replacing the verified process during the evidence wait also reports that code.
- `supervision.autostart_scope` carries boot-versus-login for per-user supervisors: launchd and Windows Startup are `login`; a systemd user unit is `boot` only with lingering. Lingering is reported, never enabled, because it affects every service the user owns.
- Treat daemon_identity_unverified as uncertain liveness: keep backend probes and inspect the PID/binary/supervisor before install or restart. A PID file naming a live process that is provably a different program is a stopped daemon, not an uncertain one, and both doctor and the install conflict guard treat it that way. windows-startup has sign-in autostart but no crash restart; doctor warns about that limitation. Setup failure can leave a Linux unit installed and retrying; inspect logs and the reported definition before retrying.
- launchd parser regressions must use the fixtures in `cmd/testdata/launchctl/` (provenance in its `README.md`), including nested coalition state and enabled/disabled overrides.

## Clock seam discipline (`serverNowFn`)

`serverNowFn` is a package-level test seam.

**Rule: capture `serverNowFn` on the goroutine that spawns the work and pass the
value down.** A goroutine that can outlive its caller must never read the seam
directly. Every test that stubs the seam restores it from `t.Cleanup`, so a read
from a goroutine the test does not join is a data race whether or not today's
tests happen to trigger it. The accept loop in `startNodeLocked`, the lifecycle
ticker, and the event-stream path all follow this rule; the event-stream clocks
are injected as fields (`eventStateCache.nowFn`, `mcpEventStream.nowFn`)
captured once in `newMCPEventsHandler`.

**Rule: inject clocks far from wall time.** The tests use 2030. With
`time.Now()` on both sides a test cannot tell the injected clock from the real
one, and a regression passes by coincidence.

The property is pinned by behaviour rather than by code shape:
`TestMCPEventsHandlerCapturesTheClockAtConstruction` swaps the seam *after* the
handler exists and asserts the frame still carries the clock installed before
it. Mutating an injected site back to `serverNowFn()` turns a test red, but not
every site has a positive assertion of its own: one of them fails by panicking
on an unrecovered goroutine rather than by a failed assertion.

The remaining reads in `server.go` (`Run`, `syncNodes`, `recordOwnedNodeWithBackoff`,
`startNodeLocked`) are all synchronous within `Run`'s call tree; the one test
that starts `Run` on a goroutine joins it and does not stub the seam.

## Known gaps (last reviewed for v0.1.0)

**The `logs` MCP tool's redaction is a surface property, not a secrecy property.**
The same log file is emitted verbatim by the `tslink logs` CLI; only the MCP tool
sanitizes. Any agent that can run a shell reads the originals. Treat the redaction
as narrowing what incidentally reaches a model's context, never as an isolation
boundary -- a design that relies on it to keep a credential from an agent is
already wrong, because that agent almost certainly has a shell.

**A capability separated from its host by a delimiter is not redacted.** The
two shapes `mcpLogsTailscaleURLPattern` does not cover, why both of its choices
are deliberate, and why widening the token class is not a fix on its own are
documented in [SECURITY.md](SECURITY.md) under Important boundaries.

**The SSE stream emits only named events** (`snapshot` / `update` / `keepalive`)
and never a default `message` event -- verified over real cross-device tailnet
traffic. A client that only sets `onmessage` receives nothing at all, not merely
"no heartbeats". There is no `retry:` field and no `Last-Event-ID` replay; a
reconnect gets a full snapshot. `id:` carries the instance-global `event_id`,
which is a different counter from the per-stream `sequence` -- deduplicate on
`event_id`, and drop cached state whenever `instance` changes.

## Licensing boundary

`LICENSE` is the unmodified Apache License 2.0. All organization sizes may use TSLink commercially under its terms; do not add mandatory fees, registration, notification, telemetry, or revenue/headcount thresholds in documentation. `COMMERCIAL.md` and `COMMERCIAL_zh.md` describe voluntary cooperation and must remain equivalent. Preserve attribution, third-party notices, previously granted rights, and the contributor rights policy in CONTRIBUTING.md. Code licensing does not grant Tailscale service or resale rights.
