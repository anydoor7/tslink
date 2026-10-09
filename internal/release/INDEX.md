# Release validation

- `publish_test.go`: stable credentials, GitHub App token, pre-release policy and cask contracts.
- `windows_publish_test.go`: Scoop/winget configuration, credential wiring and read-only publisher preflight controls.
- `workflows_test.go`: pinned actions, CI graph and release prerequisites.
- `native_gate_test.go`: native platform gate coverage.
- `release_notes_test.go`: curated release notes extraction.
- `release_verify_script_test.go`: artifact verification script behavior.
- `testmain_test.go`: test environment isolation.
