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

# Unified lifetime additions

- `extend.go`: JSON-only per-app person/Funnel deadline changes and synchronous post-save event hook.
- `mcp_durations.go`: lifetime suggestions and extend tool registry/schema.
- `durations_test.go`: CLI/MCP, policy configuration and captured clock evidence.

- `duration_public_default_test.go`: compiled CLI private-to-public default and legacy controls.
- `duration_guest_atomic_test.go`: deterministic first-invitation interleaving through hermetic REST.
- `duration_entrypaths_test.go`: compiled dry-run, MCP add and share/recipe/extend posture controls.
# Home portal commands

- `portal.go`: enable/disable CLI and shared MCP actions; exact status view.
- `mcp_portal.go`: tool schemas, annotations and registry entries.
- `portal_test.go`: CLI/MCP, status, doctor and people-guide contract tests.

- `f5_review_test.go`: independent enrollment regressions, production readiness/handoff cleanup, portal-only status and real-file failure cases.
- `auth_handoff_read_unix.go`, `auth_handoff_read_windows.go`: bounded regular-file handoff reads; Unix nonblocking/no-follow open.
- `portal_handoff_unix_test.go`: real pending-offer cancellation after FIFO/symlink replacement.
- `pending_enrollment_test.go`: reviewer-derived HTTP polling and multi-node pending/terminal controls (`enrollmenttest` tag).
- `auth_handoff_multi_test.go`: legacy migration, per-node replacement, concurrent files, exact retirement, write failures and complete status projections.

# Access requests and phone onboarding

- `people_qr.go`: phone guides, safe terminal/PNG QR output and explicit bearer consent.
- `people_qr_test.go, people_qr_unix_test.go`: independent decoding, command and file failure evidence.
- `requests.go`: owner CLI/actions, terminal filtering and F4 post-decision hook.
- `requests_test.go`: compiled CLI, MCP listener owner checks and durable decisions.
- `mcp_requests.go`: owner-only request tool schemas and annotations.

- `portal_authority_test.go`: remote authority denial, local recovery and retryable request-inbox controls.
