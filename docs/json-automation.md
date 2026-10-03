# Json Automation

## JSON Automation

Every command except the stdio `tslink mcp` server accepts `--json` and writes one versioned envelope to stdout, so owner-side automation is the same CLI with one flag. There is no REST server, dashboard, or member-facing service directory; JSON views are redacted rather than raw registry records.

```bash
# List services registered on this machine
tslink list --json

# Add a service
tslink add myapp --proxy localhost:3000 --json

# Remove a service
tslink remove myapp --json

# Check status
tslink status --json

# Show owner-only endpoint/exposure overview
tslink status --urls --json

# Run diagnostics; may record credential metadata (non-loopback probes need --probe-external)
tslink doctor --json

# Explain one service's local access model
tslink access explain myapp --json

# Preview/apply built-in templates
tslink template list --json
tslink template apply local-web --dry-run --json
tslink template apply local-web --yes --json
```

The same operations are available to MCP clients through `tslink mcp` (stdio) and the remote control plane described below; `tslink manifest --json` prints the machine-readable description of every command, flag, exit code, and error code.

All `--json` output uses the same versioned envelope. `command` names the command that produced it. Public view `data.schema_version` is the integer `1`:

```json
{
  "type": "tslink.result",
  "ok": true,
  "schema_version": 1,
  "command": "list",
  "code": 0,
  "data": {
    "schema_version": 1,
    "services": [],
    "count": 0
  }
}
```

Failures include a stable machine error code plus human text, and `error.next` lists recovery commands when one applies:

```json
{
  "type": "tslink.result",
  "ok": false,
  "schema_version": 1,
  "command": "list",
  "code": 2,
  "error": {
    "code": "usage_error",
    "message": "--tailnet conflicts with --verbose; --verbose filters this machine's registered services, while --tailnet reports tailnet devices",
    "next": ["tslink --help"]
  }
}
```

On macOS, `launchctl_domain_unavailable` is the deliberate exit-1 refusal used when TSLink cannot prove an install or uninstall handoff is safe. Its failure `data` includes `unavailable_domain`, `force_available`, the exact `force_command`, and `force_risk`; agents do not need to parse `error.message` to discover the recovery contract.

`status` sets `authenticated` and `auth_status: "authenticated"` only after a
service node is authorized. `credential_stored` separately reports a stored
credential. `daemon_state` is `running`, `absent`, or `unknown`; a missing PID
file gives `absent`. MCP `status` and its event stream use the same state field.
`doctor --json` puts its diagnostic exit code (0, 64, or 65) in the envelope
`code`. `enrollment_required` exits 3.

MCP `logs` `since`, MCP `url` `wait`, `login --expires-in`, and
`mcp.events_keepalive` accept Go duration syntax plus `d` for days, with their
own bounds. MCP `funnel_ttl` and CLI `--funnel-ttl` accept only `1h`, `8h`,
`24h`, `72h`, `7d`, or `never`; `168h` is refused. CLI `add --wait`,
`share --wait`, and `url --wait` accept Go duration syntax plus fractional
or composite days using `d`.

`--json` changes only the output format. `tslink add --json` follows the same safety guardrails as the human path: Funnel services require `--public`; TCP services reject `--allow` because TSLink does not apply HTTP identity checks to raw TCP streams.

Every successful `share --json` result includes a nonempty `data.name`: the
actually registered service name, including a collision suffix or a reused
service's name. This also applies to pending `status: "needs_login"` results;
their `auth_url` is for human enrollment, not recipient access. MCP `share`
includes the same `name` in both structured content and JSON text, and its
output schema requires it. Use the returned name for `url`, `status --urls
--name`, and `remove` (MCP `unshare`). Never guess or fall back to the requested
name. If it is missing, stop and check the installed version and contract.
See [Agent quickstart](agent-quickstart.md) for the complete handoff and undo flow.
