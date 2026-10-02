# Share self-hosted apps with TSLink

Use a recipe to share a Home Assistant dashboard, family media library, photo collection or small-team web app through its own private Tailscale hostname. The application stays on your machine. Recipients still need app accounts where the app requires them.

```sh
tslink apps list
tslink apps detect --json
tslink apps share jellyfin                       # preview, no writes
tslink apps share jellyfin --yes                  # register and ensure daemon
tslink add --recipe home-assistant --proxy 127.0.0.1:8123  # equivalent preview
tslink add family-tv --recipe jellyfin --yes      # optional service name
```

Recipe catalog version 1 was checked against official documentation on **2026-10-01**. Ports below distinguish host mappings from container ports. Override a mapping with `--proxy 127.0.0.1:<host-port>`. Replace `YOUR-TAILNET` and example hostnames in snippets with the exact URL from `tslink url <name> --wait=30s`; never guess your tailnet suffix. Merge snippets into your existing settings rather than replacing a full configuration file. Containers may see the host through a bridge gateway: trust only the actual proxy source, not an entire private network.

`apps share` and `add --recipe` default to a plan. `--yes` applies; `--dry-run` always wins. Existing names are left unchanged, and the result shows both `service` and `requested`. Neither path installs or configures the third-party app. `--no-daemon-install` saves only the registry. Recipe registration is not proof of a running app or a reachable URL.

Detection uses macOS lsof, Linux procfs or Windows netstat. It sends only GET requests to numeric loopback HTTP listeners, has a 700 ms request timeout, 15 second overall budget, 8 workers and a 64 KiB body limit, and uses no credentials, environment HTTP proxy, cookies or redirects. HTTPS-only, LAN-only, other-network-namespace, protected and unusual installations may be missed. `high` means an app-specific endpoint/body/header fingerprint; `medium` means a title match; `low` is an unidentified generic HTML title. `complete=false` means partial results. Confidence never proves authentication, health or version compatibility. Detection returns fingerprint descriptions, not response bodies.

Private services still depend on tailnet policy and any `--allow` list. Funnel is public internet exposure: use `--funnel --public` only after verifying application authentication. Recipes marked `never_public` refuse it unless the owner deliberately supplies **`--force-unsafe-public`**, which may expose host control, code execution, GPU consumption or private data to everyone. This override does not establish app authentication. A recipe with `app_login` can still be unsafe if its login is disabled or initial setup is unfinished.

Health paths below are recommendations in the catalog for future health monitoring; recipes do not implement probes. A login page proves HTTP response only, not authenticated readiness. TSLink currently limits request bodies to 32 MiB and request reads to 30 seconds; large or slow uploads in Immich, Nextcloud, Paperless and chat interfaces can fail. WebSocket upgrades are forwarded by the existing reverse proxy; validate the actual application before sharing.

Templates remain available with `template list`, `template show` and `template apply`. They are generic multi-service bundles (`local-web`, `dev-suite`, `local-ai-suite`) and do not provide recipe safety checks or app configuration. Use recipes when application advice is needed, including for Ollama in the AI template.

Agents can use `recipe_list`, `apps_detect`, `recipe_plan` then `recipe_apply`. The tools return structured results; the CLI retains its `schema_version=1` envelope. Use the same `recipe_id` and options in plan/apply. A recipient-sharing workflow is separate from recipe registration.

## Home Assistant

Recipe `home-assistant`; local ports **8123, 80**; target `http://127.0.0.1:8123`; WebSockets: **yes**; health recommendation `/`. 8123: Container and older installs; 80: HA OS default since 2026.8. Override the host port when needed.

Safety: **app_login**. Keep Home Assistant login enabled; never use trusted_networks to bypass authentication for proxy traffic.

```sh
tslink apps share home-assistant
```

2026.8+: Settings > System > Network > HTTP server. Enable Trust X-Forwarded-For and add only the actual proxy source IP to Trusted proxies. Saving restarts HA; confirm within 5 minutes. For <=2026.7 use the YAML below; remove the imported http block after upgrading. Container networking may require a bridge gateway IP instead of loopback.

```yaml
http:
  use_x_forwarded_for: true
  trusted_proxies:
    - 127.0.0.1
    - ::1
```

