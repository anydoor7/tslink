# MCP mutation journal

- `journal.go`: bounded durable journal, serialized writers, atomic replacement and small F4 adapter.
- `journal_test.go`: file failures, 24 unique concurrent durable writes under virtual time, real Record lock-wait deadline/cancellation, rotation and restart persistence.
- `journal_unix_test.go`: real FIFO and lock contention probes.
- `journal_parent_test.go`: absent directories and regular-file ancestor read/write boundaries.

- `testmain_test.go`: shared isolated test environment.

- `lifecycle.go`: typed authority changes, per-invocation MCP collection and CLI/lifecycle receipts.

- Journal reads settle/retry before interpreting absence, and use shared-delete snapshots with the existing size bound.
