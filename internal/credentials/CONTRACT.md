# internal/credentials — tiers, tags, and secret handling

## Tier 1 (default)

- No stored credential: each fresh tsnet node starts with empty `AuthKey`, no tags, no remote ACL/device API until user enrolls via emitted URL.

## Tier 2 (opt-in)

- API access token (`tskey-api-*`, renewed periodically) or OAuth client secret (`tskey-client-*`, non-expiring).
- `GetAuthKey()` calls Tailscale `CreateKey()` per service start with service tags, ephemeral flag, scoped description (not process-wide union).
- OAuth: tsnet authenticates directly; REST client uses OAuth; missing policy scope degrades tag ensure, not all services.

## Storage and fallback

- Primary: system keychain (`go-keyring v0.2.8`, service `tslink`).
- macOS/Linux file fallback `~/.config/tslink/apikey|clientsecret` (0600) only after proving stale keychain entry absent/removed.
- Unreachable/uncertain keychain → explicit login failure (headless ≠ guaranteed fallback).
- **Windows: no file fallback** (`fileCredentialFallbackEnabledFunc` in `credentials.go`); Credential Manager required.
- Legacy `~/.config/tslink/authkey` used when no modern credential; `MigrateFromLegacy()` on `serve`.

## Non-interactive secret injection

- Automation: environment variables or **stdin flags**; do not rely on argv or shell history for secrets.
- `--json` login: no interactive prompts.
- **`login --retire-other`**: only explicit path deleting the other slot after verify+commit; default dual-slot retention.

## Tag system

- Default `tag:tsmain` on `add` when `--tags` omitted.
- Funnel: additive `tag:tslink-funnel` at runtime, not auto-persisted in registry.
- Grammar: `tag:<lowercase-hyphen-name>`; legacy values migrated via `tags set` or registry edit.
- ACL auto-creation when token scopes permit; hot-reload restarts on tag changes.
- Default-on Funnel ACL provisioning: **`--no-auto-provision`** kill switch on serve/login paths.

## Cross-platform lock / event security

- **macOS**: Keychain + launchd GUI (**Aqua**) vs user domain contracts.
- **Windows**: graceful `stop` via **user-restricted named event**; DACL prevents proving user-only secrets locally.
- **Linux**: atomic file locks on registry/state where applicable; systemd user linger for headless persistence.

## Licensing boundary (credentials docs only)

- Do not document mandatory commercial gates tied to credential storage.
- Preserve Apache 2.0 attribution and contributor rights in `CONTRIBUTING.md`.
