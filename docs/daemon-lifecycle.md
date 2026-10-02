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
60-second crash retry delay (up to 255 attempts), but requires the user to remain
signed in. `--startup` is a fallback without crash restart or live PID ownership
proof. The backend application must also start after reboot, and first-time Tailscale
enrollment still requires authorization. Each installed definition binds the absolute
`TSLINK_CONFIG_DIR`; automatic setup refuses to overwrite another config's manager.
Homebrew does not install a second service manager.

## Windows supervision and migration

The default Windows manager is `windows-task-scheduler`; `supervision` reports
`autostart_scope:login` and `restart_on_exit:true` only after checking the loaded
user, config, action, enabled state and restart policy. For a running daemon it
also checks its executable and process ancestry against the task's engine PIDs.
An unknown or mismatched state stays `manual`/`none` with diagnostic detail.
This is failure restart: a successful graceful stop is intentionally left stopped.

To migrate a Startup install, stop its daemon, then run `tslink install`. For an
older binary without a shutdown event, the new `stop` returns an explicit error;
stop that old daemon separately after checking its identity, then install. The
installer verifies the task before deleting `Startup\tslink.vbs`. Reinstalling
updates a scheduler-owned daemon with a graceful restart; an unrelated/manual
daemon is a conflict and is never taken over. Task registration/start/settle
failures retain `%APPDATA%\tslink-supervisor\task.xml` for inspection and retry.
Windows upgrades do not restore the previous task or a replaced executable after
failure; a stopped/disabled task may need a successful reinstall to resume.

`tslink stop` verifies process identity and sets a Windows named shutdown event
whose DACL admits only the current user and SYSTEM. The daemon cancels the same
context used by the normal tsnet cleanup path. No console window, port, admin
rights or force termination are used. It waits up to five seconds; a timeout or
missing listener is an error and retains PID evidence. `tslink uninstall` disables
the task first, gracefully stops its verified daemon, confirms no running task
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
`PASS`/`FAIL` and exits 0/1; budget roughly three minutes. The Windows CI job runs
this smoke and the native unit/race tests. A normal admin-capable CI user does not
by itself prove standard-user install; run the same smoke as a standard user on a VM.

Microsoft references, accessed **2026-10-01**:
[task instance engines](https://learn.microsoft.com/en-us/windows/win32/taskschd/runningtask-enginepid),
[running instances](https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask-getinstances),
[named events and security](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createeventw),
[global object namespaces](https://learn.microsoft.com/en-us/windows/win32/termserv/kernel-object-namespaces).
