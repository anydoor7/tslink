---
name: tslink-preview
description: Give a running local app or dev server a private HTTPS URL with TSLink when the user wants to open it on a phone, another device, or share it privately with a colleague.
---

1. Confirm the app's listening port and check that it responds locally. Prefer
   connected TSLink MCP tools; otherwise run `tslink --help` to check installation.
   If missing, use the platform instructions at
   https://github.com/anydoor7/tslink#installation before continuing.
2. Share only the requested port. With MCP, call `share` with
   `{"target":"3000","name":"preview"}` (substitute the actual port and a short
   DNS-label name). Otherwise run `tslink share 3000 --name preview --json`.
   Sharing may install TSLink's persistent OS background service when absent.
   Keep the default private exposure; do not enable Funnel or create guest links.
3. Keep the returned `name`, including any collision suffix. MCP results expose
   fields directly; CLI JSON puts them under `data`. A successful `needs_login`
   result is waiting for a person: give the user `auth_url` and explain that they
   must open it and complete Tailscale browser enrollment for this fresh app node.
   Never present that enrollment link as the app URL. Tailnet device approval may
   also be required. No administrative API token is needed for this default path.
4. After enrollment, call MCP `url` with the returned `name` and `wait:"30s"`, or
   run `tslink url <returned-name> --wait --json`. Use only its exact `url`.
   If enrollment or URL readiness is still pending, report that state and follow
   the returned guidance; never guess a hostname or claim the app is reachable.
5. Give the exact HTTPS app URL to the user. Private recipients need Tailscale
   connected and permission to reach that node; sending the URL alone does not
   grant access to an outside colleague. By default, other tailnet members may
   also reach the app subject to tailnet policy. MCP `share` accepts `allow` for
   named HTTP readers when requested; CLI `share` has no `--allow` flag.
6. Explain cleanup: when the user is done, MCP `unshare` with that exact `name`,
   or `tslink remove <returned-name>`, removes this share and may delete its
   recorded tailnet device and local node state when ownership permits. Obtain
   cleanup authorization before executing it. Stopping the dev server alone
   leaves the registration; do not stop the shared daemon or remove other apps.
