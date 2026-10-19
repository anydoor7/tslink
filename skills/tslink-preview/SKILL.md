---
name: tslink-preview
description: Give a running local app or dev server a private HTTPS URL with TSLink when the user wants to open it on a phone, another device, or share it privately with a colleague.
---

1. Confirm the app's listening port and check that it responds locally. Prefer
   connected TSLink MCP tools; otherwise run `tslink --help` to check installation.
   If missing, ask the user to install it from
   https://github.com/anydoor7/tslink#installation or get explicit approval before
   running an install command. Never install the CLI without the user's approval.
2. Before the first share, explain that it may install a login-persistent OS
   background service (boot autostart is possible on Linux). Offer MCP
   `no_daemon_install:true` / CLI `--no-daemon-install`: `share` then requires an
   already running daemon; CLI `add` only saves configuration with that flag.
   Explain before sharing that without an allow list, every tailnet member may
   reach the app subject to tailnet policy. For "my phone/my devices", recommend
   MCP `allow:["<user login>"]`; ask for the Tailscale login if unknown.
   The name is public in certificate logs; use a neutral DNS label like `preview`
   or ask the user. Do not enable Funnel or create guest links.
3. Share only the requested port. With MCP, call `share`, for example
   `{"target":"3000","name":"preview","allow":["user@example.com"]}`, using the
   actual port and agreed login/name. CLI `share` has no `--allow`; for restricted
   access use `tslink add preview --proxy localhost:3000 --allow user@example.com --json`
   after checking `tslink list --json` to avoid replacing an existing name.
   For agreed tailnet-wide access use `tslink share 3000 --name preview --json`.
   If the target is already shared under another name, follow the conflict
   message: retry `share` without `name` / `--name`, preserving the access options;
   do not remove an existing share to resolve the conflict.
4. Keep the returned `name`, including any collision suffix. MCP results expose
   fields directly; CLI JSON puts them under `data`. A successful `needs_login`
   result, or CLI `add` with `auth_url`, is waiting for a person: give `auth_url`
   only to the tailnet owner, never the recipient. Explain that the owner must
   open it and complete Tailscale browser enrollment for this fresh app node.
   Never present that enrollment link as the app URL. Tailnet device approval may
   also be required. No administrative API token is needed for this default path.
   If `daemon_installed` is present, report its `manager`, `path` and exact `undo`.
5. After enrollment, call MCP `url` with the returned `name` and `wait:"30s"`, or
   run `tslink url <returned-name> --wait --json`. Use only its exact `url`.
   If enrollment or URL readiness is still pending, report that state and follow
   the returned guidance; never guess a hostname or claim the app is reachable.
6. Give the exact HTTPS app URL to the user. Private recipients need Tailscale
   connected and permission to reach that node; sending the URL alone does not
   grant access to an outside colleague.
7. Explain cleanup: the share may have existed before this call; mention that
   when obtaining cleanup authorization. MCP `unshare` with that exact `name`,
   or `tslink remove <returned-name>`, removes this share and may delete its
   recorded tailnet device and local node state when ownership permits. Obtain
   cleanup authorization before executing it. Include any `daemon_installed.undo`
   in cleanup instructions: it removes shared autostart, so get authorization
   after checking whether other apps depend on it. Stopping the dev server alone leaves
   the registration; do not stop the shared daemon or remove other apps unasked.
