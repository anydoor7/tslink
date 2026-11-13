# AGENTS.md

Operating manual for AI agents on **TSLink** (public repo `anydoor7/tslink`). Humans start at [CONTRIBUTING.md](CONTRIBUTING.md).

> **Budget (W0345)**: root `<=8KiB`; each `CONTRACT.md` and [docs/RUNBOOK.md](docs/RUNBOOK.md) `<=4KiB`. History: [docs/architecture/decisions-ledger.md](docs/architecture/decisions-ledger.md).

## Project overview

TSLink is a **private Tailscale gateway**: local HTTP/file/TCP services exposed through embedded **tsnet** nodes, with registry-backed hot reload and optional Keychain credentials. Shipped core covers microsegmentation and identity-aware HTTP proxying; admin Docker discovery, cluster sync, public `/metrics`, and custom-domain ACME remain roadmap (no shipped package).

New installs default to a persistent home gateway. Private apps get stable routes and per-service nodes; PortalOnly stays opt-in. **Human access adapters** use `grantAccess` with locked registry mutation, then exact runtime and durable invitation completion; no allow-only success, `--invite` gating, blind unknown-POST retries, or member-only permanent grants; device guests may use `never` with acknowledgment; browser/Funnel and scoped binding limits stay finite. First owner bootstrap uses only the untagged gateway self identity.

## Documentation policy

English-only in repo docs; homepage translations live under `docs/README.<lang>.md` only.

## Build and test

```bash
go build ./...
go vet ./...
go test ./...
go install .
```

Put `$HOME/go/bin` on `PATH` for the `tslink` binary.

**Pinned intent**: `tailscale.com v1.104.1`, `tailscale.com/client/tailscale/v2 v2.11.0`, `go-keyring v0.2.8`, `staticcheck@v0.8.2` (2026.2.2); Go floor 1.27.1 / toolchain 1.27.4.

## Architecture and dependency boundary

```
cmd/                 → Cobra CLI (login, serve, add, remove, list, status, …)
internal/config/     → ~/.config/tslink registry paths
internal/credentials/→ Tier 1/2 secrets, tags, auth keys
internal/registry/   → JSON registry load/save, grantAccess, request_limits
internal/server/     → Multi-node tsnet, proxy/file/TCP, hot-reload, SSE
internal/daemon/     → PID, signals, supervision identity
```

**Upstream**: `tailscale.com` (tsnet, LocalClient), Tailscale REST v2, `go-keyring`, Cobra, fsnotify.  
**Local truth**: `registry.json` + runtime nodes under `~/.config/tslink/`.  
**Downstream**: tailnet clients reach per-service URLs after enrollment.

### Plan B / failure modes

- Tier 1: interactive enrollment URL per node; no remote ACL until user completes login.
- Tier 2: stored credentials with scope-limited REST; tag ensure may degrade without policy scope.
- Keychain unreachable → explicit login failure (Windows: no file fallback via `fileCredentialFallbackEnabledFunc`).
- Setup failures may **retain registry** on `daemon_supervision_unverified` / `daemon_setup_failed` (`add`/`template apply`/MCP `add` → `"Configuration remains in the registry"` + `tslink list`); **`share` only requests setup when stopped and rolls back a new registration on failure**.
- `remove` remote cleanup only with **ownership proof**.
- **macOS launchd**: GUI-domain `gui/$(id -u)` availability follows the uid's desktop (**Aqua**) session, not how the caller connected. First install falls back to `user/$(id -u)`; upgrade refuses if a prior domain cannot be checked and returns `launchctl_domain_unavailable` (exit 1) with `unavailable_domain`, `force_available`, `force_command`, `force_risk`.

## Security, licensing, and agent red lines

1. **Secrets**: inject via **environment or stdin flags** in automation — never argv or shell history (`--api-key-stdin`, `--client-secret-stdin`).
2. **Funnel**: requires explicit **`--public`** on add or `public_ack: true`; default-on Funnel provisioning has **`--no-auto-provision`** kill switch.
3. **Registry**: do not hand-edit; use CLI/MCP commands.
4. **Ownership-checked removal**; do not bypass supervision gates without documented flags.
5. MCP `logs` redaction is not a secrecy boundary (CLI prints full logs); see `SECURITY.md` for delimiter boundaries.
6. **Licensing**: `LICENSE` is unmodified Apache 2.0; no mandatory fees/registration/telemetry/revenue thresholds in docs; `COMMERCIAL.md` voluntary; `CONTRIBUTING.md` preserves attribution and contributor rights; code license does not grant Tailscale service or resale rights.

## Contract routing

| Document | Scope |
|---|---|
| [cmd/CONTRACT.md](cmd/CONTRACT.md) | Commands, `--json`, Funnel gate, install/launchctl, MCP, `url --wait` |
| [internal/server/CONTRACT.md](internal/server/CONTRACT.md) | Gateway flow, health probes, `serverNowFn`, HTTP limits, SSE |
| [internal/credentials/CONTRACT.md](internal/credentials/CONTRACT.md) | Tier 1/2, tags, Keychain, locks, login slots |
| [internal/registry/CONTRACT.md](internal/registry/CONTRACT.md) | `grantAccess`, `request_limits`, `CodedError.Next` vs manifest |
| [internal/daemon/CONTRACT.md](internal/daemon/CONTRACT.md) | Supervision, `daemon_identity_unverified`, autostart scope |
| [docs/RUNBOOK.md](docs/RUNBOOK.md) | Prerequisites, build/PATH, daemon install, troubleshooting |
| [docs/architecture/decisions-ledger.md](docs/architecture/decisions-ledger.md) | Toolchain history, pitfalls, extended walkthroughs |

Update this file and affected `CONTRACT.md` in the **same commit** when changing behavior or policy.
