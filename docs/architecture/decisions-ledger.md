# TSLink architecture decisions and historical notes

> **Scope**: Extended narratives, toolchain history, agent walkthroughs, and pitfalls extracted during W0345 doc slimming. **Active invariants** live in root `AGENTS.md` and module `CONTRACT.md` files.

## Toolchain and CI history

- **Go module**: floor `go 1.27.1`, `toolchain go1.27.4` for GO-2026-6859/6809 and `golang.org/x/net v0.62.0`.
- **json.SkipFunc / json.DiscardUnknownMembers**: fix by upgrading `tailscale.com`, not local `GOTOOLCHAIN` overrides.
- **staticcheck**: 2026.2.2 (`v0.8.2`) reads Go 1.27.3+ export data; CI uses `go-version-file: go.mod`; install `STATICCHECK_VERSION` from release workflow.
- **macOS git / Xcode**: license reset causes VCS stamping exit 69 until `sudo xcodebuild -license accept`.

## Agent installation walkthrough (extended)

Full step-by-step install, optional Tier 2 `tslink login`, service registration, Funnel `--public`, JSON envelopes, exit-code table, and troubleshooting matrix were consolidated from pre-slim `AGENTS.md`. Operational quick path remains in [`docs/RUNBOOK.md`](../RUNBOOK.md); command contracts in [`cmd/CONTRACT.md`](../../cmd/CONTRACT.md).

Key retained facts:
- Default path needs browser enrollment per fresh node; Tier 2 uses Keychain (Windows: no file fallback).
- Non-interactive login: **stdin or env only**, not argv automation guidance.
- `tslink mcp` reserves stdout for JSON-RPC; no `--json`.
- macOS LaunchAgent `KeepAlive` vs `tslink uninstall`; Linux `loginctl enable-linger`; stale nodes cleanup on credentialed serve restart.

## Known pitfalls (historical themes)

- **`login --retire-other`**: only explicit path deleting the other credential slot; default dual-slot retention.
- **HTTP upload limits**: 32 MiB default, streaming body inactivity timeouts; see `docs/sharing.md` and [`internal/registry/CONTRACT.md`](../../internal/registry/CONTRACT.md).
- **`docs/cli-manifest.json` `next[]`**: empty by construction; runtime guidance in `registry.CodedError.Next`.
- **`daemon_supervision_unverified`**: `add` refuses unverified live daemon; config may remain in registry; recovery via `doctor` / `install` / retry.
- **Bootstrap ordering**: registry before install; `url_pending` vs `daemon_setup_failed`; `supervision.autostart_scope` semantics.
- **launchd fixtures**: `cmd/testdata/launchctl/` for parser regressions.
- **macOS Aqua / GUI domain**: active wording in `AGENTS.md` and RUNBOOK; must not conflate with SSH/headless phrasing forbidden by `TestDocumentationDoesNotConflateSSHWithLaunchdGUIDomain`.

## Clock seam and SSE (moved to active contract)

`serverNowFn` capture rules, 2030 injection, and `TestMCPEventsHandlerCapturesTheClockAtConstruction` are authoritative in [`internal/server/CONTRACT.md`](../../internal/server/CONTRACT.md). SSE named-event contract and MCP logs redaction boundaries likewise.

## Licensing boundary

`LICENSE` is Apache 2.0 unmodified; no mandatory commercial gates in docs; `COMMERCIAL.md` is voluntary. Active summary in `AGENTS.md`.
