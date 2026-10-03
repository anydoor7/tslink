# README visual assets

These are original, code-native SVG illustrations for TSLink. They explain
service identity and a local AI deployment; they are not application screenshots
or live service status. Node names, localhost ports, and client types are examples.

| Asset | Use |
|---|---|
| `tslink-mark-light.svg` / `tslink-mark-dark.svg` | TSLink mark "Split link": two identical hooks, one rotated 180 degrees, forming an S around a gap; 160 x 160. Light is for light backgrounds |
| `tslink-lockup-light.svg` / `tslink-lockup-dark.svg` | Mark plus the TSLink wordmark outlined from Lato SemiBold (SIL OFL 1.1), 576 x 160 |
| `favicon.ico` | 16, 32 and 48 px favicon frames |
| `social-preview.png` / `social-preview.svg` | 1280 x 640 social card with the mark, wordmark and tagline; the PNG is uploaded as the GitHub social preview |
| `FONT-NOTICE.md` / `LICENSE-Lato.txt` | Provenance of the outlined Lato glyphs and the unmodified SIL Open Font License text |
| `service-map-light.svg` / `service-map-dark.svg` | Per-host private/public access and CLI/MCP management, wide layout, 960 x 700 |
| `service-map-light-mobile.svg` / `service-map-dark-mobile.svg` | The same boundaries in a vertical layout, 400 x 1080 |
| `local-ai-flow-light.svg` / `local-ai-flow-dark.svg` | A local app, documents, model, and remote client, wide layout, 960 x 598 |
| `local-ai-flow-light-mobile.svg` / `local-ai-flow-dark-mobile.svg` | The same local AI workflow in a narrow layout, 400 x 880 |
| `system-architecture-light.svg` / `system-architecture-dark.svg` | Configuration and request paths for the architecture guides, 960 x 714 |
| `system-architecture-light-mobile.svg` / `system-architecture-dark-mobile.svg` | The same architecture with a compact vertical flow, 400 x 995 |
| `badge-license.svg` | Apache 2.0 license with an original document glyph |
| `badge-go.svg` | Go 1.26.6+ with an original terminal glyph |
| `badge-tsnet.svg` | Tailscale / tsnet with an original network glyph |
| `badge-mcp.svg` | MCP / scoped with an original connector glyph; tool availability depends on the session |

The logo files contain only outlined paths; their wordmark glyphs come from Lato, an SIL OFL 1.1 font (see `FONT-NOTICE.md`). The illustrations use SVG shapes and system-font text, with no remote assets,
scripts, custom fonts, or embedded HTML. Light and dark versions have equivalent
content. The narrow layouts reorganize the flow instead of shrinking the desktop
image. The refreshed service-map labels were checked at 960 px desktop and
358 px mobile image widths in Chromium, in both color schemes.

## Service access and management

The service map shows one PC, server or cloud machine. Cyan routes are private
Tailscale access; amber routes are optional public HTTPS/Funnel. Each row is an
example service, not a different host. Private HTTP/file access includes app
permissions and verified login where required; raw TCP relies on tailnet policy
and backend authentication. Public HTTP proxy apps use either a guest gate with
an expiring link and optional PIN, or an explicitly open publication. These are
distinct configurations, not a fallback around the guest gate.

Each service has its own embedded tsnet node. The shared daemon and app processes
remain on the same host, without process isolation. The dashed CLI/MCP path
represents configuration and inspection through the registry/watcher; it is not
in the ordinary request path. The private portal, health/alerts, access history
and mutation audit support per-host operation, not a combined multi-host view.

Tailscale supplies encrypted private transport and HTTPS. Setup still requires
an account, fresh-node enrollment, permissions and public Funnel enablement when
chosen. See [Architecture](../architecture.md), [Sharing](../sharing.md) and
[Guest links](../guest-links.md). Sources: `internal/server/server.go`,
`guest.go`, `portal.go`, `people.go`, and `cmd/mcp_scopes.go`.

## Local AI example

The second illustration shows a possible deployment:

1. A permitted phone, laptop, or agent sends requests and receives responses
   through encrypted tailnet transport.
2. A TSLink HTTPS node on the publishing machine forwards to a local AI app.
3. That app is configured to read local documents and call a local model.

