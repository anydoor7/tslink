# 用 TSLink 分享自托管应用

通过 recipe 为 Home Assistant 控制台、家庭媒体库、照片库或团队应用建立独立的私有 Tailscale 域名。应用仍在你的机器上运行；接收人仍需具体应用要求的账号。

```sh
tslink apps list
tslink apps detect --json
tslink apps share jellyfin                       # 预览，不写入
tslink apps share jellyfin --yes                  # 注册并确保 daemon
tslink add --recipe home-assistant --proxy 127.0.0.1:8123  # 等价预览
tslink add family-tv --recipe jellyfin --yes      # 自定义服务名
```

Recipe catalog 版本 2，官方文档核对日期为 **2026-10-02**。区分宿主机端口映射和容器端口，用 `--proxy 127.0.0.1:<宿主机端口>` 覆盖。运行 `tslink url <name> --wait=30s` 获得精确 URL，再替换片段中的 `YOUR-TAILNET` 及示例域名，不猜测 tailnet 后缀。合并设置片段，不覆盖整个配置文件。容器中看到的来源可能是网桥网关，只信任实际代理 IP，不信任整个私有网段。

`apps share` 和 `add --recipe` 默认给出计划；`--yes` 才应用，`--dry-run` 始终优先。同名服务保留原配置，结果同时提供 `service` 和 `requested`。命令不会安装或配置第三方应用。`--no-daemon-install` 只保存 registry。注册成功不能证明应用已运行或 URL 可访问。

`preserve_host=true` 向应用转发接收节点自身的外部 canonical 名称，Host 和 X-Forwarded-Host 均不受客户端 authority 或端口影响。优先取 runtime 第一个证书域名，与 `tslink url` 一致；否则使用 runtime DNS FQDN。Funnel 使用相同规则。名称转为小写，去掉末尾点，不带端口。名称缺失或无效时返回 HTTP 503 `canonical_host_unavailable`，不连接应用。通过别名发来的请求仍会到达 TSLink，并按 canonical 名称转发，不额外返回 421。但浏览器的 Origin 是别名，应用可能因此拒绝请求，所以请使用 `tslink url` 给出的精确地址；重命名后同步更新应用的主机名和 Origin 设置。Origin 不变，保留各应用的精确 Origin 校验。默认模式保留原有的 X-Forwarded-Host 行为。

探测使用 macOS lsof、Linux procfs、Windows GetExtendedTcpTable，只向数字 loopback HTTP 地址发 GET：单请求 700 ms，总预算 15 秒，8 个 worker，最多读取 64 KiB。无凭据、环境 HTTP 代理、Cookie 或重定向。仅 HTTPS、仅 LAN、其他网络 namespace、受认证保护或非典型安装可能无法识别。`high` 是应用端点/正文/响应头指纹，`medium` 是 title 匹配，`low` 仅代表不明 HTML 页面；`complete=false` 表示部分结果。置信度不证明认证、健康或版本兼容，输出只含指纹描述，不含响应正文。

私有访问仍受 tailnet policy 和 `--allow` 影响。Funnel 面向整个互联网：验证应用自身认证后才考虑 `--funnel --public`。`never_public` recipe 会拒绝 Funnel；只有所有者有意传入 **`--force-unsafe-public`** 才能覆盖，可能向所有人暴露主机控制、代码执行、GPU 消耗或私密数据。覆盖不能建立应用认证。`app_login` 也不表示关闭登录或未完成初始化的实例可安全公开。

下方健康路径仅为 catalog 数据，供后续监控使用，本功能不实现健康探针。登录页有响应只能说明 HTTP 可用。TSLink 当前请求体限制为 32 MiB、请求读取超时 30 秒，Immich、Nextcloud、Paperless 和聊天界面的大文件或慢速上传可能失败。Recipes 各自选择下方显示的 Host 策略：大多数转发节点自身的外部 canonical Host；Ollama、Syncthing 为满足本地 Host 防护继续改写；generic-web 未核对前默认改写。普通 add/share 和已有服务、templates 仍默认改写。用 `--preserve-host` 或 `--preserve-host=false` 覆盖 recipe（MCP 为显式布尔 `preserve_host`），省略则使用 recipe 默认值。Preserve 模式的 Host 和 X-Forwarded-Host 均使用 canonical 名称；两种模式均从真实请求重新生成 X-Forwarded-Proto/For，忽略伪造的 forwarded 头；Origin 不变。Immich 和 Uptime Kuma 直接连接原生 HTTP 端口。代理支持 WebSocket upgrade，分享前仍需验证真实应用。

