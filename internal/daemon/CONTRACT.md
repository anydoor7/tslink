# internal/daemon — supervision and process identity

## Bootstrap ordering

- Registry is written before install/setup attempts; empty registry is valid at startup.
- MCP/CLI `add` and `template_apply` judge setup on a supervisor owning a **verified, stable** process.
- Missing first Tailscale business artifact leaves `add` successful with **`url_pending`**; install/supervision failures → `daemon_setup_failed`; losing the verified process during evidence wait also → `daemon_setup_failed`.

## `daemon_identity_unverified`

- Treat as **uncertain liveness**: keep backend probes; inspect PID, binary, supervisor before install/restart.
- A PID file naming a **live process that is provably a different program** is **stopped** (not uncertain); `doctor` and install conflict guard agree.

## Platform supervision

- **launchd / Windows Task Scheduler / Windows Startup**: `supervision.autostart_scope` = **login**.
- **systemd user unit**: **boot** only when lingering enabled; lingering is **reported**, never auto-enabled.
- Windows Task Scheduler: session supervisor, exponential daemon backoff 1–60s (breaker after 8 unstable runs); Startup fallback has **no crash restart**.
- Linux setup failure may leave unit installed and retrying; inspect definition and logs before retry.

## `share` vs `add` on setup failure

- `add` / `template apply` / MCP `add`: retain registry on `daemon_supervision_unverified` and `daemon_setup_failed`.
- **`share`**: requests setup only when stopped; **rolls back** a newly created registration on failure.

## Testing fixtures

- launchd XML/state parser regressions require **`cmd/testdata/launchctl/`** fixtures (see README there).
