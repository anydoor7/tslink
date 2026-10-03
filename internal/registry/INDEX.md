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

# Browser guest links

- guests.go: durable browser grants, salted token/PIN hashes, policy, counters and revocation.
- guests_test.go: persistence, policy, gate migration, lockout and invalid ledger controls.

- `guest_usage.go`: shared authorization reads, batched usage and committed-grant notifications.
- `guest_crash_test.go`: atomic revoke persistence and restart with killed writers.

- `guest_usage_test.go`: batches, retry, revocation and opportunistic counter persistence.

- `guest_monitor.go`: one shared grant snapshot per gate tick and batched expiry latching.
- `guest_monitor_test.go`: snapshot, expiry rollback, other-grant and unreadable-registry controls.
- `guest_publication_test.go`: before/after publication errors and interleaved counter observations.

# Access requests and phone onboarding

- `requests.go`: bounded durable request ledger, rate/retention and atomic decisions.
- `requests_test.go, requests_unix_test.go`: concurrent writers, restart, policy, retention and special files.
- `people_app.go`: additive F1 single-app lifetime contract, preserving unrelated grants.

- `portal_authority_test.go`: current-registry authorization under writer contention.
- `requests_contention_test.go`: expiry maintenance lock failures, rollback and subprocess restart.

- `approval_authority_test.go`: locked pre-mutation authority, canonical targets and authorized inbox failure paths.

- `lifetime_scope.go`: locked session and duration authorization shared by lifetime mutations.
- `lifecycle_audit.go`: post-commit expiry receipts outside the registry lock.
# Concurrent registry file access

- `file_io.go`: bounded sharing-error retries for registry reads and replacements; shared-delete readers, POSIX-semantics replacement and the mid-replace not-exist settle come from `internal/atomicfile` (`OpenSharedRead`, `ReplaceFile`, `ReadSettled`). `ReadFile` is the exported read for callers outside this package.
- `file_io_test.go`, `file_io_windows_test.go`: injected classifier/policy tests and native held-handle, snapshot and failure-cleanup regressions.
- `concurrent_replace_test.go`: all registry loaders against a concurrent real registry writer, with error counts; a successful load without the saved service (false empty or `missing`) fails.

- Wave 2 bounded portal/guest readers use the shared open/retry/settle policy; `concurrent_replace_test.go` includes both alongside all main loaders.
