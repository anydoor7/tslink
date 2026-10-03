
# Windows supervision and shutdown

- [job_process_windows.go](job_process_windows.go)
- [shutdown_windows.go](shutdown_windows.go)
- [shutdown_windows_test.go](shutdown_windows_test.go)
- [supervisor.go](supervisor.go)
- [supervisor_job_windows_test.go](supervisor_job_windows_test.go)
- [supervisor_lifetime_windows_test.go](supervisor_lifetime_windows_test.go)
- [supervisor_test.go](supervisor_test.go)
- [supervisor_windows.go](supervisor_windows.go)
- [task_process_windows.go](task_process_windows.go)
- [task_process_windows_test.go](task_process_windows_test.go)
- [helper_signal_unix_test.go](helper_signal_unix_test.go): copied helper ignores non-termination signals and exits on SIGTERM.
- `daemon_test.go`, `helper_signal_unix_test.go`: copied daemon fixtures handle shutdown signals without treating Go's SIGURG preemption as an exit request.
- `shutdown_windows_test.go`: missing-listener fixtures validate stale PID timestamps, then publish the actual process-start timestamp independently of test-binary age.