`template list/show/apply` 继续工作：`local-web`、`dev-suite`、`local-ai-suite` 是通用多服务组合，没有 recipe 的安全检查或应用配置建议。需要应用指导时使用 recipe，AI 模板中的 Ollama 也适用。

Agent 可依次调用 `recipe_list`、`apps_detect`、`recipe_plan`、`recipe_apply`；CLI 保留 `schema_version=1` envelope。计划和应用使用相同的 `recipe_id` 与选项。Recipe 注册与接收人的邀请/授权是不同操作。

## Home Assistant

Recipe `home-assistant`；本地端口 **8123, 80**；默认目标 `http://127.0.0.1:8123`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**app_login**。保留应用登录，禁止用 trusted_networks 为代理流量绕过认证。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share home-assistant
```

2026.8 起在 Settings > System > Network 的 HTTP server 中启用 Trust X-Forwarded-For，并只信任实际代理来源 IP。保存后重启，5 分钟内确认。以下 YAML 仅用于 <=2026.7；升级导入后删除 http 块。容器网络可能需要信任网桥网关 IP。

```yaml
http:
  use_x_forwarded_for: true
  trusted_proxies:
    - 127.0.0.1
    - ::1
```

[官方文档 1](https://www.home-assistant.io/integrations/http/) (访问于 2026-10-02).

## Jellyfin

Recipe `jellyfin`；本地端口 **8096, 8920**；默认目标 `http://127.0.0.1:8096`；WebSocket：**需要**；建议健康路径 `/health`。

安全级别：**app_login**。完成初始化后有用户登录；公网暴露前验证家人的账号。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share jellyfin
```

在 Dashboard > Networking 的 Known Proxies 中加入实际代理 IP；独立域名的 Base URL 留空。先在本机完成初始化并保留用户登录。以下为 UI 设置值。

```text
Known Proxies: 127.0.0.1, ::1
Base URL: (empty)
```

[官方文档 1](https://jellyfin.org/docs/general/post-install/networking/reverse-proxy/) (访问于 2026-10-02). [官方文档 2](https://jellyfin.org/docs/general/post-install/networking/) (访问于 2026-10-02).

## Plex

Recipe `plex`；本地端口 **32400**；默认目标 `http://127.0.0.1:32400`；WebSocket：**需要**；建议健康路径 `/identity`。

安全级别：**app_login**。先认领服务器并使用 Plex 账号；不要为 loopback 来源豁免认证。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share plex
```

先认领服务器。在 Settings > Server > Network 的高级选项中填完整 URL，显式使用 :443；免认证网络列表留空。以下为 UI 值。

```text
Custom server access URLs: https://plex.YOUR-TAILNET.ts.net:443
List of IP addresses and networks allowed without auth: (empty)
```

[官方文档 1](https://support.plex.tv/articles/200430283-network/) (访问于 2026-10-02). [官方文档 2](https://support.plex.tv/articles/200890058-authentication-for-local-network-access/) (访问于 2026-10-02). [官方文档 3](https://support.plex.tv/articles/200931138-troubleshooting-remote-access/) (访问于 2026-10-02).

## Immich

Recipe `immich`；本地端口 **2283**；默认目标 `http://127.0.0.1:2283`；WebSocket：**需要**；建议健康路径 `/api/server/ping`。

安全级别：**app_login**。有应用登录；分享前完成管理员初始化；大文件上传还需网关层支持。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share immich
```

使用独立域名的根路径。Recipe 保留外部 Host，直接向原生 HTTP 2283 转发 X-Forwarded-Host/Proto/For。移动客户端服务器 URL 设为 https://immich.YOUR-TAILNET.ts.net，先在本地完成管理员初始化；宿主机映射仅绑定 loopback。TSLink 请求体仍限 32 MiB、请求读取仍限 30 秒，大文件或慢速上传可能失败。客户端 IP 通过 X-Forwarded-For 提供，TSLink 不生成 X-Real-IP。

```yaml
ports:
  - "127.0.0.1:2283:2283"
```

[官方文档 1](https://docs.immich.app/administration/reverse-proxy/) (访问于 2026-10-02). [官方文档 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (访问于 2026-10-02).

## Nextcloud

Recipe `nextcloud`；本地端口 **8080, 80**；默认目标 `http://127.0.0.1:8080`；WebSocket：**核心应用不需要**；建议健康路径 `/status.php`。

