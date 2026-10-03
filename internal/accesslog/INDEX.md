# Local access history

- `event.go`: typed event/writer schema, validated options, identity/address/path sanitization.
- `store.go`: bounded asynchronous queue, daily rotating JSONL, writer lock, crash recovery, eviction and health snapshots.
- `query.go`: read-only filters and all-match summaries.
- `sync_unix.go`, `sync_windows.go`: platform directory-sync boundary.
- `store_test.go`, `failure_test.go`, `special_unix_test.go`, `testmain_test.go`: privacy, filters, clock, retention/cap, queue/lifecycle, process crash and filesystem failures.

- `audit.go`: typed MCP and guest identifier/code sanitization.
- `lifecycle.go`: daemon-owned stable writer, initialization retry and missing-history windows.
- `audit_roundtrip_test.go`, `path_policy_test.go`, `lifecycle_test.go`: audit round-trips, mode/escape regression, failure/recovery and single-writer controls.

- `query_identity_test.go`: MCP nested identity and case-sensitive tag query regression.

- `query_receipt_test.go`: legacy MCP journal projection, scoped metadata and read-only history preservation.
