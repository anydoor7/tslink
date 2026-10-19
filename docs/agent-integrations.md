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
which owns the plugin manifest. The marketplace's `source.sha` pins an exact
reviewed plugin commit. Pushing to the plugin repository alone does not ship
an update through this marketplace. The installation commands above stay the
same. The integration needs a local TSLink binary and does not work on
surfaces without one, such as claude.ai chat.

## Other agents

Use [skills/tslink-preview/SKILL.md](../skills/tslink-preview/SKILL.md) with an
agent that supports Agent Skills. Copy the `tslink-preview` directory into
that agent's supported skills location. This repository is the canonical
source. The Claude Code plugin repository ships a byte-identical copy;
its CI compares that copy with this repository's `main` on pushes to main and
pull requests, plus a best-effort daily schedule that GitHub can disable on
inactive repositories. This repository's required `plugin-sync` CI job fetches
the pinned plugin commit and compares its skill with the proposed canonical
skill on every CI tier. It also checks the plugin's `tslink mcp` command.
Update both copies and the marketplace pin together as described below.

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

To validate the plugin itself, clone it into the temporary directory and
check out the marketplace's pinned commit:

```sh
preview_plugin_sha=$(python3 -c 'import json; print(next(p["source"]["sha"] for p in json.load(open(".claude-plugin/marketplace.json"))["plugins"] if p["name"] == "tslink"))')
git clone https://github.com/anydoor7/tslink-claude-plugin.git "$preview_test_config/tslink-claude-plugin"
git -C "$preview_test_config/tslink-claude-plugin" checkout --detach "$preview_plugin_sha"
CLAUDE_CONFIG_DIR="$preview_test_config" claude plugin validate "$preview_test_config/tslink-claude-plugin"
```

The plugin intentionally omits `version` so its version follows the pinned git commit.
Plugin validation should print `Validation passed with warnings` with
exactly one warning, for the missing `version`; any other warning or error
is a regression. `--strict` turns that expected warning into a failure, so
it cannot gate the plugin manifest.

## Shipping a plugin update

1. Prepare canonical skill edits in this repository, copy them byte for byte
   into the plugin repository, and commit and push the plugin changes first.
   For manifest-only or README edits, the skill can remain unchanged.
2. Open a reviewed PR in this protected repository with the canonical skill
   changes (if any) and update `.claude-plugin/marketplace.json`'s `source.sha`
   to the plugin commit's full 40-character lowercase SHA. Keep the marketplace
   description identical to `plugin.json`.
3. Run `python3 -I .github/scripts/check-plugin-sync.py` and the isolated
   installation above. The check fails on skill drift, an unavailable commit,
   a changed MCP launch command, or mismatched descriptions. Review the whole
   plugin commit before approving the pin update.
4. After that PR is reviewed and merged, users run `/plugin marketplace update
   tslink` and `/plugin update tslink@tslink`, then reload plugins or restart
   Claude Code. Plugin repository pushes alone leave the installed pin unchanged.
