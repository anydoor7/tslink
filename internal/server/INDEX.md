# Server health entry points

- `server.go`: node lifecycle and runtime snapshot publication; see repository `AGENTS.md` for the other server modules.
- `health.go`: backend scheduling, independent node-expiry refresh and monitor shutdown.
- `health_test.go`, `health_isolation_test.go`: monitor, event stream, worker isolation and notifier progress tests.
- `regression_refused_target_test.go`, `regression_node_replacement_test.go`: reviewer target-safety and node-incarnation regressions.
- `regression_scheduler_test.go`, `regression_refresh_cost_test.go`, `regression_pool_test.go`, `health_pool_test.go`: scheduler budgets, retained reads, saturation/recovery and write-count regressions.
