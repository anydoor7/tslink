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

Recipe catalog version 2 was checked against official documentation on **2026-10-02**. Ports below distinguish host mappings from container ports. Override a mapping with `--proxy 127.0.0.1:<host-port>`. Replace `YOUR-TAILNET` and example hostnames in snippets with the exact URL from `tslink url <name> --wait=30s`; never guess your tailnet suffix. Merge snippets into your existing settings rather than replacing a full configuration file. Containers may see the host through a bridge gateway: trust only the actual proxy source, not an entire private network.

`apps share` and `add --recipe` default to a plan. `--yes` applies; `--dry-run` always wins. Existing names are left unchanged, and the result shows both `service` and `requested`. Neither path installs or configures the third-party app. `--no-daemon-install` saves only the registry. Recipe registration is not proof of a running app or a reachable URL.

`preserve_host=true` forwards the receiving node's own canonical external name in Host and X-Forwarded-Host, independent of the client's authority or port. TSLink prefers its first runtime certificate domain, matching `tslink url`, and falls back to the runtime DNS FQDN; Funnel follows the same rule. Names are lowercase without a terminal dot or port. Missing or invalid runtime names return HTTP 503 `canonical_host_unavailable` before contacting the app. Requests that arrive under an alias still reach TSLink and are forwarded under the canonical name; TSLink does not return 421 for a different authority. The app can still reject such a request because the browser's Origin names the alias, so open the exact URL from `tslink url`, and update the app's hostname and Origin settings after a rename. Origin is unchanged, so retain each app's exact Origin checks. Default mode retains its existing X-Forwarded-Host behavior.

Detection uses macOS lsof, Linux procfs or Windows GetExtendedTcpTable. It sends only GET requests to numeric loopback HTTP listeners, has a 700 ms request timeout, 15 second overall budget, 8 workers and a 64 KiB body limit, and uses no credentials, environment HTTP proxy, cookies or redirects. HTTPS-only, LAN-only, other-network-namespace, protected and unusual installations may be missed. `high` means an app-specific endpoint/body/header fingerprint; `medium` means a title match; `low` is an unidentified generic HTML title. `complete=false` means partial results. Confidence never proves authentication, health or version compatibility. Detection returns fingerprint descriptions, not response bodies.

Private services still depend on tailnet policy and any `--allow` list. Funnel is public internet exposure: use `--funnel --public` only after verifying application authentication. Recipes marked `never_public` refuse it unless the owner deliberately supplies **`--force-unsafe-public`**, which may expose host control, code execution, GPU consumption or private data to everyone. This override does not establish app authentication. A recipe with `app_login` can still be unsafe if its login is disabled or initial setup is unfinished.

New recipe services use the catalog health path as their daemon probe default. Override it with `--health-path` and the other `--health-*` options (MCP: `health`). Reusing an existing service keeps its health configuration. A login page proves HTTP response only, not authenticated readiness. TSLink defaults to 32 MiB request bodies and a 30-second request-body inactivity timeout; large or slow uploads in Immich, Nextcloud, Paperless and chat interfaces can fail. Use the request-limit flags (MCP: `request_limits`) to choose suitable finite bounds; recipes never select unlimited uploads automatically. Recipes choose their own Host policy (shown below). Most forward this node's canonical external Host, while Ollama and Syncthing keep rewriting it to satisfy their local Host guards; generic-web defaults off until checked. Ordinary add/share and all existing services/templates still default to rewriting Host. Override a recipe with `--preserve-host` or `--preserve-host=false` (MCP: explicit boolean `preserve_host`); omitting it uses the recipe default. Preserve mode uses the canonical name for Host and X-Forwarded-Host; both modes regenerate X-Forwarded-Proto/For from the incoming request, ignoring forged forwarded headers; Origin is unchanged. Immich and Uptime Kuma target their native HTTP ports directly. WebSocket upgrades are forwarded; validate the actual application before sharing.

Templates remain available with `template list`, `template show` and `template apply`. They are generic multi-service bundles (`local-web`, `dev-suite`, `local-ai-suite`) and do not provide recipe safety checks or app configuration. Use recipes when application advice is needed, including for Ollama in the AI template.

