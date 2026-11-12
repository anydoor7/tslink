# Contributor documentation

> **Purpose**: Contributor and CI documentation entry points.
> **Next**: CONTRIBUTING.md for CI policy and local checks.
> **Boundary**: Command behavior and release payload details are in docs/.

- [CONTRIBUTING.md](CONTRIBUTING.md): development, licensing and CI policy.
- [Release artifacts](docs/release-artifacts.md): release contents and CI gate.
- [CI scripts](.github/scripts/INDEX.md): trusted classifier and hermetic regression tests.
- [llms.txt](llms.txt): compact product documentation discovery index.
- [gemini-extension.json](gemini-extension.json): Gemini CLI extension manifest; it runs the local `tslink mcp` server and is indexed by the geminicli.com gallery through the `gemini-cli-extension` topic.
- [glama.json](glama.json): Glama directory maintainer claim for the listing at glama.ai/mcp/servers/anydoor7/tslink.
- [server.json](server.json): MCP Registry entry `io.github.anydoor7/tslink`; its package is the macOS `.mcpb` bundle attached to each GitHub release (see docs/release-artifacts.md).
- [Scoped third-party source](third_party/INDEX.md): pinned local dependency repairs and provenance.
- [Agent skills](skills/INDEX.md): private preview workflow shared with the Claude Code plugin.
- [Claude Code marketplace](.claude-plugin/INDEX.md): catalog pointing to the standalone plugin repository.
- [Coding agent integrations](docs/agent-integrations.md): setup, enrollment and isolated plugin validation.
