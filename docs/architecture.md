# Architecture

## How It Works

```
┌─────────────┐         ┌──────────────────────┐         ┌──────────────┐
│ Your Machine│         │   Tailscale Network  │         │  Your Phone  │
│             │         │   (WireGuard mesh)    │         │              │
│  localhost   │◄──────►│                      │◄──────►│  Browser     │
│  :3000      │  tsnet  │  WireGuard encrypted  │  HTTPS │              │
│  :5432      │  nodes  │  Encrypted tailnet path│  +TLS  │              │
│  ~/Documents│  (1/svc)│                       │        │              │
└─────────────┘         └──────────────────────┘         └──────────────┘
```

TSLink creates a dedicated [tsnet](https://tailscale.com/kb/1244/tsnet) node for each registered service: no Tailscale client installation required on the server side. Each service joins your tailnet as its own device (e.g., `myapp`, `docs`, `mydb`). Proxy/file services use Tailscale HTTPS listeners; raw TCP services use private tailnet transport and proxy bytes to the configured target.

**Key architectural decisions:**
- **Per-service embedded nodes**: each service gets its own tailnet identity and hostname; proxy/file services also get Tailscale HTTPS listener semantics
- **Identity-aware proxying**: WhoIs verification on tailnet HTTP proxy/file requests; the proxy strips client-supplied Tailscale identity headers, including underscore variants, before injecting verified identity; public Funnel and raw TCP do not get TSLink-enforced HTTP identity
- **Secure credential management**: system keychain storage; macOS/Linux restricted-permission file fallback only after stale keychain authority is proven absent or cleared
- **File-based registry**: services persist across restarts in `~/.config/tslink/registry.json`
- **Hot reload**: file watcher on the registry means `tslink add` takes effect without restarting the server
- **PID-based lifecycle**: daemon management with process identity checks and platform-specific stop behavior
- **Structured logging**: slog-based structured logging with access logs

