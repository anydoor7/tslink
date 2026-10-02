# Home portal golden pages

- `portal-apps.golden.html`: visible apps, all four health states, finite/no expiry and unavailable address.
- `portal-empty.golden.html`: no authorized apps and nontechnical help.
- `portal-tcp.golden.html`: TCP connection address without a broken browser link.
- `portal-hostile.golden.html`: escaped hostile service name, URL query and expiry text.

Regenerate intentionally with `UPDATE_PORTAL_GOLDEN=1 go test ./internal/server -run TestPortalGoldenHTML`; ordinary tests compare committed bytes.
