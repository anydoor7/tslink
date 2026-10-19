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

This repository hosts the marketplace. Its GitHub source fetches the plugin
from [anydoor7/tslink-claude-plugin](https://github.com/anydoor7/tslink-claude-plugin),
which owns the plugin manifest. The installation commands above stay the
same. The integration needs a local TSLink binary and does not work on
surfaces without one, such as claude.ai chat.

## Other agents

Use [skills/tslink-preview/SKILL.md](../skills/tslink-preview/SKILL.md) with an
agent that supports Agent Skills. Copy the `tslink-preview` directory into
that agent's supported skills location. This repository is the canonical
source. The Claude Code plugin repository ships a byte-identical copy;
its CI compares that copy with this repository's `main` on pushes, pull
requests and a daily schedule. Land skill edits here first, then update
the plugin copy.

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

From this repository's root, use an isolated configuration directory and
local marketplace. Installation fetches the plugin from GitHub, so push any
plugin repository changes before testing this path:

```sh
preview_test_config=$(mktemp -d)
# Marketplace only; there is no plugin manifest in this repository
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin validate .
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin marketplace add "$PWD"
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin install tslink@tslink
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin list
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin details tslink
```

These commands validate and install the plugin without sharing a port or
enrolling a Tailscale node. The installed plugin should resolve one
`skills/tslink-preview/SKILL.md` and one MCP server (`tslink`, running
`tslink mcp`) from the GitHub plugin repository. `details` lists the source
as `tslink@tslink`; the marketplace entry specifies the GitHub repository,
and `list` shows the installed commit-derived version. Marketplace
validation should pass without warnings; it does not validate the remote
plugin's files.

To validate the plugin itself, clone its repository separately:

```sh
git clone https://github.com/anydoor7/tslink-claude-plugin.git
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin validate ./tslink-claude-plugin
```

The plugin intentionally omits `version` so updates follow the git commit.
Plugin validation should print `Validation passed with warnings` with
exactly one warning, for the missing `version`; any other warning or error
is a regression. `--strict` turns that expected warning into a failure, so
it cannot gate the plugin manifest.
