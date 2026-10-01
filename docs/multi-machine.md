# Multi Machine

## Working Across Machines

The registry in `~/.config/tslink/registry.json` is per machine, while the tailnet is shared. Two read-only features make that boundary visible, and the [Remote MCP Control Plane](remote-mcp.md) lets an agent on another tailnet machine operate this install.

### Every TSLink device in the tailnet

`tslink list` reads this machine's registry. `tslink list --tailnet` asks the Tailscale API instead and reports every TSLink-tagged device in the tailnet: services registered here, services registered on other machines, and orphan nodes. A device counts as TSLink-tagged when it carries the configured default tag (`tag:tsmain` unless changed), `tag:tslink-funnel`, or any other `tag:tslink-*` tag.

```bash
tslink list --tailnet
tslink list --tailnet --json
```

Each row says which side of the machine boundary it came from:

| `origin` | Meaning |
|---|---|
| `local_registry` | This machine's `registry.json` holds a service with exactly this hostname |
| `local_name_variant` | The hostname is a `<service>-N` tsnet collision variant of a service registered here, the usual shape of an orphan this machine left behind |
| `unregistered` | This machine's registry knows nothing about the hostname: another machine's service, or an orphan |

The human view ends with `N of M TSLink-owned tailnet devices are not registered on this machine.` and the JSON payload carries `registered_count` and `unregistered_count` alongside `count`. Every result also carries a constant `cleanup_authority` field, because the view exposes a real limit: `tslink cleanup` deletes only devices whose exact NodeID is recorded in this machine's local `node-ownership.json`, so a device this machine's registry does not name must be cleaned up from the machine that created it. `--tailnet` never emits NodeIDs and never deletes anything.

`--tailnet` needs a stored Tailscale API credential (a `tskey-api-*` access token or an OAuth client secret). Without one it fails with `auth_error` (exit 3) and bootstrap guidance in `error.next`; it does not return an empty list. It also conflicts with `--name`, `--type`, `--fields`, and `--verbose` (exit 2), because those filter this machine's registered services while `--tailnet` reports tailnet devices.

### Tailscale SSH as the remote CLI path

If Tailscale SSH is enabled on the machine running TSLink and the tailnet policy has an `ssh` rule admitting you, `tailscale ssh <host> tslink <command>` drives that install from any other tailnet device with no extra software. Both halves are Tailscale-layer configuration: TSLink neither enables Tailscale SSH nor edits the policy, and it never requires either.

To make the first half discoverable, `tslink doctor` reads Tailscale SSH enablement from the local `tailscaled` and prints `Tailscale SSH (this node): <state>`. The JSON payload carries `tailscale_ssh.state` and `tailscale_ssh.acl_rule_required: true`, the latter recording the half no local read can observe.

| State | Finding code | What it says |
|---|---|---|
| `enabled` | `tailscale_ssh_enabled` | `tailscale ssh <this-host> tslink list --json` works once a tailnet ACL `ssh` rule admits the caller |
| `disabled` | `tailscale_ssh_disabled` | Run `tailscale set --ssh` on this machine and add the ACL `ssh` rule to use the remote path |
| `unknown` | `tailscale_ssh_unknown` | The local Tailscale client state could not be read within one second; check `tailscale status` |

All three outcomes are informational. They never change doctor's status or exit code.

Set `TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1` to skip the local read, for example when a test suite runs the compiled binary on a developer's machine. The state is then `unknown`, and the `tailscale_ssh_unknown` finding says the check was skipped.

