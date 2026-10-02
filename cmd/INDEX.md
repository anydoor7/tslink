# Health CLI entry points

- `health.go`: health formatting, configuration flags and durable alert journal reads.
- `status.go`: runtime observations and durable event projection; see repository `AGENTS.md` for the other CLI commands.
- `health_test.go`: CLI, MCP, event stream and doctor health projections.
- `regression_stale_alerts_test.go`: newer durable events and journal errors survive stale runtime snapshots.
- `regression_doctor_journal_test.go`: doctor journal authority and monitor-warning projections.
