# internal/registry — services, access, and errors

## Human access (`grantAccess`)

- Human access adapters share `grantAccess` with **locked registry mutation**, then exact runtime and durable invitation completion.
- Do not restore: allow-only success, `--invite` gating, blind unknown-POST retries, member-only permanent grants.
- Device guests may use **`never`** with acknowledgment.
- Browser/Funnel exposure and scoped binding limits remain **finite**.

## HTTP `request_limits`

- Defaults (overridable per service and via add/share flags): 32 MiB upload, 10s header, 30s upload read inactivity, 60s keep-alive idle; `unlimited` needs acknowledgement.
- Uploads stream without total-duration timeout; inactivity timeout excludes backend backpressure.
- Does not cap response streams or downloads (`docs/sharing.md`).

## CLI manifest vs runtime errors

- `docs/cli-manifest.json` field **`next[]`** is **always empty** (`tools/gen-manifest` does not populate it).
- Per-error follow-up guidance lives at runtime in **`registry.CodedError.Next`** (`registry.go`), not in the generated manifest.

## Registry discipline

- `registry.json` is source of truth; mutations via CLI/MCP only.
- Public/funnel mutations respect acknowledgement flags and scoped limits from registry layer.
