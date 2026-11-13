# TSLink operations RUNBOOK

## Prerequisites

- Go **1.27.1+**; builds prefer **go1.27.4** via `go.mod` toolchain (see decisions ledger for CVE notes).
- macOS 13+ for supported Go darwin builds.
- Tailscale account; optional admin token/OAuth for Tier 2 durable installs.

## Build, vet, test, install

```bash
go build ./...
go vet ./...
go test ./...
go install .
export PATH="$PATH:$HOME/go/bin"
```

Ensure `tslink --help` works. Repo uses `scripts/check.sh` with `staticcheck@v0.8.2` (2026.2.2).

## PATH and local state

- Registry: `~/.config/tslink/registry.json`, `nodes/`, `logs/`, `tslink.pid`.
- Do not hand-edit `registry.json`; use `add`/`remove`/`tags`.

## Daemon lifecycle

```bash
tslink add <name> --proxy host:port   # installs supervision when needed
tslink status
tslink logs --last 20
tslink stop
tslink install                        # macOS login autostart
tslink uninstall                      # before stop if disabling KeepAlive autostart
```

- Linux headless user service: `loginctl enable-linger "$USER"` when needed; disable if only for TSLink.
- Stale tailnet nodes: `tslink stop && tslink serve --daemon` (ownership-safe cleanup when credentialed).

## Troubleshooting (quick)

| Symptom | Action |
|---|---|
| not authenticated | `serve` + browser URL, or Tier 2 stdin login |
| already running | `stop` then retry |
| Keychain denied | restore access; stdin login |
| launchd restarts after stop | `uninstall` first when disabling autostart |
| no Aqua / `gui/$(id -u)` | GUI-domain follows uid's **Aqua** session, not caller transport. **First install** → `user/$(id -u)` fallback. **Upgrade** refuses if prior domain uncheckable → `launchctl_domain_unavailable` exit 1 with `unavailable_domain`, `force_available`, `force_command`, `force_risk`. |
| binary not found | add `$HOME/go/bin` to PATH |

## Cross-platform notes

- Windows Task Scheduler: session supervisor, exponential daemon backoff (1–60s, breaker after 8 unstable runs).
- macOS: Xcode license can break `go build` VCS stamping (exit 69).
- Upgrading Tailscale module: watch for transitive `go-json-experiment/json` API drift.

## Agent operations reminder

- Renew expiring API tokens from admin console.
- Keep upstream `localhost` service running behind proxy routes.
- Check `status` supervision fields; `--no-daemon-install` is explicit escape hatch for CI.
- `tslink url <name> --wait` never derives hostname from service name alone.
