# People sharing files

This index records the people sharing lifecycle implementation and regression tests in this directory. Other package files are described by the repository architecture guide.

- [people.go](people.go)
- [people_invites.go](people_invites.go)
- [people_identity_test.go](people_identity_test.go)
- [people_invites_test.go](people_invites_test.go)
- [people_invites_unix_test.go](people_invites_unix_test.go)
- [people_review_regression_test.go](people_review_regression_test.go)
- [rereview_store_test.go](rereview_store_test.go): ported independent re-review fixtures.
- [testdata/INDEX.md](testdata/INDEX.md): parent registry upgrade provenance.
- [review3_identity_test.go](review3_identity_test.go): complete-list or UTF-8 boundary regression evidence.
- [review3_utf8_test.go](review3_utf8_test.go): complete-list or UTF-8 boundary regression evidence.

# Service registry tests

> **Purpose**: Service registry tests
> **Next**: `registry.go`
> **Boundary**: Registry admission and persistence; runtime proxy settings are in ../server/proxy.go.

- `preserve_host_test.go`: strict boolean decoding, legacy defaults, persistence and proxy-only admission.
- `proxy_host.go`: shared trusted canonical name selection for proxy, health monitor and doctor.

# Unified lifetime additions

- `durations.go`: per-app locked expiry changes, audience policy and DurationChange payload.
- `durations_test.go`: deadline/latch/regrant, malformed input, concurrent writer and save failure evidence.
- `durations_unix_test.go`: real FIFO, directory and symlink refusal through configuration/registry runtime paths.
- `people.go`: additive ChangePersonWithLifetime API; F1 trusted store API retained; optional sticky guest classification in schema 2.

- `duration_boundary_test.go`: final Funnel policy, guest persistence/re-add and invite grant CAS evidence.
# Home portal configuration

- `people.go`: `ResolvePersonLoginIn` exposes the existing exact legacy-key resolver for one already-read registry.
- `portal.go`: optional schema-2 portal settings, admission, hostname reservation and locked updates.
- `portal_read.go`, `portal_read_unix.go`, `portal_read_windows.go`: bounded read-only registry reader, special-file refusal.
- `portal_test.go`, `portal_unix_test.go`: persistence, concurrent writers, hostile configuration and FIFO/symlink tests.
# Scoped MCP registry operations

- `mcp_actions.go`: locked single-app grants, revocation and gateway restart requests.
- `mcp_actions_test.go`: unrelated app preservation, expiry, tombstones and failed writes.

- `mcp_boundary_test.go`: real lock tests for session expiry/cancellation across mutation writers and owner-only person creation.
