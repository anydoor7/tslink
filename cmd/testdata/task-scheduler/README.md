# Task Scheduler fixtures

Synthetic Task Scheduler 2.0 XML and JSON projections from the documented COM
properties `RegisteredTask.Xml`, `Enabled`, `State`, `LastTaskResult`, and
`GetInstances(0)` / `RunningTask.EnginePID`. These are independent text fixtures,
not captures from a Windows machine. All usernames, SIDs, paths and PIDs are fake.

`task.xml` specifies an interactive, least privilege logon task with no execution
deadline, no battery/idle/network restriction, one instance, and 255 retries at
one minute intervals. Its encoded launcher was independently encoded as UTF-16LE
with Python. It waits for foreground `serve` and returns the daemon exit code.

`running.json`, `ready.json`, `disabled.json`, and `absent.json` represent the
locale-independent COM projection the transport emits. The parser must reject
missing fields, localized error text, malformed XML, and invalid engine PIDs.
Task engines are not assumed to be daemon PIDs; Windows tests check ownership
separately. `windows_task_test.go` runs the XML/status fixtures on all CI hosts.

Microsoft sources (accessed 2026-10-01):

- https://learn.microsoft.com/en-us/windows/win32/taskschd/task-scheduler-schema
- https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask
- https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask-getinstances
- https://learn.microsoft.com/en-us/windows/win32/taskschd/runningtask-enginepid

Use `scripts/windows-supervision-smoke.ps1` on a clean Windows user session to
validate the real XML consumer, COM shape, crash restart, and graceful stop.
Fixture success alone does not establish native Task Scheduler compatibility.
