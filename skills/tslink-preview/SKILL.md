---
name: tslink-preview
description: Give a running local app or dev server a private HTTPS URL with TSLink when the user wants to open it on a phone, another device, or share it privately with a colleague.
---

Path: check the app, agree on access, share, finish enrollment, get the URL,
report. Use the TSLink MCP tools if connected; otherwise the `tslink` CLI
(JSON results are under `data`).

1. Check that the app responds on its port. If TSLink is missing, ask the user
   to install it (https://github.com/anydoor7/tslink#installation); never
   install it yourself without approval.
2. Before sharing, agree on these in one message:
   - Access: without an allow list, every tailnet member permitted by policy
     can reach it. For "my phone", restrict it to the user's Tailscale login
     (ask if unknown).
   - Name: it appears in public certificate logs. Default to `preview`.
   - Background service: the first share may install a login-persistent
     TSLink service. If the user declines and TSLink is not already running,
     stop. If it is running, pass `no_daemon_install:true` / `--no-daemon-install`.
   Never enable Funnel or create guest links.
3. Share only that port:
   - MCP: `share {"target":"3000","name":"preview","allow":["user@example.com"]}`
   - CLI, restricted: check `tslink list --json` (add replaces a same-name
     entry), then `tslink add preview --proxy localhost:3000 --allow user@example.com --ephemeral --json`
   - CLI, tailnet-wide (only if agreed): `tslink share 3000 --name preview --json`
   On a name conflict, retry without the name and keep the access options. If
   a conflict asks to remove or reconfigure an existing service, ask the user.
4. Use the returned `name` from now on. If the result is `needs_login` or has
   `auth_url`, give that link only to the tailnet owner to complete Tailscale
   sign-in (and device approval if shown). It is not the app URL; never send
   it to the recipient.
5. Get the URL with MCP `url {"name":"<name>","wait":"30s"}` or
   `tslink url <name> --wait --json`. Report only that exact `url`. If it is
   still pending, say so; never guess a hostname.
6. Recipients need Tailscale and access to the node; the URL alone grants
   nothing.
7. If a background service was installed, report it: MCP `daemon_installed`
   (`manager`, `path`, `undo`), or the CLI stderr line ending
   `Undo with: tslink uninstall`.
8. Cleanup, only with the user's go-ahead: MCP `unshare {"name":"<name>"}` or
   `tslink remove <name>`. This also deletes the tailnet device and node state
   TSLink owns. Mention that a reused share may have existed before.
   Uninstalling the service affects every TSLink app; offer it, never run it
   unasked. Stopping the dev server alone leaves the share registered.
