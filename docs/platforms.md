# Platforms

## Prerequisites

- [Tailscale account](https://tailscale.com) (free for personal use)
- Tailscale installed on the devices you want to access from (phone, tablet, etc.)
- Go 1.26.6+ (if building from source)

## Platform Support

| Platform | Daemon | Auto-start | Stop behavior |
|----------|--------|------------|---------------|
| macOS | `--daemon` | LaunchAgent | Graceful SIGTERM |
| Linux | `--daemon` | systemd user service | Graceful SIGTERM |
| Windows | `--daemon` | Per-user Task Scheduler (Startup fallback) | Graceful named event |

## Windows unattended operation

`tslink install` registers `TSLink-<current-user-SID>` in Task Scheduler using
`InteractiveToken`, `LeastPrivilege` and a logon trigger restricted to that user.
It starts and verifies the daemon immediately. No administrator rights or Windows
password are needed. Using the existing interactive token preserves the user's
Credential Manager logon context; credential availability still depends on the
host's policies. Keep the user signed in (locking the desktop is fine). This is
login autostart, not boot before login or operation after logout.

The task waits for foreground `serve` and propagates its exit code. A nonzero exit
retries after a fixed 60-second backoff, up to 255 attempts, the scheduler schema's
maximum. Continuous crash loops eventually stop; inspect `tslink logs` and
`tslink doctor`, fix the cause, and run `tslink install` again. The task has no
execution time limit, ignores overlapping starts, and does not require idle,
network availability, or AC power. Sleep suspends the host; TSLink does not wake it.
These settings support long sessions, and do not guarantee months of uptime.

| Option | Admin needed | Credential context / trade-off | TSLink choice |
|---|---|---|---|
| Task Scheduler with interactive token | No, for your own least privilege task | Existing user's logon session; available only while signed in; crash retry settings | Default |
| Task Scheduler with S4U or password | S4U registration may avoid admin | S4U lacks network/encrypted-file access; password mode stores a Windows password and needs batch-logon rights; cannot promise the same Credential Manager session | Not offered |
| SCM Windows service (including service wrappers) | Normally yes to create the service | Session 0 and a separate service account/logon context; suitable for machine boot only with separate credential and service support | Not implemented |
| Startup folder / HKCU Run | No | Interactive user context; login launch only, no native crash restart | `tslink install --startup` fallback |
| Custom per-user watchdog | No | Can retry indefinitely, but another process and shutdown protocol must themselves be supervised | Not added |

When Task Scheduler or built-in Windows PowerShell is unavailable or blocked by
policy, explicitly choose `tslink install --startup`. It registers the existing
VBScript launcher for the next sign-in and reports `windows-startup`, with
`restart_on_exit:false` and a doctor warning. It requires Windows Script Host.
Uninstall a scheduled task before choosing this fallback. No silent downgrade
turns a scheduler failure into a promise of crash recovery.

Microsoft documentation, accessed **2026-10-01**:

- [Task security contexts](https://learn.microsoft.com/en-us/windows/win32/taskschd/security-contexts-for-running-tasks): own-user interactive registration and least privilege without a password.
- [Task registration and logon types](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskfolder-registertask): interactive session, S4U restrictions and batch logon alternatives.
- [Credential Manager token context](https://learn.microsoft.com/en-us/windows/win32/api/wincred/nf-wincred-credreadw): credentials belong to the current token's logon session; the access conclusion above follows from selecting that existing token.
- [Restart interval](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-interval-restarttype-element), [retry count](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-restarttype-complextype), [unlimited runtime](https://learn.microsoft.com/en-us/windows/win32/taskschd/taskschedulerschema-executiontimelimit-settingstype-element).
- [SCM access rights](https://learn.microsoft.com/en-us/windows/win32/services/service-security-and-access-rights), [service session isolation](https://learn.microsoft.com/en-us/windows/win32/services/interactive-services).

Configuration and state live in `~/.config/tslink/` on macOS and Linux and in `%AppData%\tslink\` on Windows; set `TSLINK_CONFIG_DIR` to use another directory. Paths written as `~/.config/tslink/` elsewhere in this README mean that directory. On Windows, TSLink never moves an older `%USERPROFILE%\.config\tslink\`: if only that directory exists, every command stops with `legacy_config_dir_present` and prints the one `move` command to run; if both exist, it refuses to choose and names both.

`credentials.lock` in that directory serializes credential changes between TSLink processes: `tslink login`, `tslink logout` and `tslink doctor --probe-remote` create it, while `tslink serve` and the commands that report credential status create it only when they record metadata for a stored credential that has none yet, or when `serve` finds a legacy `apikey` file. With the system keychain enabled, which is the default, TSLink also takes `.tslink/credentials.lock` in the OS account's home directory (`%USERPROFILE%\.tslink\` on Windows), which follows neither `TSLINK_CONFIG_DIR` nor `$HOME`; both files are empty and stay in place. `node-identities/` holds one record per service with the tags (including the derived `tag:tslink-funnel`), ephemeral setting and control URL its node was started with, so a change to any of them, even one made while the daemon was stopped, clears that node's state and enrolls it again. On Tier 1 (no stored credential) a node advertises no tags, so there only an ephemeral or control URL change does. Once a removed service's `nodes/<name>/` state is gone, whether `tslink remove` or the daemon deleted it (`tslink remove --help` says when), the daemon deletes the service's record on its next sync. A service that is only missing from `registry.json`, without `tslink remove`, keeps its node state: a lost, replaced or mistyped registry never deletes a node identity.

The read-only MCP `list`, `status`, `url`, and `doctor` tools describe missing
credential metadata without recording it or creating `credential-meta.json`
or `credentials.lock`. CLI `status` and `doctor` record it when needed, and a
backfill is written only while the credential store still holds the value read.
While a newly registered `share` waits for its URL,
`registry.json.tentative-<name>` marks a registration it may roll back. An
identical `share` or unchanged `add` settles it; the mark is removed when the
share finishes or the registration is reused.

macOS LaunchAgent installs use launchd `KeepAlive` with `ThrottleInterval=30`. If `tslink stop` is run while the LaunchAgent remains installed, launchd will restart TSLink. Run `tslink uninstall` before `tslink stop` when the intent is to disable autostart. When no desktop session exists for the user, `tslink install` first tries `gui/$(id -u)` and falls back to `user/$(id -u)` if the GUI launchd domain is unavailable. Linux headless user services may need `loginctl enable-linger "$USER"` to keep running after logout; if lingering was enabled only for TSLink, run `loginctl disable-linger "$USER"` after uninstall.

Re-running `tslink install` is the supported upgrade path on every platform. On macOS, TSLink saves an existing plist before the launchd handoff and only treats a running daemon as launchd-owned when its pidfile PID matches `launchctl print`. If post-bootstrap verification fails during an upgrade, TSLink restores the previous plist and reloads a previously identified launchd-owned job; it cannot restore an executable binary that was replaced before the command ran. If the same verification fails during a first install, TSLink boots out the new job and removes the new plist only after bootout succeeds. If cleanup cannot finish, the plist is kept so `tslink uninstall` can retry. When an upgrade cannot check a launchd domain, it preserves the previous plist and refuses by default; retry from a desktop session, or run `tslink install --force` only after confirming no job remains in the unavailable domain, because the override may start a second daemon. Uninstall removes the plist only when launchctl reports no real error and every domain is either successfully unloaded or confirms the job already absent. If any domain is unavailable, or if a real launchctl error occurs, uninstall keeps the plist and exits non-zero. `tslink uninstall --force` is the explicit recovery path for domain unavailability: it removes the plist after all addressable domains are unloaded or absent, but a job may remain running in an unavailable domain. When the domain is addressable again, run `launchctl print gui/<uid>/com.tslink.daemon` (or the corresponding `user/<uid>` target) to check it; if the job is loaded, run `launchctl bootout gui/<uid>/com.tslink.daemon` (or the corresponding `user/<uid>` target) to remove it. Real launchctl errors remain fatal with `--force`.

On Linux, TSLink likewise saves an existing systemd user unit before replacing it. If `daemon-reload`, `enable`, `restart`, or post-restart verification fails, TSLink stops the failed service, atomically restores the previous unit, reloads systemd, and restarts a service that was previously confirmed systemd-owned. Neither platform can restore an executable binary that was replaced before `tslink install` ran. Fix the reported cause and re-run `tslink install`.
