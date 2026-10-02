# Local access history

- `event.go`: typed event/writer schema, validated options, identity/address/path sanitization.
- `store.go`: bounded asynchronous queue, daily rotating JSONL, writer lock, crash recovery, eviction and health snapshots.
- `query.go`: read-only filters and all-match summaries.
- `sync_unix.go`, `sync_windows.go`: platform directory-sync boundary.
- `store_test.go`, `failure_test.go`, `special_unix_test.go`, `testmain_test.go`: privacy, filters, clock, retention/cap, queue/lifecycle, process crash and filesystem failures.
