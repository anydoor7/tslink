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
