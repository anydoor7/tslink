# Agent quickstart

Use TSLink when the owner wants to share an app or artifact that already runs on
their computer, inspect its private address, and withdraw the share. This guide
is for operating an installed TSLink; [AGENTS.md](../AGENTS.md) covers contributing.
For installation, Go/Git requirements, MagicDNS, and HTTPS setup, follow
[Getting started](getting-started.md). Fresh service nodes require a human's
Tailscale browser enrollment; tailnet device approval may also be required.

## Connect a local MCP client

For a local client using the `mcpServers` configuration format:

```json
{
  "mcpServers": {
    "tslink": { "command": "tslink", "args": ["mcp"] }
  }
}
```

The client must resolve the installed binary; use its absolute path if its PATH
differs from your shell's. `tslink mcp` reserves stdout for MCP JSON-RPC; do not
add `--json`. Consult [MCP clients](mcp-clients.md) for client-specific setup.
Read the live tool schemas before calling tools. The CLI equivalent below makes
the share, verification, and undo steps explicit.

## Share, hand off, verify, undo

Confirm the intended target and audience with the owner. This example assumes
their HTTP app is already listening on port 3000 and should stay private:

```bash
tslink manifest
tslink share 3000 --name preview --json
```

`share` can install/start the user's background service. A requested name may
receive a numeric suffix on collision; use the actual `name` returned in `data`
(or the MCP tool result) for subsequent steps. The examples below assume it is
`preview`.

Treat `needs_login` as **pending human handoff**, even if the command or tool
result is otherwise successful. Give the returned `auth_url` to the owner to
complete in their browser; it is an enrollment URL, not the app URL. Do not claim
the app is reachable or try to bypass enrollment. After the owner finishes any
sign-in and device approval, fetch exact runtime evidence:

```bash
tslink url preview --wait --json
```

The general form is `tslink url <name> --wait`; bare `--wait` waits up to 30s.
Never construct a `.ts.net` hostname from the requested name. If the result is
pending or fails, follow the structured continuation/error rather than reporting
success. In MCP, use the `url` tool with `{"name":"preview","wait":"30s"}`.

**Verify:** inspect the selected share:

```bash
tslink status --urls --name preview --json
```

Require `data.runtime_snapshot.exact: true` and the selected service's
`endpoint.state: "exact"`; inspect its `runtime_state` and errors. A live daemon
or saved registry entry alone does not prove access.
Have the owner open the returned URL from a permitted tailnet device and check
the expected app response before declaring recipient access verified. Backend
health alone also cannot prove that recipient path.

**Undo:** remove the actual name created for this task:

```bash
tslink remove preview --json
```

MCP uses `unshare` with that name. Removal attempts remote node cleanup only when
credentials and exact ownership proof permit it; inspect the result, including
`node_state_kept_reason`, for retained state. It does not uninstall the daemon,
stop the backend app, recall downloads, or remove unrelated shares.

## Parse contracts, not display text

CLI `--json` results use a `tslink.result` envelope with `schema_version: 1`,
`ok`, `command`, and numeric `code`. On failure, read `error.code`, `error.message`,
and `error.next`; a successful envelope can still contain a pending state.
Check supported versions before consuming fields. The raw `tslink manifest`
document has its own `schema_version` (currently 2), distinct from the result
envelope and public view versions. MCP uses its tool result/JSON-RPC contract,
not the CLI envelope. See [JSON automation](json-automation.md) and
[Agents](agents.md).

## Authority and audience boundaries

Local stdio MCP runs with the launching OS user's TSLink authority. It is not a
sandbox against an agent with that user's shell or filesystem access.
[Remote MCP](remote-mcp.md) is off by default: the owner enables it and configures
`mcp.allow` login emails or controlled tags. It listens on a dedicated tailnet-only
node and matches callers through Tailscale WhoIs. Authorized peers can operate
shares, publish through Funnel, and send invitations; elevated invitations need
the separate opt-in. Do not interpret the allow list as a per-app role or scope.
Scoped agent roles are not shipped in this version.

TSLink's MCP operates TSLink. Publishing another HTTP MCP server through TSLink
controls reachability; it does **not** authorize that server's tools. Keep its own
authentication, tool permissions, and audit controls. Private HTTP/files use
gateway identity checks; raw TCP needs tailnet policy and backend authentication.
Public Funnel reaches anyone with the URL and needs deliberate owner consent.
See [Sharing](sharing.md), [People](people.md), and [Security](../SECURITY.md).
