# cmd — CLI command contracts

## Core commands

- **`add`**: registry first, then daemon supervision or install; `--no-daemon-install` config-only (no green check offline). Default tag `tag:tsmain` when `--tags` omitted. Refuses live daemon without verified supervision (`daemon_supervision_unverified`, exit 1) unless `--no-daemon-install`. Retains registry on `daemon_supervision_unverified` / `daemon_setup_failed` with human text **Configuration remains in the registry** → `tslink list`.
- **`share`**: setup only when liveness says stopped; **rolls back a newly created registration** on failure (not the same retain contract as `add`).
- **`remove`**: registry removal + remote cleanup only with ownership proof.
- **`list` / `status` / `logs`**: human or `--json`; logs supports `--last`, `--level` (verbatim; MCP `logs` redacts only MCP surface).
- **`health set`**: per-service probe configuration (see server contract).
- **`url <name> --wait`**: never construct hostname from service name alone; wait for real URL evidence.
- **`portal` / `door`**: Portal-only mode opt-in; stable routes for private HTTP/file apps.
- **`mcp`**: JSON-RPC on stdout; **rejects `--json`**. `add`/`template_apply`: no second URL wait; missing first artifact → success + `url_pending`; verified process lost during wait → `daemon_setup_failed`.

## Funnel and public exposure

- **`--funnel`** requires explicit **`--public`** on add (or `public_ack: true` in registry). Legacy Funnel entries must be re-added or edited.

## macOS launchd install domain

- GUI `gui/$(id -u)` follows the uid's **Aqua** desktop session, not caller transport.
- **First install** may fall back to `user/$(id -u)` when GUI domain is absent.
- **Upgrade** refuses if a prior domain cannot be checked → `launchctl_domain_unavailable` exit **1** with structured `error.code` and `data`: `unavailable_domain`, `force_available`, `force_command`, `force_risk` (not a generic internal error).
- Parser regressions use fixtures under **`cmd/testdata/launchctl/`** (nested coalition state, enable/disable overrides).

## JSON programmatic output

- Shared envelope: `ok`, `schema_version`, `command`, `code`, `data` / `error.{code,message}`.
- `tslink serve --json` may return `needs_login` with `auth_url` while daemon child waits.
- Exit codes: 0 success, 1 general, 2 usage, 3 auth, 4 conflict, 5 not found, 64/65 diagnostic thresholds.

## Non-interactive login (agents)

- Exactly one credential source: `--api-key-stdin` / `--client-secret-stdin` (preferred) or `--api-key` / `--client-secret` (compat, not for automation docs).
- Env `TSLINK_API_KEY` / `TSLINK_CLIENT_SECRET` when no explicit flag; `--json` disables interactive login.
- Secrets via **env or stdin, never argv** in official automation guidance.

## Tags subcommands

- `tags list|pull|add|set|set-default|delete-remote` (remote delete needs `--force --manage-acl`).

## Registration contract for AI agents

1. `go install` with `$HOME/go/bin` on PATH.
2. Optional Tier 2: pipe secrets to login stdin flags.
3. `add` with `--proxy` / `--dir`; verify with `status`, `url <name> --wait`.
4. Maintain: token renewal, upstream service liveness, daemon supervision fields, registry via CLI only.
