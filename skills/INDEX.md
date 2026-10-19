# Agent skills

- [tslink-preview/SKILL.md](tslink-preview/SKILL.md): private dev-server previews
  through MCP or the CLI. This is the canonical source for Agent Skills users.

The [Claude Code plugin repository](https://github.com/anydoor7/tslink-claude-plugin)
carries a byte-identical copy checked by its CI on pushes to main and pull
requests, plus a best-effort daily schedule. This repository's required
`plugin-sync` CI job checks that copy and the MCP command at the marketplace's
pinned SHA on every CI tier. Push the plugin copy first, then include canonical
skill edits and the new pin in a reviewed PR here. See the
[update flow](../docs/agent-integrations.md#shipping-a-plugin-update).
