# Health package

- `probe.go`, `file.go`, `file_unix.go`, `file_windows.go`, `file_other.go`: safe backend checks and file replacement handling.
- `expiry.go`: expiry observations and warning thresholds.
- `alerts.go`, `delivery.go`, `command_unix.go`, `command_other.go`: durable events, bounded delivery, and platform command handling.
- `health_test.go`, `safety_test.go`, `delivery_test.go`: business probes, delivery and safety tests.
- `regression_notifier_test.go`, `regression_file_timeout_test.go`: reviewer process-tree and FIFO regressions (Unix).
- `regression_junction_windows_test.go`: native Windows directory-junction compatibility fixture.
- `persistence_test.go`: batched delivery persistence and unchanged-state write counts.
- `wave1_integration_test.go`: bodyless real HTTP probes under finite/unlimited request limits and invalid-limit refusal before backend I/O.

- `current.go`, `current_test.go`: shared freshness projection for status and portal; exact stale boundary checks.

# Access requests and phone onboarding

- `alerts.go`: typed access-request notification and bounded notifier config reads.
- `alerts_file_unix_test.go`: real FIFO, symlink, directory, oversized and partial notifier configuration probes.

- Alert state reads use the shared state-file read/retry/settle helpers.
