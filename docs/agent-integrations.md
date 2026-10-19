# Coding agent integrations

Install the [TSLink CLI](../README.md#installation) first and make `tslink`
available on the coding agent's `PATH`. The integration uses that binary; it
does not bundle or install it.

## Claude Code

In Claude Code, add the marketplace and install the plugin:

```text
/plugin marketplace add anydoor7/tslink
/plugin install tslink@tslink
```

Restart Claude Code or reload plugins after installation. Ask the agent to
open your running dev server on your phone, or invoke
`/tslink:tslink-preview`. The plugin supplies the preview skill and an MCP
server launching `tslink mcp`. Avoid configuring a second TSLink MCP server
in the same client.

## Other agents

Use [skills/tslink-preview/SKILL.md](../skills/tslink-preview/SKILL.md) with an
agent that supports Agent Skills. Copy the `tslink-preview` directory into
that agent's supported skills location. The same file ships in the Claude
Code plugin through the repository's default `skills/` directory; there is
no second copy to maintain.

If the agent supports MCP, [connect TSLink](mcp-clients.md#stdio) for direct
tool calls. Otherwise the skill uses the CLI's versioned JSON results.

## What the user still does

For each fresh node on the default credential-free path, the agent returns
a Tailscale browser enrollment link. Complete that enrollment before the
agent can retrieve the exact app URL. Tailnet device approval may also apply.
Phones and private recipients need Tailscale and permission to reach the node;
a URL alone does not invite an outside colleague. Sharing may install a
persistent background service. Remove the preview with
`tslink remove <returned-name>` when finished; other apps keep running.

## Local plugin validation

From a checkout, use an isolated configuration directory and local marketplace:

```sh
preview_test_config=$(mktemp -d)
# Plugin manifest
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin validate --strict .claude-plugin/plugin.json
# Marketplace manifest (validating . also selects this manifest)
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin validate --strict .claude-plugin/marketplace.json
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin marketplace add "$PWD"
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin install tslink@tslink
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin list
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin details tslink
```

These commands validate and install the plugin without sharing a port or
enrolling a Tailscale node. The installed plugin should resolve one
`skills/tslink-preview/SKILL.md` and the MCP configuration from `plugin.json`.
The plugin intentionally omits `version` so updates follow the git commit.
Claude Code 2.1.302 reports this as a missing-version warning and exits nonzero
under `--strict`; inspect the output for any additional errors or warnings.
