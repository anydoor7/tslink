# Daemon Lifecycle

## From nothing to a URL

Create a demo page and share it without an API token:

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

TSLink registers the directory and starts the daemon if it is not already running.
On first use, it prints a Tailscale authorization URL. Open it to approve the node,
then run `tslink url demo --wait` if the service URL is still pending. Open the
service URL from another device on your tailnet.

No API token. No OAuth client. No admin console visit. The node is enrolled as
you, so it needs no ACL policy of its own.

If you would rather register services explicitly and keep them around:

The proxy example requires a local app already listening on port 3000.

```bash
# 1. Expose a local web service
tslink add myapp --proxy localhost:3000

# On first use, TSLink installs its background service and prints the exact URL.
# If Tailscale enrollment is needed, open the printed authorization URL once.

# Reachable from tailnet devices your tailnet policy permits: https://myapp.<your-tailnet>.ts.net
```

`add`, `share`, and `template apply --yes` automatically install and start TSLink's
background service when it is absent. Automatic installation needs no TTY. On
Linux it requires a login session with a working systemd user manager and
`XDG_RUNTIME_DIR`; in CI or without that user bus, use `--no-daemon-install`
and run `tslink serve` manually. Installation
announces the manager, file location, config directory, and `tslink uninstall` undo
command on stderr; `--json` stdout remains a single result. Use `--no-daemon-install`
on these commands to opt out. Offline `add` saves configuration and reports
`daemon_running:false` plus repair guidance, without a green check. `share` with
that flag requires an already running service. `add` waits up to 30 seconds for an
exact URL or an enrollment URL; use `--wait=0` for registration without waiting.

The MCP `add`, `share`, and `template_apply` tools use the same bootstrap policy
and expose `no_daemon_install`. MCP `add` returns current URL/enrollment evidence
after setup without an additional URL wait; poll `url` if it is still pending.
These tools report a completed background-service install as
`daemon_installed` (`manager`, `path`, `undo`) in their results. If a later
step fails, the MCP failure `data` still carries that installation receipt.
`add` and template application save the registry before installing, retaining it
if setup fails. Explicit `install` also works before any registry exists.
Setup errors report whether a supervisor definition remains: Linux can leave an
enabled unit retrying after a readiness failure; macOS new-install verification
rolls back its job/plist when cleanup succeeds. Inspect `tslink logs` and
`tslink doctor` before retrying (Linux also: `journalctl --user -u tslink.service`).
Newly installed or reinstalled Linux and Windows service definitions write the
log file read by `tslink logs`; after upgrading either platform, run
`tslink install` again to update an existing definition. The installer checks
stable manager state. Bootstrap then judges setup on that alone:
the supervisor owns a stable process whose identity it verified. It also looks for
fresh daemon business evidence, but that evidence is produced only after the daemon
reaches the Tailscale coordination server, so its absence leaves setup successful and
the enrollment URL is resolved by the wait `add` already performs. Losing the verified
process is still a setup failure. Neither check guarantees future uptime.

`status` and `doctor` report `supervision` in text and JSON: verified manager,
autostart, `autostart_scope`, restart policy, and evidence. An unverified running
process is `manual`; an absent process without verified management is `none`.
`autostart_scope` answers what `autostart` alone cannot for a per-user supervisor:
`boot` returns with the machine while nobody is logged in, `login` waits for this
user to sign in, and `unknown` means the difference could not be determined. A macOS
LaunchAgent, Windows scheduled task and Windows Startup entry are always `login`. A systemd user unit is
`boot` only with lingering enabled; without it, `status` reports `login` and names
`loginctl enable-linger "$USER"`. TSLink reports lingering and never changes it,
because it applies to every service the user owns. Doctor treats registered
services without supervision as an error and defers backend probes while TSLink is
confirmed stopped. When PID identity or supervisor state is uncertain, doctor reports
a warning and keeps backend probes enabled; inspect the running binary and logs
before installing or restarting. `url` returns an enrollment action for any pending
node in this daemon instead of waiting again. `url` points to `tslink install` when the service is stopped.

With no registered services and no enabled MCP node, human `status` points to
`tslink add` or `tslink share`; installing an empty daemon creates no service
node to enroll.

On macOS this installs a LaunchAgent that starts at user login; on Linux it enables
a systemd user unit. For Linux boot before login and survival after logout, run
`loginctl enable-linger "$USER"` once. Windows Task Scheduler starts immediately and at later sign-ins, with a
Windows-only built-in supervisor, but requires the user to remain signed in.
It restarts daemon crashes with bounded exponential backoff; `--startup` is a
fallback without crash restart or live PID ownership
proof. The backend application must also start after reboot, and first-time Tailscale
enrollment still requires authorization. Each installed definition binds the absolute
`TSLINK_CONFIG_DIR`; automatic setup refuses to overwrite another config's manager.
Homebrew does not install a second service manager.

## Windows supervision and migration