安全级别：**app_login**。有账号登录；先完成安装；分享网关不会创建 Nextcloud 账号。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share nextcloud
```

将片段合并进 config/config.php，保留已有可信域名并替换域名；只信任实际代理 IP。根路径部署，Talk/notify-push 另需 WebSocket。仍受 TSLink 32 MiB/30 秒上传限制。

```php
'trusted_domains' => ['localhost', 'nextcloud.YOUR-TAILNET.ts.net'],
 'trusted_proxies' => ['127.0.0.1', '::1'],
 'overwritehost' => 'nextcloud.YOUR-TAILNET.ts.net',
 'overwriteprotocol' => 'https',
 'overwrite.cli.url' => 'https://nextcloud.YOUR-TAILNET.ts.net',
```

[官方文档 1](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/reverse_proxy_configuration.html) (访问于 2026-10-02). [官方文档 2](https://docs.nextcloud.com/server/latest/admin_manual/configuration_server/config_sample_php_parameters.html) (访问于 2026-10-02). [官方文档 3](https://github.com/nextcloud/docker) (访问于 2026-10-02).

## Open WebUI

Recipe `open-webui`；本地端口 **3000, 8080**；默认目标 `http://127.0.0.1:3000`；WebSocket：**需要**；建议健康路径 `/health`。

安全级别：**app_login**。WEBUI_AUTH 开启时有登录；关闭认证的实例及首个管理员注册页面应保持私有。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share open-webui
```

首次启动前设置；已启动时在 Admin > Settings > General 修改持久化 WEBUI_URL。使用精确 origin，保留认证，在本机完成首个管理员注册后关闭注册。不要仅因 TSLink 提供身份头就启用 trusted-header 认证。

```dotenv
WEBUI_URL=https://open-webui.YOUR-TAILNET.ts.net
CORS_ALLOW_ORIGIN=https://open-webui.YOUR-TAILNET.ts.net
WEBUI_AUTH=true
ENABLE_SIGNUP=false
WEBUI_SESSION_COOKIE_SECURE=true
WEBUI_AUTH_COOKIE_SECURE=true
```

[官方文档 1](https://docs.openwebui.com/troubleshooting/connection-error/) (访问于 2026-10-02). [官方文档 2](https://docs.openwebui.com/reference/env-configuration/) (访问于 2026-10-02). [官方文档 3](https://docs.openwebui.com/getting-started/quick-start/) (访问于 2026-10-02).

## Ollama

Recipe `ollama`；本地端口 **11434**；默认目标 `http://127.0.0.1:11434`；WebSocket：**核心应用不需要**；建议健康路径 `/`。

安全级别：**never_public**。本地 API 无认证，禁止 Funnel；请求可消耗 GPU 并调用已配置的云模型。

代理 Host：**改写为上游地址**（`preserve_host=false`）。

```sh
tslink apps share ollama
```

保持 Host 改写：loopback listener 为防 DNS rebinding 拒绝外部 .ts.net Host，即使 OLLAMA_ORIGINS 已允许该 Origin。本地 Ollama API 没有认证。保留 loopback 绑定；浏览器直接调用时只允许精确 origin，禁止 *。各平台环境变量设置见 FAQ。

```dotenv
OLLAMA_HOST=127.0.0.1:11434
OLLAMA_ORIGINS=https://open-webui.YOUR-TAILNET.ts.net
```

