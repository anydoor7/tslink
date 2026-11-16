# TSLink — agent entry

Operating manual for AI agents on **TSLink** (public `anydoor7/tslink`). Humans: [CONTRIBUTING.md](CONTRIBUTING.md).

> **Budget (W0345)**: root `<=8KiB`; each `CONTRACT.md` and [docs/RUNBOOK.md](docs/RUNBOOK.md) `<=4KiB`. History: [docs/architecture/decisions-ledger.md](docs/architecture/decisions-ledger.md).

## Positioning & stack

Private Tailscale gateway: local HTTP/file/TCP via embedded **tsnet** nodes, registry hot reload, optional Keychain creds. Shipped: microsegmentation + identity-aware HTTP proxy; roadmap (unshipped): admin Docker discovery, cluster sync, public `/metrics`, custom-domain ACME.

Default persistent home gateway; PortalOnly opt-in. Access via `grantAccess` + locked registry mutation; no allow-only success, blind unknown-POST retries, or member-only permanent grants; device guests may use `never` with ack. First owner bootstrap uses untagged gateway self identity only. Docs English in repo; homepage i18n under `docs/README.<lang>.md`.

**Pinned deps:** `tailscale.com v1.104.1`, `client/tailscale/v2 v2.11.0`, `go-keyring v0.2.8`, `staticcheck@v0.8.2`; Go ≥1.27.1, toolchain 1.27.4.

## Build & test

```bash
go build ./... && go vet ./... && go test ./... && go install .
```

Put `$HOME/go/bin` on `PATH` for `tslink`.

## Architecture & Plan B

```
cmd/ → Cobra CLI; internal/config/ registry paths; internal/credentials/ tiers;
internal/registry/ JSON + grantAccess; internal/server/ tsnet proxy + hot-reload;
internal/daemon/ PID/supervision
```

**Upstream:** tailscale tsnet/LocalClient, REST v2, keyring, Cobra, fsnotify. **Truth:** `~/.config/tslink/registry.json` + runtime nodes. **Downstream:** tailnet clients after enrollment.

- Tier1: interactive enroll URL/node; Tier2: scoped REST; tag ensure may degrade.
- Keychain fail explicit (Windows no file fallback unless enabled).
- Setup fail may retain registry on `daemon_supervision_unverified`/`daemon_setup_failed` (`add`/`template`/`mcp add`); `share` only requests setup when stopped, rolls back new reg on fail.
- `remove` remote cleanup needs ownership proof.
- macOS launchd: GUI domain needs Aqua session; fallback `user/$(id -u)`; upgrade refuses with `launchctl_domain_unavailable` (exit 1) if prior domain unchecked.

## Security red lines

1. Secrets via env/stdin flags only — never argv (`--api-key-stdin`, `--client-secret-stdin`).
2. Funnel needs explicit `--public` or `public_ack: true`; `--no-auto-provision` kill switch.
3. No hand-edit registry; use CLI/MCP.
4. Ownership-checked removal; no bypassing supervision without documented flags.
5. MCP `logs` redaction not a secrecy boundary; see `SECURITY.md`.
6. Apache 2.0 `LICENSE`; no mandatory fees/telemetry in docs; `COMMERCIAL.md` voluntary.

## Contract & contributor routing (1-hop)

| Document | Scope |
|---|---|
| [cmd/CONTRACT.md](cmd/CONTRACT.md) | Commands, `--json`, Funnel, install/launchctl, MCP |
| [internal/server/CONTRACT.md](internal/server/CONTRACT.md) | Gateway, probes, HTTP limits, SSE |
| [internal/credentials/CONTRACT.md](internal/credentials/CONTRACT.md) | Tiers, Keychain, locks |
| [internal/registry/CONTRACT.md](internal/registry/CONTRACT.md) | `grantAccess`, `request_limits`, errors |
| [internal/daemon/CONTRACT.md](internal/daemon/CONTRACT.md) | Supervision, autostart |
| [docs/RUNBOOK.md](docs/RUNBOOK.md) | Prerequisites, daemon, troubleshooting |
| [docs/architecture/decisions-ledger.md](docs/architecture/decisions-ledger.md) | History |
| [CONTRIBUTING.md](CONTRIBUTING.md) | CI policy, local checks |
| [docs/release-artifacts.md](docs/release-artifacts.md) | Release payload & CI gate |
| [.github/scripts/INDEX.md](.github/scripts/INDEX.md) | CI classifiers |
| [llms.txt](llms.txt) | Compact discovery index |
| [docs/agent-integrations.md](docs/agent-integrations.md) | Agent setup & plugin validation |
| [skills/INDEX.md](skills/INDEX.md), [.claude-plugin/INDEX.md](.claude-plugin/INDEX.md) | Skills & marketplace |
| [third_party/INDEX.md](third_party/INDEX.md) | Pinned dependency repairs |

Behavior/policy changes: update this file and affected `CONTRACT.md` in the **same commit**.