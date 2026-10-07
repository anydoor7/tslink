# Choose a sharing tool

Primary sources accessed **2026-10-02**; the Cloudflare Quick Tunnels and zrok
rows were checked on **2026-10-06**. The choices below are judgments about
workflow fit, based on those sources and this version's TSLink documentation;
they are not performance benchmarks or a claim that other tools lack features.
Recheck current plans, platform support, and security requirements before choosing.

TSLink fits when one person already runs several apps and wants separate private
addresses, named-person deadlines for HTTP/files, and an inspect/remove workflow
through CLI or MCP. It works with Tailscale, independent project. See
[Sharing](sharing.md), [People](people.md), and [Agents](agents.md).

| Choose | When it fits or wins | What to account for |
|---|---|---|
| **Tailscale Serve** | You already run the Tailscale client and want to expose a local web service, file/directory, or TCP forwarder inside your tailnet. Reusing that daemon is a simpler starting point for a single service. [Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve) | Serve supports multiple targets and service configuration; TSLink's separate embedded node per app is a different operating model. macOS file serving depends on the Tailscale client variant. |
| **Tailscale Services** | You administer resources across hosts and need a stable service name while moving hosts, adding redundant hosts, or steering traffic. This wins over TSLink's single-publishing-host availability model. [Services](https://tailscale.com/docs/features/tailscale-services) | Services decouple resource addresses from hosting devices, with granular access control and approval workflows. Follow its service-host and tailnet-policy setup; TSLink is not a replacement for that multi-host routing layer. |
| **ngrok** | You need a public localhost URL for a webhook, API demo, or preview, or want its managed gateway/Traffic Policy workflow. [Get started](https://ngrok.com/docs/start) | Its docs include authentication, traffic policy, observability, and MCP connectivity. Choose based on endpoint policy and deployment needs, not an assumption that public tunnels cannot authenticate users. |
| **Cloudflare Tunnel** | You want origins connected to Cloudflare's network through outbound-only `cloudflared` connections, including apps behind a firewall. [Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) | Tunnel supplies connectivity; decide separately which applications are public and which need Access policies. Its network/account setup differs from private tailnet enrollment. |
| **Cloudflare Quick Tunnels** | You need a quick preview for one or a few people now, with no account on either side. Since `cloudflared` 2026.9.3 (announced 2026-10-02), `cloudflared tunnel --url http://localhost:8080 --allowed-mail you@example.com` admits only the listed addresses or domains; each visitor proves the address with a one-time PIN sent by email, and it costs nothing. This wins over TSLink for zero-setup previews: no Tailscale account, enrollment, or Funnel permission. [Protected Quick Tunnels](https://blog.cloudflare.com/protected-quick-tunnels/), [Quick Tunnels](https://developers.cloudflare.com/tunnel/get-started/quick-tunnels/) | A visitor session lasts up to four hours, or until `cloudflared` stops. Access ends for everyone when the process exits, and changing the allowed list means starting a new tunnel on a new random `trycloudflare.com` hostname. Cloudflare describes Quick Tunnels as for testing and development, with no uptime guarantee, a limit of 200 in-flight requests, no Server-Sent Events, and email checks only in an interactive browser. TSLink grants and guest links store absolute deadlines that survive restarts, and each can be revoked on its own without stopping the app. |
| **zrok** | You want open-source sharing you can self-host on an OpenZiti network, or NetFoundry's hosted service with a free tier, without a Tailscale account. Private shares use closed permission mode by default: `zrok2 share private localhost:8080 --access-grant friend@example.com` admits another zrok account by email, and `zrok2 modify share` adds or removes grants later. It also shares files and other resources besides HTTP. [Permission modes](https://netfoundry.io/docs/zrok/how-tos/shares/configure-permission-modes/), [Pricing](https://zrok.io/pricing/) | Each recipient of a private share needs a zrok account and runs `zrok2 access private` on their own device. The hosted free tier lists 5 GB per day. The first visit to a public share from an unverified free account shows an anti-phishing interstitial page; adding a card removes it. TSLink recipients use Tailscale or, for HTTP apps, a browser guest link; people grants default to a 24-hour deadline, and a permanent grant needs explicit acknowledgement. |
| **Pangolin** | You want a self-hostable or managed identity-aware access platform across sites/resources, with roles and audit trails; or need expiring, revocable browser access links now. [Introduction](https://docs.pangolin.net/), [Access links](https://docs.pangolin.net/manage/access-control/links) | This overlaps TSLink's access workflow. Choose the platform/deployment model that fits. TSLink's browser guest links are expiring bearer links with an optional PIN that can be forwarded and do not verify identity; its scoped agent roles and audit receipts cover one host at a time. |
| **Cloudflare Access** | Your organization needs an identity-aware proxy with IdP and device-posture policy, or temporary access approvals. [Web applications](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/), [Temporary authentication](https://developers.cloudflare.com/cloudflare-one/access-controls/policies/temporary-auth/) | Access checks requests against policies; Tunnel and Access can be composed. Choose this for its organizational policy workflow rather than treating temporary authorization as unique to TSLink. |
| **Umbrel** | You want a home-server OS that installs and runs apps, with household accounts/app sharing and an agent connection. [umbrelOS](https://umbrel.com/umbrelos), [Sharing with users](https://umbrel.com/support/basics/sharing-your-umbrel-with-other-users), [AI agents](https://umbrel.com/support/advanced/connecting-ai-agents) | Umbrel already offers sharing and MCP. It wins when app installation and home-server management are the task. TSLink publishes apps you already operate and does not replace their runtime or accounts. |
| **Coolify** | You want to build, deploy, and operate applications/databases on your servers through a dashboard/API, using Git, Dockerfiles, Compose, or images. [What is Coolify](https://coolify.io/docs/core/what-is-coolify), [MCP](https://coolify.io/docs/mcp/what-is-mcp) | Coolify handles deployment, domains, HTTPS, and operations. Its [security model](https://coolify.io/docs/core/security-model) distinguishes infrastructure administration from app security. Keep visitor authorization explicit when composing it with an access gateway. |
| **TSLink** | Your apps should keep running where they are, while you or your agent manage per-app publication and private HTTP/file people grants with deadlines. [Quickstart](getting-started.md), [Agent quickstart](agent-quickstart.md) | Install with Homebrew on macOS and Linux, a Windows zip, or `.deb`/`.rpm` packages from the release; building from source needs Go/Git. Fresh nodes need browser enrollment and possibly device approval. All service nodes share one daemon/host. Raw TCP access uses tailnet policy and backend authentication. |

## Boundaries that matter more than a feature score

TSLink does not install apps, isolate workloads, or provide multi-host failover.
Private recipients need Tailscale access; Funnel and browser guest links use
public HTTPS, and a guest link can be forwarded without verifying the visitor. People revocation/expiry denies new
requests, but cannot recall delivered data or close already accepted streams.
Gateway policy covers traffic through TSLink, so a directly reachable backend or
another public route needs its own protection. Application logins and upstream
MCP tool permissions remain the application's responsibility.

These products can complement each other: run an app with Umbrel or Coolify,
then choose the ingress and identity layer for its actual audience. Verify the
recipient's path, existing public routes, and the backend's login/session behavior.

## Tailscale alone or TSLink

Serve is enough for one web app on your own devices. TSLink manages per-app nodes, access deadlines, a portal and backend health together on one host. Tailscale already offers service names, granular access and JIT workflows; choose the setup that fits the job.

Official sources accessed **2026-10-07**; TSLink behavior checked against **v0.1.0** documentation. These are workflow comparisons, not performance or recipient-compatibility tests.

| Job | Tailscale alone | TSLink |
|---|---|---|
| Open one web app from your own phone | `tailscale serve 3000` is enough once the app, Tailscale and HTTPS are ready; use `--bg` to keep serving after the terminal closes. [^serve][^serve-examples] | `tslink share 3000`; a fresh app node needs enrollment, then retrieve its exact URL. [Sharing](sharing.md) |
| Three apps, each with its own name | Ordinary Serve uses the device name, with different ports or paths. Services supplies separate names: define services, use a tagged host, configure/advertise endpoints, approve or auto-approve the host, and allow access in policy. Separate Tailscale nodes are another option. [^serve][^services][^docker] | One `share --name` or `add` per app; TSLink runs one embedded node per app in a shared daemon. Enroll each fresh node; device approval may apply. [Architecture](architecture.md) · [Getting started](getting-started.md) |
| Give one person one app for seven days | Invite them into the tailnet or share the host; restrict access to the app's node/port without broader matching grants. Ordinary network rules have no documented expiry field. Use expiring posture attributes, JIT automation, or scheduled rule removal. [^invites][^sharing][^grants][^jit] | `tslink people add alice@example.com --apps photos --for 7d`, for enrolled private HTTP/file apps and the actual Tailscale login. Network access must already exist; outsiders need app invitations. [People](people.md) |
| A browser link without Tailscale, for three days | Funnel publishes an internet endpoint; the documented CLI has no expiry or visitor-authentication flag. Add application authentication or a gate, and arrange shutdown at the deadline. [^funnel] | `tslink guest create photos --for 3d --public --print-link`, for an online HTTP proxy app with Funnel permission. A bearer gate checks expiry; optional `--pin`; revoke links individually. [Guest links](guest-links.md) |
| Revoke access, inspect visits and check app health | Remove all matching permissions or the device share; disable Funnel separately. Configuration and network-flow logs exist; use app logging and backend probes for HTTP request history and app health. [^grants][^sharing][^funnel][^logging][^flows][^docker] | `tslink people remove alice@example.com` (whole person), `tslink guest revoke <id>`, `tslink access log --app photos`; background backend checks and optional command/webhook notifications. Guest history identifies a link, not a person. [Access history](access-log.md) · [Health](health-and-alerts.md) |
| Let an AI agent manage this | The Tailscale CLI and API automate device/service configuration and policy operations; combine them with your chosen deadline, app-log and health workflow. [^serve][^api][^jit] | Management commands accept `--json`; `tslink mcp` uses JSON-RPC, with owner or reduced app-scoped roles. Human enrollment may still be required. Reduced roles cannot create apps/guest links; roles do not restrict the agent's shell. [JSON](json-automation.md) · [MCP scopes](mcp-scopes.md) |

Private visits need Tailscale on the recipient's device and a policy that permits the connection. TSLink's optional [portal](portal.md) lists permitted apps, addresses, health and deadlines.

Outsiders: add `--invite --print-links` to `people add` with a stored user-owned API token, or create app shares manually. Each app needs its own recipient acceptance; an emailed device invite can be accepted by a different Tailscale account, but the TSLink grant checks the actual login. [People](people.md)[^sharing]

People expiry/removal denies subsequent HTTP/file requests; accepted streams and network shares may remain. Guest revoke/expiry cancels requests and closes streams; an already authorized bounded request may finish. Neither can recall delivered data. Access history records gateway events, not a complete audit of every route to the backend. [People](people.md) · [Guest links](guest-links.md) · [Access history](access-log.md)

Tailscale already supports automatic deadlines through expiring posture attributes (documented for Premium/Enterprise), its Slack Accessbot example, API automation and third-party integrations. Tailscale PAM, currently beta, also documents scoped, time-bound privileged access. These overlap the temporary-access job. [^jit][^pam]

### Tailscale alone is enough when…

- You want your own phone to open one web app; Serve already does that. [^serve-examples]
- Ports or paths under one device name suit your apps, and the existing access policy suits their users. [^serve][^grants]
- You already operate Services with tagged hosts and approvals, especially for resources served by several hosts. [^services]

### TSLink does not…

- Install apps, isolate workloads, or provide multi-host failover; app nodes share one publishing host and daemon.
- Replace tailnet policy or app logins. Raw TCP uses tailnet policy and backend authentication; directly reachable backends and other public routes need their own protection.
- Make guest links private or verify the visitor's identity. They use public HTTPS and can be forwarded, including with a PIN.

[^serve]: [Serve CLI](https://tailscale.com/docs/reference/tailscale-cli/serve), accessed 2026-10-07.
[^serve-examples]: [Serve examples](https://tailscale.com/docs/reference/examples/serve), accessed 2026-10-07.
[^services]: [Tailscale Services](https://tailscale.com/docs/features/tailscale-services), accessed 2026-10-07.
[^sharing]: [Device sharing](https://tailscale.com/docs/features/sharing), accessed 2026-10-07.
[^invites]: [Invite any user](https://tailscale.com/docs/features/sharing/how-to/invite-any-user), accessed 2026-10-07.
[^grants]: [Grants syntax](https://tailscale.com/docs/reference/syntax/grants) and [policy syntax](https://tailscale.com/docs/reference/syntax/policy-file), accessed 2026-10-07. No direct network-rule expiry is a schema-based conclusion; posture expiry and app-defined capabilities remain possible.
[^jit]: [JIT overview](https://tailscale.com/docs/features/access-control/just-in-time-access), [expiring posture attributes and Accessbot](https://tailscale.com/docs/features/tailscale-accessbot-jit), and [ConductorOne/Opal integrations](https://tailscale.com/docs/integrations/jit-access), accessed 2026-10-07.
[^pam]: [Tailscale PAM](https://tailscale.com/docs/privileged-access-management/what-is-tailscale-pam), accessed 2026-10-07; beta availability is not a tested onboarding path.
[^funnel]: [Funnel CLI](https://tailscale.com/docs/reference/tailscale-cli/funnel) and [Funnel overview](https://tailscale.com/docs/features/tailscale-funnel), accessed 2026-10-07. The no-flag statement is limited to the published CLI reference; application authentication remains possible.
[^logging]: [Logging overview](https://tailscale.com/docs/features/logging), accessed 2026-10-07.
[^flows]: [Network flow logs](https://tailscale.com/docs/features/logging/network-flow-logs), accessed 2026-10-07; documented for Premium/Enterprise.
[^docker]: [Docker parameters](https://tailscale.com/docs/features/containers/docker/docker-params), accessed 2026-10-07. Docker's `/healthz` checks whether the node has a tailnet IP, not the backend app response.
[^api]: [Tailscale API](https://tailscale.com/docs/reference/tailscale-api), accessed 2026-10-07.