[官方文档 1](https://docs.ollama.com/faq) (访问于 2026-10-02). [官方文档 2](https://docs.ollama.com/api/authentication) (访问于 2026-10-02).

## ComfyUI

Recipe `comfyui`；本地端口 **8188**；默认目标 `http://127.0.0.1:8188`；WebSocket：**需要**；建议健康路径 `/system_stats`。

安全级别：**never_public**。本地工作流服务禁止 Funnel；只分享给可被授权运行工作流及自定义节点的人。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share comfyui
```

服务只绑定 loopback。Recipe 保留外部 Host，默认 Host/Origin middleware 可接受同域浏览器请求，无需 --enable-cors-header。保留默认 Origin 和 Sec-Fetch-Site 防护，确认 /ws WebSocket 可用。Custom nodes 可执行代码；此 recipe 始终 never public，不能建立认证层。

```sh
python main.py --listen 127.0.0.1 --port 8188
```

[官方文档 1](https://docs.comfy.org/development/comfyui-server/startup-flags) (访问于 2026-10-02). [官方文档 2](https://github.com/Comfy-Org/ComfyUI/blob/master/server.py) (访问于 2026-10-02).

## Grafana

Recipe `grafana`；本地端口 **3000**；默认目标 `http://127.0.0.1:3000`；WebSocket：**需要**；建议健康路径 `/api/health`。

安全级别：**app_login**。禁用匿名访问后有登录；更换初始管理员密码。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share grafana
```

合并进 grafana.ini（Windows 为 custom.ini）后重启。精确 HTTPS URL 用于链接及 Live origin 校验，上游保留 HTTP；禁用匿名访问并更换初始凭据。

```ini
[server]
protocol = http
http_addr = 127.0.0.1
http_port = 3000
root_url = https://grafana.YOUR-TAILNET.ts.net/
[auth.anonymous]
enabled = false
```

[官方文档 1](https://grafana.com/tutorials/run-grafana-behind-a-proxy/) (访问于 2026-10-02). [官方文档 2](https://grafana.com/docs/grafana/latest/setup-grafana/configure-grafana/) (访问于 2026-10-02).

## Jupyter

Recipe `jupyter`；本地端口 **8888**；默认目标 `http://127.0.0.1:8888`；WebSocket：**需要**；建议健康路径 `/login`。

安全级别：**never_public**。代码执行环境；无 token/密码即无保护，默认禁止 Funnel。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share jupyter
```

合并到 jupyter_server_config.py。Recipe 保留外部 Host，把精确域名加入 local_hostnames，保留防 DNS rebinding 的 Host 检查。同域 WebSocket Origin 检查无需 allow_origin 覆盖；trust_xheaders 恢复 HTTP XSRF 检查所需 HTTPS scheme。保留生成的 token 或通过 jupyter server password 设置密码。不要同时清空 token 和密码、禁用 XSRF、允许所有远程 Host 或使用通配 origin。Notebook 执行所有者权限代码，只给可信用户访问，本 recipe 始终 never public。

```python
c.ServerApp.ip = "127.0.0.1"
c.ServerApp.port = 8888
c.ServerApp.local_hostnames = ["localhost", "jupyter.YOUR-TAILNET.ts.net"]
c.ServerApp.trust_xheaders = True
c.ServerApp.disable_check_xsrf = False
```

[官方文档 1](https://jupyter-server.readthedocs.io/en/latest/operators/public-server.html) (访问于 2026-10-02). [官方文档 2](https://jupyter-server.readthedocs.io/en/latest/other/full-config.html) (访问于 2026-10-02). [官方文档 3](https://github.com/jupyter-server/jupyter_server/blob/main/jupyter_server/base/websocket.py) (访问于 2026-10-02).

## Uptime Kuma

Recipe `uptime-kuma`；本地端口 **3001**；默认目标 `http://127.0.0.1:3001`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**app_login**。初始化后控制台有登录；公开状态页本来就无需登录。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share uptime-kuma
```

Recipe 保留外部 Host，直接连接原生 HTTP 3001；默认 WebSocket 校验可比较浏览器 Origin 和 Host，无需额外代理配置。先在本地完成管理员初始化，保留控制台登录；UPTIME_KUMA_WS_ORIGIN_CHECK 保持默认，不设 bypass。宿主机映射只绑定 loopback，只公开有意公开的状态页。

```yaml
ports:
  - "127.0.0.1:3001:3001"
```

[官方文档 1](https://github.com/louislam/uptime-kuma/wiki/Reverse-Proxy) (访问于 2026-10-02). [官方文档 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (访问于 2026-10-02). [官方文档 3](https://github.com/louislam/uptime-kuma/blob/2.0.2/server/uptime-kuma-server.js) (访问于 2026-10-02).

## Paperless-ngx

Recipe `paperless-ngx`；本地端口 **8000**；默认目标 `http://127.0.0.1:8000`；WebSocket：**需要**；建议健康路径 `/accounts/login/`。

安全级别：**app_login**。有账号登录；暴露前必须取消 PAPERLESS_AUTO_LOGIN_USERNAME。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share paperless-ngx
```

写入 docker-compose.env 或 webserver 环境。外部 Host 保留后，PAPERLESS_URL 同时提供允许的域名和精确 CSRF/CORS origin，无需 forwarded-host 覆盖。SSL header 恢复外部 HTTPS scheme；隔离后端，确保只有 TSLink 能提供此信任头。不要设置自动登录，密钥通过 secret manager 配置。上传仍受 TSLink 32 MiB/30 秒限制。

```sh
PAPERLESS_URL=https://paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_PROXY_SSL_HEADER=["HTTP_X_FORWARDED_PROTO","https"]
```

[官方文档 1](https://docs.paperless-ngx.com/configuration/) (访问于 2026-10-02). [官方文档 2](https://github.com/paperless-ngx/paperless-ngx/blob/main/src/paperless/settings/__init__.py) (访问于 2026-10-02).

## Vaultwarden

Recipe `vaultwarden`；本地端口 **80, 8080**；默认目标 `http://127.0.0.1:80`；WebSocket：**需要**；建议健康路径 `/alive`。

安全级别：**never_public**。有密码库登录但内容高度敏感；TSLink 策略禁止公开。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share vaultwarden
```

DOMAIN 填精确 HTTPS origin。本机完成账号准备后关闭注册，清空可覆盖 SIGNUPS_ALLOWED 的 SIGNUPS_DOMAINS_WHITELIST。新版通知使用主 HTTP 端口的 WebSocket，禁止使用旧 3012 端口。密码库采用仅私有策略。

```dotenv
DOMAIN=https://vaultwarden.YOUR-TAILNET.ts.net
SIGNUPS_ALLOWED=false
SIGNUPS_DOMAINS_WHITELIST=
```

[官方文档 1](https://github.com/dani-garcia/vaultwarden/wiki/Proxy-examples) (访问于 2026-10-02). [官方文档 2](https://github.com/dani-garcia/vaultwarden/wiki/Disable-registration-of-new-users) (访问于 2026-10-02).

## Syncthing GUI

Recipe `syncthing`；本地端口 **8384**；默认目标 `http://127.0.0.1:8384`；WebSocket：**核心应用不需要**；建议健康路径 `/`。

安全级别：**never_public**。GUI 认证可选且能管理文件同步，禁止 Funnel。

代理 Host：**改写为上游地址**（`preserve_host=false`）。

```sh
tslink apps share syncthing
```

保持 Host 改写，使默认 loopback Host 防护继续工作。先在 Settings > GUI 设置用户名及密码并保留 loopback 绑定。TSLink 使用上游 loopback Host，应保留 Host 校验，不需要 insecureSkipHostcheck。以下为 UI 值；分享的是管理界面，不是 22000 文件同步端口。

```text
GUI Listen Address: 127.0.0.1:8384
GUI Authentication User: (choose locally)
GUI Authentication Password: (set locally)
```

[官方文档 1](https://docs.syncthing.net/users/config.html) (访问于 2026-10-02). [官方文档 2](https://docs.syncthing.net/users/reverseproxy.html) (访问于 2026-10-02).

## Portainer

Recipe `portainer`；本地端口 **9443, 9000**；默认目标 `http://127.0.0.1:9000`；WebSocket：**需要**；建议健康路径 `/api/status`。

安全级别：**never_public**。容器管理员可控制主机，禁止 Funnel，尤其禁止公开初始化界面。

代理 Host：**保留外部域名**（`preserve_host=true`）。

```sh
tslink apps share portainer
```

先在本地完成首次管理员设置。默认 9443 使用自签名证书，TSLink 不绕过校验。合并此 compose 片段，显式启用 HTTP，9000 只映射到 loopback。Recipe 保留外部 Host，当前 Portainer CSRF 防护可接受同域浏览器，无需 --trusted-origins。保留 CSRF、登录和 setup-token 防护；不暴露 agent 8000，控制台需要 WebSocket。

```yaml
ports:
  - "127.0.0.1:9000:9000"
command: ["--http-enabled"]
```

[官方文档 1](https://docs.portainer.io/start/install-ce/server/docker/linux) (访问于 2026-10-02). [官方文档 2](https://docs.portainer.io/advanced/reverse-proxy/traefik) (访问于 2026-10-02). [官方文档 3](https://docs.portainer.io/start/install/server/setup) (访问于 2026-10-02). [官方文档 4](https://docs.portainer.io/advanced/cli) (访问于 2026-10-02). [官方文档 5](https://github.com/portainer/portainer/blob/develop/api/http/csrf/csrf.go) (访问于 2026-10-02).

## Generic web app

Recipe `generic-web`；本地端口 **8080, 3000, 8000**；默认目标 `http://127.0.0.1:8080`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**never_public**。无法判定认证，所有者审查具体应用前禁止 Funnel。

代理 Host：**改写为上游地址**（`preserve_host=false`）。

```sh
tslink apps share generic-web
```

应用未知，默认改写 Host；核对应用 Host/Origin 规则后才使用 --preserve-host。检查具体应用文档中的外部 URL、可信域名/origin 和代理 IP；没有通用配置文件片段。保留认证并使用独立域名。未知应用保持私有；根路径非空 HTML title 只能说明是通用网页。

```text
External URL: https://generic-web.YOUR-TAILNET.ts.net
Authentication: enabled
Trusted proxy: actual loopback source IP
```

[官方文档 1](https://tailscale.com/docs/features/tailscale-funnel) (访问于 2026-10-02).
