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

# Application recipe additions

This index records the app recipe additions; the CLI manifest describes the complete command tree.

- `apps.go`: `apps list/detect/share`, recipe plans, safe registry writes and the `add --recipe` adapter.
- `apps_mcp.go`: app discovery and recipe MCP schemas, annotations and handlers.
- `apps_test.go`: recipe CLI/MCP and registry/safety contract tests.
- `apps_adoption_test.go`: tentative registration adoption, creator rollback, compare failures and read-only preview regressions.
- `add.go`, `mcp.go`, `manifest.go`: thin integration with existing command, tool and manifest registries.

- `preserve_host_test.go`: CLI/MCP overrides, Host policy conflict reuse, registry-backed status/list/access projections.

# Wave-1 integration

- `wave1_integration_test.go`: recipe health/limits overrides, people scope and reuse, combined conflict diagnostics, CLI/MCP projections and doctor canonical Host business probes.

# Windows supervision additions

- [builtin_supervisor_windows.go](builtin_supervisor_windows.go)
- [builtin_supervisor_windows_test.go](builtin_supervisor_windows_test.go)
- [serve_shutdown_other.go](serve_shutdown_other.go)
- [serve_shutdown_windows.go](serve_shutdown_windows.go)
- [service_manager_guard_windows_test.go](service_manager_guard_windows_test.go)
- [windows_scheduler_native_test.go](windows_scheduler_native_test.go)
- [windows_supervision_regression_test.go](windows_supervision_regression_test.go)
- [windows_task.go](windows_task.go)
- [windows_task_com_test.go](windows_task_com_test.go)
- [windows_task_test.go](windows_task_test.go)

- `wave1_round2_test.go`: scheduler installation schemas with recipe, Host and request-limit contracts.

- `json_test.go`: concurrently drains captured CLI output; `wave1_round2_test.go` covers output larger than the OS pipe buffer.

# Scoped MCP permissions

- `mcp_scopes.go`: shared tool authorization, output filtering, reduced stdio launch and owner audit CLI.
- `mcp_scopes_test.go`: complete role/tool/app matrix, real HTTP and stdio sessions, audit and expiry probes.

- `mcp_scopes_boundary_test.go`: MCP transaction, identity and app attribution correctness tests.

- `mcp_install_darwin_test.go`, `mcp_install_linux_test.go`, `mcp_install_windows_test.go`: real MCP/bootstrap/definition paths with fake manager boundaries and active/expired/cancelled controls.
- `mcp_manager_context_test.go`: real bounded subprocess cancellation and expiry probes.
- `service_manager_context_test.go`: context seam adapter retaining the service-manager guard restoration check.
- `supervision_unix.go`: checked Unix manager query adapter and caller-context command adapter.
- `bootstrap_queries_unix_test.go`, `bootstrap_queries_windows_test.go`: caller session checks at query boundaries with active, expired and cancelled controls.
- `bootstrap_query_process_test.go`: cancellation after a real query subprocess reports readiness.
- `bootstrap_inspection_test.go`: existing definitions, installer inspection and supervision context propagation.
- `bootstrap_install_context_darwin_test.go`, `bootstrap_install_context_linux_test.go`: isolated prior-definition inspection adapters.

- `read_manager_context_test.go`: ported shared-read session, subprocess cancellation and setup error-code fixtures; bounded compensation provenance; additional list, URL, share and event snapshot callers.
- `manager_inventory_test.go`: all-platform AST inventory and transitive MCP manager context checks, with explicit compensation and CLI classifications.
