# MCP mutation journal

- `journal.go`: bounded durable journal, serialized writers, atomic replacement and small F4 adapter.
- `journal_test.go`: file failures, concurrency, rotation and restart persistence.
- `journal_unix_test.go`: real FIFO and lock contention probes.

- `testmain_test.go`: shared isolated test environment.
