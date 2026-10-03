# Global configuration

- `config.go`: isolated configuration paths and locked strict global config persistence.
- `durations.go`: validates durations.public_max and returns the shared lifetime policy.
- `durations_test.go`: strict input, load/save refusal and preservation evidence.
- Other platform paths and existing regression tests retain their current files.
# MCP configuration additions

- `config.go`: shared configuration and strict MCP binding validation.
- `mcp_scopes_test.go`: invalid bindings, unknown fields and legacy owner round trips.

- Global configuration reads use the shared state-file read/retry/settle helpers.
