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

# Concurrent registry file access

- `file_io.go`: bounded sharing-error retries for registry reads and replacements; shared-delete readers, POSIX-semantics replacement and the mid-replace not-exist settle come from `internal/atomicfile` (`OpenSharedRead`, `ReplaceFile`, `ReadSettled`). `ReadFile` is the exported read for callers outside this package.
- `file_io_test.go`, `file_io_windows_test.go`: injected classifier/policy tests and native held-handle, snapshot and failure-cleanup regressions.
- `concurrent_replace_test.go`: all registry loaders against a concurrent real registry writer, with error counts; a successful load without the saved service (false empty or `missing`) fails.
