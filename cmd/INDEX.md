# Application recipe additions

This index records the app recipe additions; the CLI manifest describes the complete command tree.

- `apps.go`: `apps list/detect/share`, recipe plans, safe registry writes and the `add --recipe` adapter.
- `apps_mcp.go`: app discovery and recipe MCP schemas, annotations and handlers.
- `apps_test.go`: recipe CLI/MCP and registry/safety contract tests.
- `apps_adoption_test.go`: tentative registration adoption, creator rollback, compare failures and read-only preview regressions.
- `add.go`, `mcp.go`, `manifest.go`: thin integration with existing command, tool and manifest registries.

- `preserve_host_test.go`: CLI/MCP overrides, Host policy conflict reuse, registry-backed status/list/access projections.
