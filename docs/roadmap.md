# Roadmap

## Roadmap

The optional one-accept gateway, local portal-only apps, explicit remote HTTPS routes, home portal, access requests, browser guest links, flexible durations, access history, scoped MCP roles and QR onboarding are available. The remaining areas below are planned or limited as indicated:

| Area | Current status |
|---|---|
| Docker labels | Not implemented. |
| Middleware | Not implemented; there are no registry fields or flags for it yet. |
| Admin dashboard / REST API | No dashboard or REST handler is shipped; the tailnet-only MCP control plane (`tslink serve --mcp`) is the only remote management surface. Future dashboard or REST work must be explicitly experimental and tested end to end. |
| Prometheus `/metrics` | Not implemented; there is no request instrumentation and no scrape endpoint. |
| Custom domain / ACME | Not implemented; there are no registry fields or flags for it yet. |
| Cluster sync | Not implemented. |
| Federated multi-host app catalog | Planned. Manually registered remote gateway routes are available; discovery and catalog synchronization are not. |
| Signed visitor identity assertions | Planned. Remote apps authenticate the gateway tag, not a forwarded human identity. |
| App cookie namespacing | Planned. Gateway ports separate origins but share cookies on the same hostname. |
| Portal-only apps by default | Planned. Gateway and portal-only registration remain explicit opt-ins. |
| Marketplace or third-party template registry | Not implemented. |
| Docker image | Not published. |
| Headscale end-to-end validation | Pending. |
| Other Layer 2 modules | Pending integration tests. |
