# People sharing files

This index records the people sharing lifecycle implementation and regression tests in this directory. Other package files are described by the repository architecture guide.

- [people.go](people.go)
- [people_invites.go](people_invites.go)
- [mcp_people.go](mcp_people.go)
- [people_identity_test.go](people_identity_test.go)
- [people_invites_test.go](people_invites_test.go)
- [people_review_regression_test.go](people_review_regression_test.go)
- [rereview_adversarial_test.go](rereview_adversarial_test.go): ported independent re-review fixtures.
- [review3_boundary_test.go](review3_boundary_test.go): complete-list or UTF-8 boundary regression evidence.
- [review3_input_test.go](review3_input_test.go): complete-list or UTF-8 boundary regression evidence.

# Health CLI entry points

- `health.go`: health formatting, configuration flags and durable alert journal reads.
- `status.go`: runtime observations and durable event projection; see repository `AGENTS.md` for the other CLI commands.
- `health_test.go`: CLI, MCP, event stream and doctor health projections.
- `regression_stale_alerts_test.go`: newer durable events and journal errors survive stale runtime snapshots.
- `regression_doctor_journal_test.go`: doctor journal authority and monitor-warning projections.
