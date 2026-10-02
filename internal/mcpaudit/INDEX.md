# MCP mutation journal

- `journal.go`: bounded durable journal, serialized writers, atomic replacement and small F4 adapter.
- `journal_test.go`: file failures, concurrency, rotation and restart persistence.
- `journal_unix_test.go`: real FIFO and lock contention probes.
- `journal_parent_test.go`: absent directories and regular-file ancestor read/write boundaries.

- `testmain_test.go`: shared isolated test environment.