[Official documentation 1](https://www.home-assistant.io/integrations/http/) (accessed 2026-10-01).

## Jellyfin

Recipe `jellyfin`; local ports **8096, 8920**; target `http://127.0.0.1:8096`; WebSockets: **yes**; health recommendation `/health`. 8096 HTTP; 8920 optional HTTPS, not the recipe target.

Safety: **app_login**. Has user login after setup. Verify every family account before public exposure.

```sh
tslink apps share jellyfin
```

Dashboard > Networking: add the actual TSLink source IP to Known Proxies. Use a dedicated hostname and an empty Base URL. Keep user login enabled; finish initial setup locally. These are UI values, not a config file.

```text
Known Proxies: 127.0.0.1, ::1
Base URL: (empty)
```

[Official documentation 1](https://jellyfin.org/docs/general/post-install/networking/reverse-proxy/) (accessed 2026-10-01). [Official documentation 2](https://jellyfin.org/docs/general/post-install/networking/) (accessed 2026-10-01).

## Plex

Recipe `plex`; local ports **32400**; target `http://127.0.0.1:32400`; WebSockets: **yes**; health recommendation `/identity`.

Safety: **app_login**. Requires a claimed server and Plex accounts. Loopback must not be exempted from authentication.

```sh
tslink apps share plex
```

Claim the server first. Settings > Server > Network > Show Advanced: publish the exact URL including :443 (otherwise Plex may substitute the Remote Access port). Keep the unauthenticated networks list empty. UI values below.

```text
Custom server access URLs: https://plex.YOUR-TAILNET.ts.net:443
List of IP addresses and networks allowed without auth: (empty)
```

[Official documentation 1](https://support.plex.tv/articles/200430283-network/) (accessed 2026-10-01). [Official documentation 2](https://support.plex.tv/articles/200890058-authentication-for-local-network-access/) (accessed 2026-10-01). [Official documentation 3](https://support.plex.tv/articles/200931138-troubleshooting-remote-access/) (accessed 2026-10-01).

## Immich

Recipe `immich`; local ports **2283**; target `http://127.0.0.1:2283`; WebSockets: **yes**; health recommendation `/api/server/ping`.

Safety: **app_login**. Has its own login. Finish first-admin setup before sharing; large uploads need gateway changes.

```sh
tslink apps share immich
```

Use the root of a dedicated hostname, never a subpath. Set the mobile app server URL as below. Official reverse-proxy guidance requires Host, X-Real-IP, X-Forwarded-Proto and X-Forwarded-For. TSLink currently rewrites Host and does not set X-Real-IP; validate clients or use a local compatible proxy in front of Immich. TSLink also caps uploads at 32 MiB and request reads at 30 seconds, so large/slow photo uploads are not supported by this recipe alone.

```text
Mobile app server URL: https://immich.YOUR-TAILNET.ts.net
```

[Official documentation 1](https://docs.immich.app/administration/reverse-proxy/) (accessed 2026-10-01).

## Nextcloud

Recipe `nextcloud`; local ports **8080, 80**; target `http://127.0.0.1:8080`; WebSockets: **no (core app)**; health recommendation `/status.php`. 8080 host port in official Docker example; container Apache uses 80. FPM 9000 is not HTTP.

Safety: **app_login**. Has account login; complete installation first. Sharing the gateway does not create Nextcloud accounts.

```sh
tslink apps share nextcloud
```

Merge these entries into existing config/config.php; keep other trusted_domains and replace the hostname. Trust only the actual proxy source IP. Dedicated hostname uses the root path. Optional Talk/notify-push services have separate WebSocket requirements. TSLink 32 MiB/30s upload limits still apply.

```php
'trusted_domains' => ['localhost', 'nextcloud.YOUR-TAILNET.ts.net'],
 'trusted_proxies' => ['127.0.0.1', '::1'],
 'overwritehost' => 'nextcloud.YOUR-TAILNET.ts.net',
 'overwriteprotocol' => 'https',
 'overwrite.cli.url' => 'https://nextcloud.YOUR-TAILNET.ts.net',
```

[Official documentation 1](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/reverse_proxy_configuration.html) (accessed 2026-10-01). [Official documentation 2](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/config_sample_php_parameters.html) (accessed 2026-10-01). [Official documentation 3](https://github.com/nextcloud/docker) (accessed 2026-10-01).

## Open WebUI

Recipe `open-webui`; local ports **3000, 8080**; target `http://127.0.0.1:3000`; WebSockets: **yes**; health recommendation `/health`. 3000 host port in Docker quick start (maps to 8080); native server commonly 8080.

Safety: **app_login**. Has login when WEBUI_AUTH is enabled. Auth-disabled installs and the first-admin signup screen must stay private.

```sh
tslink apps share open-webui
```

Set before first startup, or update persistent WEBUI_URL in Admin > Settings > General. Use exact origins, keep auth on, finish first-admin signup locally, then close signup. Do not enable trusted-header auth merely because TSLink supplies identity headers.

```dotenv
WEBUI_URL=https://open-webui.YOUR-TAILNET.ts.net
CORS_ALLOW_ORIGIN=https://open-webui.YOUR-TAILNET.ts.net
WEBUI_AUTH=true
ENABLE_SIGNUP=false
WEBUI_SESSION_COOKIE_SECURE=true
WEBUI_AUTH_COOKIE_SECURE=true
```

[Official documentation 1](https://docs.openwebui.com/troubleshooting/connection-error/) (accessed 2026-10-01). [Official documentation 2](https://docs.openwebui.com/reference/env-configuration/) (accessed 2026-10-01). [Official documentation 3](https://docs.openwebui.com/getting-started/quick-start/) (accessed 2026-10-01).

## Ollama

Recipe `ollama`; local ports **11434**; target `http://127.0.0.1:11434`; WebSockets: **no (core app)**; health recommendation `/`.

Safety: **never_public**. No local API auth: never Funnel. Requests can consume GPU resources and invoke configured cloud models.

```sh
tslink apps share ollama
```

Local Ollama API has no authentication. Keep binding on loopback. If a browser must call it directly, allow only the exact browser origin, never *. Platform environment-variable setup is in the FAQ.

```dotenv
OLLAMA_HOST=127.0.0.1:11434
OLLAMA_ORIGINS=https://open-webui.YOUR-TAILNET.ts.net
```

[Official documentation 1](https://docs.ollama.com/faq) (accessed 2026-10-01). [Official documentation 2](https://docs.ollama.com/api/authentication) (accessed 2026-10-01).

## ComfyUI

Recipe `comfyui`; local ports **8188**; target `http://127.0.0.1:8188`; WebSockets: **yes**; health recommendation `/system_stats`.

Safety: **never_public**. Local workflow server: never Funnel. Only share with trusted people who may run workflows and custom nodes.

```sh
tslink apps share comfyui
```

Keep the server on loopback. No reverse-proxy trust setting is required for the basic server; WebSocket /ws must work. Custom nodes can execute code. TSLink classifies the self-hosted server as never public because this recipe does not establish an authentication layer.

```sh
python main.py --listen 127.0.0.1 --port 8188
```

[Official documentation 1](https://docs.comfy.org/development/comfyui-server/startup-flags) (accessed 2026-10-01).

## Grafana

Recipe `grafana`; local ports **3000**; target `http://127.0.0.1:3000`; WebSockets: **yes**; health recommendation `/api/health`.

Safety: **app_login**. Has login when anonymous access is disabled; rotate the initial admin password.

```sh
tslink apps share grafana
```

Merge into grafana.ini (custom.ini on Windows), then restart. Set the exact HTTPS URL for links and Grafana Live origin checks; the upstream stays HTTP. Disable anonymous access and rotate initial credentials.

```ini
[server]
protocol = http
http_addr = 127.0.0.1
http_port = 3000
root_url = https://grafana.YOUR-TAILNET.ts.net/
[auth.anonymous]
enabled = false
```

[Official documentation 1](https://grafana.com/tutorials/run-grafana-behind-a-proxy/) (accessed 2026-10-01). [Official documentation 2](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/) (accessed 2026-10-01).

## Jupyter

Recipe `jupyter`; local ports **8888**; target `http://127.0.0.1:8888`; WebSockets: **yes**; health recommendation `/login`.

Safety: **never_public**. Code execution environment; without token/password there is no protection. Never Funnel by default.

```sh
tslink apps share jupyter
```

Merge into jupyter_server_config.py. Keep generated token authentication, or set a password using jupyter server password. Never clear the token and password. Only trusted users should receive access because notebooks execute code as the owner. This recipe stays never public even when a token is configured; detection cannot verify auth.

```python
c.ServerApp.ip = "127.0.0.1"
c.ServerApp.port = 8888
c.ServerApp.base_url = "/"
c.ServerApp.trust_xheaders = True
```

[Official documentation 1](https://jupyter-server.readthedocs.io/en/latest/operators/public-server.html) (accessed 2026-10-01). [Official documentation 2](https://jupyter-server.readthedocs.io/en/latest/other/full-config.html) (accessed 2026-10-01).

## Uptime Kuma

Recipe `uptime-kuma`; local ports **3001**; target `http://127.0.0.1:3001`; WebSockets: **yes**; health recommendation `/`.

Safety: **app_login**. Has dashboard login after setup; public status pages are intentionally unauthenticated.

```sh
tslink apps share uptime-kuma
```

Finish administrator setup locally and keep dashboard authentication on. Publish only deliberately public status pages. No app-side proxy config is required at a dedicated hostname; WebSocket upgrades must pass. The following is a UI action, not a file setting.

```text
Setup: create administrator locally
Dashboard authentication: enabled
```

[Official documentation 1](https://github.com/louislam/uptime-kuma/wiki/Reverse-Proxy) (accessed 2026-10-01).

## Paperless-ngx

Recipe `paperless-ngx`; local ports **8000**; target `http://127.0.0.1:8000`; WebSockets: **yes**; health recommendation `/accounts/login/`.

Safety: **app_login**. Has account login; PAPERLESS_AUTO_LOGIN_USERNAME must be unset before exposure.

```sh
tslink apps share paperless-ngx
```

Set in docker-compose.env or the webserver environment. PAPERLESS_URL adds host, CORS and CSRF trust; no trailing slash. Keep auto-login unset and configure a secret key through your secret manager. Uploads remain subject to TSLink 32 MiB/30s limits.

```dotenv
PAPERLESS_URL=https://paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_ALLOWED_HOSTS=paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_TRUSTED_PROXIES=127.0.0.1,::1
```

[Official documentation 1](https://docs.paperless-ngx.com/configuration/) (accessed 2026-10-01).

## Vaultwarden

Recipe `vaultwarden`; local ports **80, 8080**; target `http://127.0.0.1:80`; WebSockets: **yes**; health recommendation `/alive`. Container listens on 80; 8080 is a common owner-selected host mapping, not a universal default.

Safety: **never_public**. Has vault login, but contains high-value secrets. TSLink policy: never public.

```sh
tslink apps share vaultwarden
```

Set DOMAIN to the exact HTTPS origin. Finish account provisioning locally, then disable registration; leave SIGNUPS_DOMAINS_WHITELIST empty because it can override SIGNUPS_ALLOWED. Modern notifications use WebSockets on the main HTTP port; do not register the obsolete 3012 port. This recipe is private-only because it serves password vaults.

```dotenv
DOMAIN=https://vaultwarden.YOUR-TAILNET.ts.net
SIGNUPS_ALLOWED=false
SIGNUPS_DOMAINS_WHITELIST=
```

[Official documentation 1](https://github.com/dani-garcia/vaultwarden/wiki/Proxy-examples) (accessed 2026-10-01). [Official documentation 2](https://github.com/dani-garcia/vaultwarden/wiki/Disable-registration-of-new-users) (accessed 2026-10-01).

## Syncthing GUI

Recipe `syncthing`; local ports **8384**; target `http://127.0.0.1:8384`; WebSockets: **no (core app)**; health recommendation `/`.

Safety: **never_public**. GUI auth is optional and controls file synchronization. Never Funnel.

```sh
tslink apps share syncthing
```

Settings > GUI: set a GUI username and password first and retain loopback binding. TSLink uses an upstream loopback Host, so keep host checking enabled. No insecureSkipHostcheck bypass is required. GUI settings below are UI values. This shares the management GUI, not file-sync port 22000.

```text
GUI Listen Address: 127.0.0.1:8384
GUI Authentication User: (choose locally)
GUI Authentication Password: (set locally)
```

[Official documentation 1](https://docs.syncthing.net/users/config.html) (accessed 2026-10-01). [Official documentation 2](https://docs.syncthing.net/users/reverseproxy.html) (accessed 2026-10-01).

## Portainer

Recipe `portainer`; local ports **9443, 9000**; target `http://127.0.0.1:9000`; WebSockets: **yes**; health recommendation `/api/status`. 9443 HTTPS default; 9000 optional HTTP, explicitly enabled for this recipe.

Safety: **never_public**. Container administrator can control the host. Never Funnel, especially the setup screen.

```sh
tslink apps share portainer
```

Complete first-admin setup locally before sharing. Default 9443 uses a self-signed certificate which TSLink will not bypass. For loopback HTTP proxying, explicitly publish legacy port 9000 only on loopback as below, and verify your Portainer version enables its HTTP listener. Do not expose agent port 8000. UI sessions and consoles require WebSockets. Setup-token protection is version-dependent; it does not make a setup screen safe to share.

```yaml
ports:
  - "127.0.0.1:9000:9000"
```

[Official documentation 1](https://docs.portainer.io/start/install-ce/server/docker/linux) (accessed 2026-10-01). [Official documentation 2](https://docs.portainer.io/advanced/reverse-proxy/traefik) (accessed 2026-10-01). [Official documentation 3](https://docs.portainer.io/start/install/server/setup) (accessed 2026-10-01).

## Generic web app

Recipe `generic-web`; local ports **8080, 3000, 8000**; target `http://127.0.0.1:8080`; WebSockets: **yes**; health recommendation `/`. Examples only; there is no universal local port.

Safety: **never_public**. Authentication unknown: never Funnel until the owner has reviewed the application.

```sh
tslink apps share generic-web
```

Inspect the application documentation for its exact external URL, allowed hosts/origins and proxy trust settings. There is no universal config snippet. Keep its own authentication enabled and use a dedicated hostname. TSLink policy keeps unknown apps private; GET / with a nonempty HTML title identifies only a generic web response.

```text
External URL: https://generic-web.YOUR-TAILNET.ts.net
Authentication: enabled
Trusted proxy: actual loopback source IP
```

[Official documentation 1](https://tailscale.com/docs/features/tailscale-funnel) (accessed 2026-10-01).