The default Windows manager is `windows-task-scheduler`; `supervision` reports
`autostart_scope:login` and `restart_on_exit:true` only after checking the loaded
user, config, action, enabled state, restart policy and live built-in supervisor.
For a running daemon it also checks its own PID identity and executable, its
ancestry to that supervisor, and the supervisor's ancestry to the task engine.
An unknown or mismatched state stays `manual`/`none` with diagnostic detail.
The scheduled task is a logon launcher. In a Windows VM, neither killing the
direct daemon nor an independent exit-1 control caused Task Scheduler to relaunch
it. `RestartOnFailure` remains configured at 60 seconds / 255 attempts as a
launcher backstop; recovery after a supervisor crash is not proven by that setting.

The hidden Windows supervisor starts a foreground `serve` child with the same
environment and config directory. The daemon continues to publish its own PID.
A config-directory lock prevents duplicate supervisors; a Windows Job Object
contains each child at process creation and terminates its process tree if the
supervisor disappears, including before startup completes. This requires Windows
10 or newer. Unexpected daemon
exits (including zero) retry after 1, 2, 4, 8, 16, 32, then at most 60 seconds.
A run lasting at least five minutes resets the failure counter. Eight consecutive
unstable runs open the breaker and stop recovery with a successful supervisor
exit, so task retries cannot undo it. The breaker persists across sign-in; inspect
logs, then run `tslink install` to reset it. `status` and `doctor` expose
`supervisor_pid`, `runtime_state` (`starting`, `running`, `restarting`, `stopped`,
`circuit_open`, or `failed`) and `failure_reason`. A missing daemon during backoff
is distinct from a missing supervisor; the next start time appears in the detail.

To migrate a Startup install, stop its daemon, then run `tslink install`. For an
older binary without a shutdown event, the new `stop` returns an explicit error;
stop that old daemon separately after checking its identity, then install. The
installer verifies the task before deleting `Startup\tslink.vbs`. Reinstalling
updates a scheduler-owned daemon with a graceful restart; an unrelated/manual
daemon is a conflict and is never taken over. Task registration/start/settle
failures retain `%APPDATA%\tslink-supervisor\task.xml` for inspection and retry.
Windows upgrades do not restore the previous task or a replaced executable after
failure; a stopped/disabled task may need a successful reinstall to resume.
An owned task remains repairable with `tslink install` and removable with
`tslink uninstall` when disabled or when its restart settings need repair. A
disabled task never reports healthy supervision. After a stop or deletion error,
resolve the reported cause and retry either command; ownership still requires
the same user, config and exact launcher action. Legacy direct-daemon tasks are
repairable/removable, but cannot report verified crash recovery. A missing PID file after a
clean stop is accepted; malformed or unreadable PID evidence is still refused.

`tslink stop` retains the process handle while verifying identity and signaling;
the shutdown event is bound to that instance's recorded creation time. It sets a Windows named shutdown event
whose DACL admits only the current user and SYSTEM. It first stops the supervisor,
including a pending backoff with no daemon PID. The supervisor cancels recovery
and asks its direct child to run the normal tsnet cleanup path. A manual daemon
uses the existing named event. No console window, port or admin rights are needed.
Child shutdown waits up to five seconds (plus a bounded startup-event wait);
supervisor shutdown waits up to ten seconds. Failures retain inspection evidence;
the Job Object reclaims assigned children if the supervisor exits after a failure.
`tslink uninstall` disables the task first, stops both processes, confirms no running task
instance, deletes the task, then removes the local definition. Inspection,
ownership or stop failures retain the definition; the task may be disabled.
Startup-only uninstall removes autostart without taking ownership of a process.

If Task Scheduler is unavailable, `tslink install --startup` remains a deliberate
fallback. It has no crash recovery and doctor reports `daemon_restart_unavailable`
when services are registered. See [Windows platform options](platforms.md#windows-unattended-operation).

On a clean, disposable Windows user session, run the non-interactive smoke:

```powershell
go build -o .\tslink.exe .
powershell -NoProfile -File .\scripts\windows-supervision-smoke.ps1 -Binary .\tslink.exe
```

It refuses existing TSLink supervisor files/tasks and credentials, uses an isolated
empty registry, and creates no Tailscale nodes. It checks a matching daemon runtime
artifact, kills the daemon, observes restart after the backoff, verifies that
graceful stop stays stopped, reinstalls, then uninstalls. It prints explicit
`PASS`/`FAIL` and exits 0/1; budget roughly two minutes. The Windows CI job runs
this smoke and the native unit/race tests. A normal admin-capable CI user does not
by itself prove standard-user install; run the same smoke as a standard user on a VM.

Microsoft references, accessed **2026-10-02**:
[RestartOnFailure scope](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tsch/2ff4aa5a-7bc4-449f-bbb1-27475645867f),
[Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects),
[task instance engines](https://learn.microsoft.com/en-us/windows/win32/taskschd/runningtask-enginepid),
[running instances](https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask-getinstances),
[named events and security](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createeventw),
[global object namespaces](https://learn.microsoft.com/en-us/windows/win32/termserv/kernel-object-namespaces).
