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

> **定位**: Service registry tests
> **下一跳**: `registry.go`
> **边界**: Registry admission and persistence; runtime proxy settings are in ../server/proxy.go.

- `preserve_host_test.go`: strict boolean decoding, legacy defaults, persistence and proxy-only admission.
- `proxy_host.go`: shared trusted canonical name selection for proxy, health monitor and doctor.

# Scoped MCP registry operations

- `mcp_actions.go`: locked single-app grants, revocation and gateway restart requests.
- `mcp_actions_test.go`: unrelated app preservation, expiry, tombstones and failed writes.
