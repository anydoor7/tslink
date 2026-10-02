# Recipe proxy configuration checks

> **定位**: Recipe integration tests against the real reverse proxy.
> **下一跳**: `recipe_proxy_test.go` and `recipe_bridge_test.go`.
> **边界**: Server architecture is described in the repository `AGENTS.md`.

- `recipe_proxy_test.go`: real `NewProxyHandler` headers and Host/Origin-sensitive catalog settings; optional receipts for disposable upstream consumer validation.
- `recipe_bridge_test.go`: catalog bridge snippets through disposable Nginx, with real proxy requests and backend header checks.

This index covers the recipe integration tests. The repository AGENTS.md describes the server architecture.
