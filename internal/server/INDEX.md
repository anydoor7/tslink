# People sharing files

This index records the people sharing lifecycle implementation and regression tests in this directory. Other package files are described by the repository architecture guide.

- [people.go](people.go)
- [people_review_regression_test.go](people_review_regression_test.go)
- [rereview_identity_test.go](rereview_identity_test.go): ported independent re-review fixtures.
- [review3_utf8_test.go](review3_utf8_test.go): complete-list or UTF-8 boundary regression evidence.

# Server health entry points

- `server.go`: node lifecycle and runtime snapshot publication; see repository `AGENTS.md` for the other server modules.
- `health.go`: backend scheduling, independent node-expiry refresh and monitor shutdown.
- `health_test.go`, `health_isolation_test.go`: monitor, event stream, worker isolation and notifier progress tests.
- `regression_refused_target_test.go`, `regression_node_replacement_test.go`: reviewer target-safety and node-incarnation regressions.
- `regression_scheduler_test.go`, `regression_refresh_cost_test.go`, `regression_pool_test.go`, `health_pool_test.go`: scheduler budgets, retained reads, saturation/recovery and write-count regressions.
- `regression_snapshot_retry_test.go`, `regression_stream_aging_test.go`: failed snapshot retry and connected event-stream health aging regressions.

# Recipe proxy configuration checks

> **定位**: Recipe integration tests against the real reverse proxy.
> **下一跳**: `recipe_proxy_test.go` and `proxy_preserve_host_test.go`.
> **边界**: Server architecture is described in the repository `AGENTS.md`.

- `recipe_proxy_test.go`: real `NewProxyHandler` headers and Host/Origin-sensitive catalog settings; optional receipts for disposable upstream consumer validation.

This index covers the recipe integration tests. The repository AGENTS.md describes the server architecture.
- `proxy_preserve_host_test.go`: real HTTP/TLS Host forwarding, regenerated forwarded headers and hot-reload policy changes.

- `host_authority_test.go`: reviewer-derived real TLS, HTTP/1 absolute-form and HTTP/2 cross-node virtual-host regressions.
- `canonical_host_test.go`: trusted runtime name selection, normalization, missing-name refusal and recovery.

- `wave1_integration_test.go`: real proxy and health Host agreement, rename/refusal/recovery, snapshot health, request limits and immediate people revocation.

# Registry watcher reconciliation

- `registry_watcher_test.go`: periodic and overflow reconciliation with real registry files.
- `registry_watcher_active_removal_test.go`: active removal while another node enrolls.
- `registry_watcher_long_sync_test.go`: cancellation and callback joins during blocked startup.
- `registry_watcher_decision_test.go`: generation ordering and lossless pending rechecks.

- `wave1_round2_test.go`: lost-event recovery across health, Host, limits and people configuration.

- Watcher fixtures stop and join their workers, then close file nodes before temporary directory cleanup on Windows.

- `duration_guest_policy_test.go`: first guest grant enforced by real HTTP proxy before invite history; restart/rollback latch.
# Home portal

- `app_access.go`: shared private app/portal authorization decision; F1 deadlines and tombstones, legacy rules and explicit owner/admin identities.
- `portal.go`: read-only HTML/JSON, trusted Host/Origin and CSP/cache headers; reserved access-request route.
- `portal_node.go`: independent tsnet lifecycle, bounded startup, retry and runtime observations.
- `portal_test.go`, `portal_node_test.go`: real HTTP/TLS listener, identity, expiry, isolation, headers and failure-path tests.
- `testdata/INDEX.md`: rendered HTML golden snapshots.

- `f5_review_test.go`: ported reviewer lifecycle/auth-error/TCP probes and real-handler browser artifacts.
- `portal_access_model_test.go`: service-type enforcement/disclosure, tagged/admin/tombstone and cancelled-generation checks.
- `enrollment_testbridge.go`: build-tagged bridge to the production enrollment loop; excluded from normal builds.

# Access requests and phone onboarding

- `portal_requests.go`: WhoIs member gate, stateless CSRF, POST, F2 notification and F4 hook.
- `portal_requests_test.go`: real listener, command, webhook, SSE, partial body and lifecycle evidence.
