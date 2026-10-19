# Claude Code integration

- [plugin.json](plugin.json): `tslink` plugin, using the repository's default
  `skills/` directory and launching `tslink mcp`.
- [marketplace.json](marketplace.json): `tslink` marketplace, listing the
  repository root as the single plugin source.

Because the plugin source is the repository root, root-level `bin/`, `hooks/`,
`agents/`, `commands/` and `.mcp.json` become plugin components if added.
Review new root components as plugin changes too.