The publishing-host boundary includes TSLink, the app, its documents, and the
model. Documents connect to the app, not directly to the model API. The app
provides document processing, retrieval if configured, and model integration;
TSLink provides transport. A raw model API does not automatically gain document
retrieval from being published through TSLink.

This is conditional on the chosen app and its configuration. It does not claim
that TSLink loads models, performs inference, indexes documents, sandboxes the
app, or prevents other network traffic. The example is a way to use local data
with a local model, not a blanket zero-cloud or no-egress guarantee.

The shipped `local-ai-suite` template in [`cmd/template.go`](../../cmd/template.go)
registers an Ollama HTTP endpoint at `localhost:11434` and an Open WebUI endpoint
at `localhost:8080`. It configures TSLink services; those applications must be
installed, running, and configured separately. The example node `model` in the
service map is illustrative and is not the template's fixed `ollama` node name.

## Provenance and badges

All service, document, database, model, device, and connector glyphs are original
SVG paths. The compact bracket/lightning motif follows the unchanged TSLink
logo. No third-party logo, stock illustration, generated bitmap, or reference
repository screenshot is embedded in these diagrams.

The local badges are static capability labels, not remotely generated status
badges. Their values come from `LICENSE`, `go.mod`, and the shared MCP tool
registry. They do not indicate certification, download counts, or a live build
result. Their intrinsic height is 24 px; they are also checked at 20 px. Allow
them to wrap on small screens instead of using a non-wrapping table.

## Embedding

From a root README, select the narrow layout at small viewport sizes and the
matching palette for the viewer's color scheme. Keep narrow sources before the
broad dark source. `img` provides a fallback for renderers without `picture`.

```html
<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="docs/assets/service-map-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/service-map-dark.svg">
  <img src="docs/assets/service-map-light.svg" alt="One app host with private Tailscale access, optional public HTTPS/Funnel, per-service nodes and CLI/MCP management." width="960">
</picture>
```

Suggested caption: "One host, separate service nodes. Private access by default;
public HTTPS only by choice. Manage each host independently."

```html
<picture>
  <source media="(max-width: 600px) and (prefers-color-scheme: dark)" srcset="docs/assets/local-ai-flow-dark-mobile.svg">
  <source media="(max-width: 600px)" srcset="docs/assets/local-ai-flow-light-mobile.svg">
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/local-ai-flow-dark.svg">
  <img src="docs/assets/local-ai-flow-light.svg" alt="Local AI deployment example: a permitted device reaches a TSLink HTTPS node through encrypted tailnet transport. A separately configured local app reads documents and calls a local model on the publishing machine, then returns the response." width="960">
</picture>
```

Suggested adjacent caption: "Example deployment: configure your app to use
local documents and a local model; TSLink provides the private connection."

Suggested alt text: "Local AI deployment example: a permitted device connects to a local TSLink HTTPS node through encrypted tailnet transport. A separately configured local app reads documents, calls a local model, and returns the result."

Keep both images' essential meanings and configuration conditions in surrounding
README prose so they remain available to readers who cannot view the diagrams.

## System architecture diagrams

The `system-architecture-*` family remains a detailed private-only reference;
the README and architecture guide now use the private/public `service-map-*`
family above. In the private-only reference, dashed amber arrows show changes from
CLI/MCP through `registry.json` to the daemon's registry watcher. Solid lines
show the private request path through tailnet policy and separate service nodes
to local app, file, database, and model targets. One shared daemon contains the
watcher and all illustrated nodes; separate identities do not mean separate
host processes.

The wide view maps each node to its target. The narrow view groups the same
four local targets to keep labels legible. HTTP proxy/file services can add an
allow list; raw TCP has no HTTP WhoIs layer and relies on tailnet policy and
application authentication. The guide explains these details in accessible
text. The graph abstracts setup, supervision, and optional remote MCP/Funnel;
it is not a full deployment topology or a live capture.

Sources: `internal/server/server.go` (registry watcher, shared node lifecycle,
HTTP/file handlers, and raw TCP branch), `cmd/add.go` (`executeAdd` registry
persistence), and `cmd/mcp.go` (shared add operation). The architecture guides
embed the same light/dark and wide/mobile variants with `assets/` paths.
