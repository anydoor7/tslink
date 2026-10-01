# Roadmap

## Roadmap

These features are not part of the shipped runtime:

| Area | Current status |
|---|---|
| Docker labels | Not implemented. |
| Middleware | Not implemented; there are no registry fields or flags for it yet. |
| Admin dashboard / REST API | No dashboard or REST handler is shipped; the tailnet-only MCP control plane (`tslink serve --mcp`) is the only remote management surface. Future dashboard or REST work must be explicitly experimental and tested end to end. |
| Prometheus `/metrics` | Not implemented; there is no request instrumentation and no scrape endpoint. |
| Custom domain / ACME | Not implemented; there are no registry fields or flags for it yet. |
| Cluster sync | Not implemented. |
| Member portal or service directory | Not implemented. |
| Marketplace or third-party template registry | Not implemented. |
| Docker image | Not published. |
| Headscale end-to-end validation | Pending. |
| Other Layer 2 modules | Pending integration tests. |

