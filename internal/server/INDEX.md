# Recipe proxy configuration checks

> **定位**: Recipe integration tests against the real reverse proxy.
> **下一跳**: `recipe_proxy_test.go` and `proxy_preserve_host_test.go`.
> **边界**: Server architecture is described in the repository `AGENTS.md`.

- `recipe_proxy_test.go`: real `NewProxyHandler` headers and Host/Origin-sensitive catalog settings; optional receipts for disposable upstream consumer validation.

This index covers the recipe integration tests. The repository AGENTS.md describes the server architecture.
- `proxy_preserve_host_test.go`: real HTTP/TLS Host forwarding, regenerated forwarded headers and hot-reload policy changes.

- `host_authority_test.go`: reviewer-derived real TLS, HTTP/1 absolute-form and HTTP/2 cross-node virtual-host regressions.
- `canonical_host_test.go`: trusted runtime name selection, normalization, missing-name refusal and recovery.
