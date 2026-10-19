# Claude Code marketplace

- [marketplace.json](marketplace.json): `tslink` marketplace, fetching its
  single plugin from the GitHub source `anydoor7/tslink-claude-plugin`.
- [Plugin repository](https://github.com/anydoor7/tslink-claude-plugin): the
  plugin root, its only `plugin.json`, and the `tslink mcp` connection.

The canonical [preview skill](../skills/tslink-preview/SKILL.md) stays here.
The plugin repository carries a CI-checked copy; edit the skill here first.
See [isolated validation](../docs/agent-integrations.md#local-plugin-validation)
for separate marketplace and plugin checks.