Agents can use `recipe_list`, `apps_detect`, `recipe_plan` then `recipe_apply`. The tools return structured results; the CLI retains its `schema_version=1` envelope. Use the same `recipe_id` and options in plan/apply. After registration, grant an existing tailnet member access with `tslink people add alice@example.com --apps jellyfin --for 7d`. Recipe services use the same private HTTP people grants, request-time expiry and revocation rules as other proxy services; invitations remain a separate operation. See [people sharing](people.md).

## Home Assistant

Recipe `home-assistant`; local ports **8123, 80**; target `http://127.0.0.1:8123`; WebSockets: **yes**; health recommendation `/`. 8123: Container and older installs; 80: HA OS default since 2026.8. Override the host port when needed.

Safety: **app_login**. Keep Home Assistant login enabled; never use trusted_networks to bypass authentication for proxy traffic.

Proxy Host: **preserved external** (`preserve_host=true`).

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

[Official documentation 1](https://www.home-assistant.io/integrations/http/) (accessed 2026-10-02).

## Jellyfin

Recipe `jellyfin`; local ports **8096, 8920**; target `http://127.0.0.1:8096`; WebSockets: **yes**; health recommendation `/health`. 8096 HTTP; 8920 optional HTTPS, not the recipe target.

Safety: **app_login**. Has user login after setup. Verify every family account before public exposure.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share jellyfin
```

Dashboard > Networking: add the actual TSLink source IP to Known Proxies. Use a dedicated hostname and an empty Base URL. Keep user login enabled; finish initial setup locally. These are UI values, not a config file.

```text
Known Proxies: 127.0.0.1, ::1
Base URL: (empty)
```

[Official documentation 1](https://jellyfin.org/docs/general/post-install/networking/reverse-proxy/) (accessed 2026-10-02). [Official documentation 2](https://jellyfin.org/docs/general/post-install/networking/) (accessed 2026-10-02).

## Plex

Recipe `plex`; local ports **32400**; target `http://127.0.0.1:32400`; WebSockets: **yes**; health recommendation `/identity`.

Safety: **app_login**. Requires a claimed server and Plex accounts. Loopback must not be exempted from authentication.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share plex
```

Claim the server first. Settings > Server > Network > Show Advanced: publish the exact URL including :443 (otherwise Plex may substitute the Remote Access port). Keep the unauthenticated networks list empty. UI values below.

```text
Custom server access URLs: https://plex.YOUR-TAILNET.ts.net:443
List of IP addresses and networks allowed without auth: (empty)
```

[Official documentation 1](https://support.plex.tv/articles/200430283-network/) (accessed 2026-10-02). [Official documentation 2](https://support.plex.tv/articles/200890058-authentication-for-local-network-access/) (accessed 2026-10-02). [Official documentation 3](https://support.plex.tv/articles/200931138-troubleshooting-remote-access/) (accessed 2026-10-02).

## Immich

Recipe `immich`; local ports **2283**; target `http://127.0.0.1:2283`; WebSockets: **yes**; health recommendation `/api/server/ping`. Native HTTP port 2283; use the actual loopback host mapping.

Safety: **app_login**. Has its own login. Finish first-admin setup before sharing; large uploads need gateway changes.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share immich
```

Use the root of a dedicated hostname. The recipe preserves the external Host and forwards X-Forwarded-Host/Proto/For directly to native HTTP port 2283. Set the mobile server URL to https://immich.YOUR-TAILNET.ts.net and finish administrator setup locally. Bind the host mapping to loopback as below. TSLink still caps uploads at 32 MiB and request reads at 30 seconds; large or slow uploads can fail. Client IP is provided in X-Forwarded-For; TSLink does not synthesize X-Real-IP.

```yaml
ports:
  - "127.0.0.1:2283:2283"
```

[Official documentation 1](https://docs.immich.app/administration/reverse-proxy/) (accessed 2026-10-02). [Official documentation 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (accessed 2026-10-02).

## Nextcloud

Recipe `nextcloud`; local ports **8080, 80**; target `http://127.0.0.1:8080`; WebSockets: **no (core app)**; health recommendation `/status.php`. 8080 host port in official Docker example; container Apache uses 80. FPM 9000 is not HTTP.

Safety: **app_login**. Has account login; complete installation first. Sharing the gateway does not create Nextcloud accounts.

Proxy Host: **preserved external** (`preserve_host=true`).

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

[Official documentation 1](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/reverse_proxy_configuration.html) (accessed 2026-10-02). [Official documentation 2](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/config_sample_php_parameters.html) (accessed 2026-10-02). [Official documentation 3](https://github.com/nextcloud/docker) (accessed 2026-10-02).

## Open WebUI

Recipe `open-webui`; local ports **3000, 8080**; target `http://127.0.0.1:3000`; WebSockets: **yes**; health recommendation `/health`. 3000 host port in Docker quick start (maps to 8080); native server commonly 8080.

Safety: **app_login**. Has login when WEBUI_AUTH is enabled. Auth-disabled installs and the first-admin signup screen must stay private.

Proxy Host: **preserved external** (`preserve_host=true`).

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

[Official documentation 1](https://docs.openwebui.com/troubleshooting/connection-error/) (accessed 2026-10-02). [Official documentation 2](https://docs.openwebui.com/reference/env-configuration/) (accessed 2026-10-02). [Official documentation 3](https://docs.openwebui.com/getting-started/quick-start/) (accessed 2026-10-02).

## Ollama

Recipe `ollama`; local ports **11434**; target `http://127.0.0.1:11434`; WebSockets: **no (core app)**; health recommendation `/`.

Safety: **never_public**. No local API auth: never Funnel. Requests can consume GPU resources and invoke configured cloud models.

Proxy Host: **rewritten upstream** (`preserve_host=false`).

```sh
tslink apps share ollama
```

Host preservation stays off: the loopback listener rejects an external .ts.net Host to protect against DNS rebinding, even if OLLAMA_ORIGINS allows that Origin. Local Ollama API has no authentication. Keep binding on loopback. If a browser must call it directly, allow only the exact browser origin, never *. Platform environment-variable setup is in the FAQ.

```dotenv
OLLAMA_HOST=127.0.0.1:11434
OLLAMA_ORIGINS=https://open-webui.YOUR-TAILNET.ts.net
```

[Official documentation 1](https://docs.ollama.com/faq) (accessed 2026-10-02). [Official documentation 2](https://docs.ollama.com/api/authentication) (accessed 2026-10-02).

## ComfyUI

Recipe `comfyui`; local ports **8188**; target `http://127.0.0.1:8188`; WebSockets: **yes**; health recommendation `/system_stats`.

Safety: **never_public**. Local workflow server: never Funnel. Only share with trusted people who may run workflows and custom nodes.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share comfyui
```

Keep the server on loopback. The recipe preserves the external Host, so the default Host/Origin middleware accepts same-origin browser requests; no --enable-cors-header override is needed. Keep the default Origin and Sec-Fetch-Site protections. WebSocket /ws must work. Custom nodes can execute code; this recipe stays never public and does not establish an authentication layer.

```sh
python main.py --listen 127.0.0.1 --port 8188
```

[Official documentation 1](https://docs.comfy.org/development/comfyui-server/startup-flags) (accessed 2026-10-02). [Official documentation 2](https://github.com/Comfy-Org/ComfyUI/blob/master/server.py) (accessed 2026-10-02).

## Grafana

Recipe `grafana`; local ports **3000**; target `http://127.0.0.1:3000`; WebSockets: **yes**; health recommendation `/api/health`.

Safety: **app_login**. Has login when anonymous access is disabled; rotate the initial admin password.

Proxy Host: **preserved external** (`preserve_host=true`).

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

[Official documentation 1](https://grafana.com/tutorials/run-grafana-behind-a-proxy/) (accessed 2026-10-02). [Official documentation 2](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/) (accessed 2026-10-02).

## Jupyter

Recipe `jupyter`; local ports **8888**; target `http://127.0.0.1:8888`; WebSockets: **yes**; health recommendation `/login`.

Safety: **never_public**. Code execution environment; without token/password there is no protection. Never Funnel by default.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share jupyter
```

Merge into jupyter_server_config.py. The recipe preserves the external Host: add only its exact hostname to local_hostnames so the DNS-rebinding Host check stays enabled. Same-host WebSocket Origin checking needs no allow_origin override. trust_xheaders restores HTTPS for HTTP XSRF checks. Keep generated token authentication, or set a password using jupyter server password. Never clear both token and password, disable XSRF checks, allow all remote Hosts or use a wildcard origin. Only trusted users should receive notebook access; this recipe stays never public.

```python
c.ServerApp.ip = "127.0.0.1"
c.ServerApp.port = 8888
c.ServerApp.local_hostnames = ["localhost", "jupyter.YOUR-TAILNET.ts.net"]
c.ServerApp.trust_xheaders = True
c.ServerApp.disable_check_xsrf = False
```

[Official documentation 1](https://jupyter-server.readthedocs.io/en/latest/operators/public-server.html) (accessed 2026-10-02). [Official documentation 2](https://jupyter-server.readthedocs.io/en/latest/other/full-config.html) (accessed 2026-10-02). [Official documentation 3](https://github.com/jupyter-server/jupyter_server/blob/main/jupyter_server/base/websocket.py) (accessed 2026-10-02).

## Uptime Kuma

Recipe `uptime-kuma`; local ports **3001**; target `http://127.0.0.1:3001`; WebSockets: **yes**; health recommendation `/`. Native HTTP port 3001; use the actual loopback host mapping.

Safety: **app_login**. Has dashboard login after setup; public status pages are intentionally unauthenticated.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share uptime-kuma
```

The recipe preserves the external Host and targets native HTTP port 3001. The default WebSocket validator can compare the browser Origin with that Host without extra proxy configuration. Finish administrator setup locally and keep dashboard login on. Keep UPTIME_KUMA_WS_ORIGIN_CHECK at its default, never bypass. Bind the host mapping to loopback. Publish only deliberately public status pages.

```yaml
ports:
  - "127.0.0.1:3001:3001"
```

[Official documentation 1](https://github.com/louislam/uptime-kuma/wiki/Reverse-Proxy) (accessed 2026-10-02). [Official documentation 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (accessed 2026-10-02). [Official documentation 3](https://github.com/louislam/uptime-kuma/blob/2.0.2/server/uptime-kuma-server.js) (accessed 2026-10-02).

## Paperless-ngx

Recipe `paperless-ngx`; local ports **8000**; target `http://127.0.0.1:8000`; WebSockets: **yes**; health recommendation `/accounts/login/`.

Safety: **app_login**. Has account login; PAPERLESS_AUTO_LOGIN_USERNAME must be unset before exposure.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share paperless-ngx
```

Set in docker-compose.env or the webserver environment. With the external Host preserved, PAPERLESS_URL supplies the allowed hostname and exact CSRF/CORS origin; no forwarded-host override is needed. The SSL header restores the external HTTPS scheme. Keep the backend isolated so only TSLink can supply this trusted header. Keep auto-login unset and configure a secret key through your secret manager. Uploads remain subject to TSLink 32 MiB/30s limits.

```sh
PAPERLESS_URL=https://paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_PROXY_SSL_HEADER=["HTTP_X_FORWARDED_PROTO","https"]
```

[Official documentation 1](https://docs.paperless-ngx.com/configuration/) (accessed 2026-10-02). [Official documentation 2](https://github.com/paperless-ngx/paperless-ngx/blob/main/src/paperless/settings/__init__.py) (accessed 2026-10-02).

## Vaultwarden

Recipe `vaultwarden`; local ports **80, 8080**; target `http://127.0.0.1:80`; WebSockets: **yes**; health recommendation `/alive`. Container listens on 80; 8080 is a common owner-selected host mapping, not a universal default.

Safety: **never_public**. Has vault login, but contains high-value secrets. TSLink policy: never public.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share vaultwarden
```

Set DOMAIN to the exact HTTPS origin. Finish account provisioning locally, then disable registration; leave SIGNUPS_DOMAINS_WHITELIST empty because it can override SIGNUPS_ALLOWED. Modern notifications use WebSockets on the main HTTP port; do not register the obsolete 3012 port. This recipe is private-only because it serves password vaults.

```dotenv
DOMAIN=https://vaultwarden.YOUR-TAILNET.ts.net
SIGNUPS_ALLOWED=false
SIGNUPS_DOMAINS_WHITELIST=
```

[Official documentation 1](https://github.com/dani-garcia/vaultwarden/wiki/Proxy-examples) (accessed 2026-10-02). [Official documentation 2](https://github.com/dani-garcia/vaultwarden/wiki/Disable-registration-of-new-users) (accessed 2026-10-02).

## Syncthing GUI

Recipe `syncthing`; local ports **8384**; target `http://127.0.0.1:8384`; WebSockets: **no (core app)**; health recommendation `/`.

Safety: **never_public**. GUI auth is optional and controls file synchronization. Never Funnel.

Proxy Host: **rewritten upstream** (`preserve_host=false`).

```sh
tslink apps share syncthing
```

Host preservation stays off so the default loopback Host guard continues to work. Settings > GUI: set a GUI username and password first and retain loopback binding. TSLink uses an upstream loopback Host, so keep host checking enabled. No insecureSkipHostcheck bypass is required. GUI settings below are UI values. This shares the management GUI, not file-sync port 22000.

```text
GUI Listen Address: 127.0.0.1:8384
GUI Authentication User: (choose locally)
GUI Authentication Password: (set locally)
```

[Official documentation 1](https://docs.syncthing.net/users/config.html) (accessed 2026-10-02). [Official documentation 2](https://docs.syncthing.net/users/reverseproxy.html) (accessed 2026-10-02).

## Portainer

Recipe `portainer`; local ports **9443, 9000**; target `http://127.0.0.1:9000`; WebSockets: **yes**; health recommendation `/api/status`. 9443 HTTPS default; 9000 optional HTTP, explicitly enabled for this recipe.

Safety: **never_public**. Container administrator can control the host. Never Funnel, especially the setup screen.

Proxy Host: **preserved external** (`preserve_host=true`).

```sh
tslink apps share portainer
```

Complete first-admin setup locally before sharing. Default 9443 uses a self-signed certificate which TSLink will not bypass. Merge this service fragment into compose: explicitly enable HTTP and publish 9000 only on loopback. The recipe preserves the external Host, so current Portainer CSRF protection accepts the same-origin browser without --trusted-origins. Keep CSRF, login and setup-token protection enabled. Do not expose agent port 8000. UI consoles require WebSockets.

```yaml
ports:
  - "127.0.0.1:9000:9000"
command: ["--http-enabled"]
```

[Official documentation 1](https://docs.portainer.io/start/install-ce/server/docker/linux) (accessed 2026-10-02). [Official documentation 2](https://docs.portainer.io/advanced/reverse-proxy/traefik) (accessed 2026-10-02). [Official documentation 3](https://docs.portainer.io/start/install/server/setup) (accessed 2026-10-02). [Official documentation 4](https://docs.portainer.io/advanced/cli) (accessed 2026-10-02). [Official documentation 5](https://github.com/portainer/portainer/blob/develop/api/http/csrf/csrf.go) (accessed 2026-10-02).

## Generic web app

Recipe `generic-web`; local ports **8080, 3000, 8000**; target `http://127.0.0.1:8080`; WebSockets: **yes**; health recommendation `/`. Examples only; there is no universal local port.

Safety: **never_public**. Authentication unknown: never Funnel until the owner has reviewed the application.

Proxy Host: **rewritten upstream** (`preserve_host=false`).

```sh
tslink apps share generic-web
```

Host preservation defaults off because the app is unknown; use --preserve-host only after checking its Host/Origin rules. Inspect the application documentation for its exact external URL, allowed hosts/origins and proxy trust settings. There is no universal config snippet. Keep its own authentication enabled and use a dedicated hostname. TSLink policy keeps unknown apps private; GET / with a nonempty HTML title identifies only a generic web response.

```text
External URL: https://generic-web.YOUR-TAILNET.ts.net
Authentication: enabled
Trusted proxy: actual loopback source IP
```

[Official documentation 1](https://tailscale.com/docs/features/tailscale-funnel) (accessed 2026-10-02).
