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

Recipe catalog 版本 1，官方文档核对日期为 **2026-10-02**。区分宿主机端口映射和容器端口，用 `--proxy 127.0.0.1:<宿主机端口>` 覆盖。运行 `tslink url <name> --wait=30s` 获得精确 URL，再替换片段中的 `YOUR-TAILNET` 及示例域名，不猜测 tailnet 后缀。合并设置片段，不覆盖整个配置文件。容器中看到的来源可能是网桥网关，只信任实际代理 IP，不信任整个私有网段。

`apps share` 和 `add --recipe` 默认给出计划；`--yes` 才应用，`--dry-run` 始终优先。同名服务保留原配置，结果同时提供 `service` 和 `requested`。命令不会安装或配置第三方应用。`--no-daemon-install` 只保存 registry。注册成功不能证明应用已运行或 URL 可访问。

探测使用 macOS lsof、Linux procfs、Windows GetExtendedTcpTable，只向数字 loopback HTTP 地址发 GET：单请求 700 ms，总预算 15 秒，8 个 worker，最多读取 64 KiB。无凭据、环境 HTTP 代理、Cookie 或重定向。仅 HTTPS、仅 LAN、其他网络 namespace、受认证保护或非典型安装可能无法识别。`high` 是应用端点/正文/响应头指纹，`medium` 是 title 匹配，`low` 仅代表不明 HTML 页面；`complete=false` 表示部分结果。置信度不证明认证、健康或版本兼容，输出只含指纹描述，不含响应正文。

私有访问仍受 tailnet policy 和 `--allow` 影响。Funnel 面向整个互联网：验证应用自身认证后才考虑 `--funnel --public`。`never_public` recipe 会拒绝 Funnel；只有所有者有意传入 **`--force-unsafe-public`** 才能覆盖，可能向所有人暴露主机控制、代码执行、GPU 消耗或私密数据。覆盖不能建立应用认证。`app_login` 也不表示关闭登录或未完成初始化的实例可安全公开。

下方健康路径仅为 catalog 数据，供后续监控使用，本功能不实现健康探针。登录页有响应只能说明 HTTP 可用。TSLink 当前请求体限制为 32 MiB、请求读取超时 30 秒，Immich、Nextcloud、Paperless 和聊天界面的大文件或慢速上传可能失败。TSLink 将 Host 改为上游地址，外部域名放在 X-Forwarded-Host，Origin 保留浏览器值；应按下方配置精确域名/origin 及代理信任。Immich 和 Uptime Kuma 需要下方的本地 Nginx 桥接，recipe 使用桥接目标。代理支持 WebSocket upgrade，分享前仍需验证真实应用。

`template list/show/apply` 继续工作：`local-web`、`dev-suite`、`local-ai-suite` 是通用多服务组合，没有 recipe 的安全检查或应用配置建议。需要应用指导时使用 recipe，AI 模板中的 Ollama 也适用。

Agent 可依次调用 `recipe_list`、`apps_detect`、`recipe_plan`、`recipe_apply`；CLI 保留 `schema_version=1` envelope。计划和应用使用相同的 `recipe_id` 与选项。Recipe 注册与接收人的邀请/授权是不同操作。

## Home Assistant

Recipe `home-assistant`；本地端口 **8123, 80**；默认目标 `http://127.0.0.1:8123`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**app_login**。保留应用登录，禁止用 trusted_networks 为代理流量绕过认证。

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

Recipe `immich`；本地端口 **2283**；默认目标 `http://127.0.0.1:12283`；WebSocket：**需要**；建议健康路径 `/api/server/ping`。

安全级别：**app_login**。有应用登录；分享前完成管理员初始化；大文件上传还需网关层支持。

```sh
tslink apps share immich
```

将此 server 段合并进本机 Nginx 配置的 http 段，应用 recipe 前先启动或重载代理。TSLink 连接 12283 上的 loopback 桥接，恢复精确外部 Host，并把 WebSocket upgrade 转给 2283 上的应用。域名换成精确 TSLink URL 域名；隔离两个监听器，不需要绕过 Origin 校验。使用独立域名根路径，移动端 URL 为 https://immich.YOUR-TAILNET.ts.net；桥接从 TSLink forwarded 客户端 IP 提供 X-Real-IP。上传仍受 TSLink 32 MiB/30 秒限制，桥接无法消除网关限制。

```nginx
server {
    listen 127.0.0.1:12283;
    server_name _;
    location / {
        proxy_pass http://127.0.0.1:2283;
        proxy_http_version 1.1;
        proxy_set_header Host immich.YOUR-TAILNET.ts.net;
        proxy_set_header X-Forwarded-Host $http_x_forwarded_host;
        proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;
        proxy_set_header X-Forwarded-For $http_x_forwarded_for;
        proxy_set_header X-Real-IP $http_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

[官方文档 1](https://docs.immich.app/administration/reverse-proxy/) (访问于 2026-10-02). [官方文档 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (访问于 2026-10-02).

## Nextcloud

Recipe `nextcloud`；本地端口 **8080, 80**；默认目标 `http://127.0.0.1:8080`；WebSocket：**核心应用不需要**；建议健康路径 `/status.php`。

安全级别：**app_login**。有账号登录；先完成安装；分享网关不会创建 Nextcloud 账号。

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

```sh
tslink apps share ollama
```

本地 Ollama API 没有认证。保留 loopback 绑定；浏览器直接调用时只允许精确 origin，禁止 *。各平台环境变量设置见 FAQ。

```dotenv
OLLAMA_HOST=127.0.0.1:11434
OLLAMA_ORIGINS=https://open-webui.YOUR-TAILNET.ts.net
```

[官方文档 1](https://docs.ollama.com/faq) (访问于 2026-10-02). [官方文档 2](https://docs.ollama.com/api/authentication) (访问于 2026-10-02).

## ComfyUI

Recipe `comfyui`；本地端口 **8188**；默认目标 `http://127.0.0.1:8188`；WebSocket：**需要**；建议健康路径 `/system_stats`。

安全级别：**never_public**。本地工作流服务禁止 Funnel；只分享给可被授权运行工作流及自定义节点的人。

```sh
tslink apps share comfyui
```

保留 loopback 监听。TSLink 改写 Host 后，ComfyUI 默认 loopback Host/Origin 比较会拒绝外部浏览器请求。--enable-cors-header 必须带精确外部 origin，不能省略参数或填 *。它会选择 CORS middleware 替代默认 Origin 拒绝逻辑，CORS 不等于认证。/ws 必须可用；自定义节点可执行代码，本 recipe 不建立认证层，仍禁止公开。

```sh
python main.py --listen 127.0.0.1 --port 8188 --enable-cors-header https://comfyui.YOUR-TAILNET.ts.net
```

[官方文档 1](https://docs.comfy.org/development/comfyui-server/startup-flags) (访问于 2026-10-02). [官方文档 2](https://github.com/Comfy-Org/ComfyUI/blob/master/server.py) (访问于 2026-10-02).

## Grafana

Recipe `grafana`；本地端口 **3000**；默认目标 `http://127.0.0.1:3000`；WebSocket：**需要**；建议健康路径 `/api/health`。

安全级别：**app_login**。禁用匿名访问后有登录；更换初始管理员密码。

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

```sh
tslink apps share jupyter
```

合并进 jupyter_server_config.py。TSLink 将 Host 改为 loopback 上游，trust_xheaders 不会为 kernel WebSocket origin 检查恢复 Host；allow_origin 必须填精确外部 HTTPS origin。保留随机 token，或用 jupyter server password 设置密码；不能同时清空 token 与密码，不能关闭 XSRF 或使用通配 origin。Notebook 以所有者权限执行代码，只给可信用户；探测无法验证认证，即使有 token 也禁止公开。

```python
c.ServerApp.ip = "127.0.0.1"
c.ServerApp.port = 8888
c.ServerApp.base_url = "/"
c.ServerApp.trust_xheaders = True
c.ServerApp.allow_origin = "https://jupyter.YOUR-TAILNET.ts.net"
c.ServerApp.disable_check_xsrf = False
```

[官方文档 1](https://jupyter-server.readthedocs.io/en/latest/operators/public-server.html) (访问于 2026-10-02). [官方文档 2](https://jupyter-server.readthedocs.io/en/latest/other/full-config.html) (访问于 2026-10-02). [官方文档 3](https://github.com/jupyter-server/jupyter_server/blob/main/jupyter_server/base/websocket.py) (访问于 2026-10-02).

## Uptime Kuma

Recipe `uptime-kuma`；本地端口 **3001**；默认目标 `http://127.0.0.1:13001`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**app_login**。初始化后控制台有登录；公开状态页本来就无需登录。

```sh
tslink apps share uptime-kuma
```

将此 server 段合并进本机 Nginx 配置的 http 段，应用 recipe 前先启动或重载代理。TSLink 连接 13001 上的 loopback 桥接，恢复精确外部 Host，并把 WebSocket upgrade 转给 3001 上的应用。域名换成精确 TSLink URL 域名；隔离两个监听器，不需要绕过 Origin 校验。先在本机建立管理员并保留控制台认证。UPTIME_KUMA_WS_ORIGIN_CHECK 保留默认值，不能 bypass；WebSocket 会比较 Origin 和 Host，trustProxy 分支不使用 X-Forwarded-Host。只公开有意发布的状态页。

```nginx
server {
    listen 127.0.0.1:13001;
    server_name _;
    location / {
        proxy_pass http://127.0.0.1:3001;
        proxy_http_version 1.1;
        proxy_set_header Host uptime-kuma.YOUR-TAILNET.ts.net;
        proxy_set_header X-Forwarded-Host $http_x_forwarded_host;
        proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;
        proxy_set_header X-Forwarded-For $http_x_forwarded_for;
        proxy_set_header X-Real-IP $http_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

[官方文档 1](https://github.com/louislam/uptime-kuma/wiki/Reverse-Proxy) (访问于 2026-10-02). [官方文档 2](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) (访问于 2026-10-02). [官方文档 3](https://github.com/louislam/uptime-kuma/blob/2.0.2/server/uptime-kuma-server.js) (访问于 2026-10-02).

## Paperless-ngx

Recipe `paperless-ngx`；本地端口 **8000**；默认目标 `http://127.0.0.1:8000`；WebSocket：**需要**；建议健康路径 `/accounts/login/`。

安全级别：**app_login**。有账号登录；暴露前必须取消 PAPERLESS_AUTO_LOGIN_USERNAME。

```sh
tslink apps share paperless-ngx
```

写入 docker-compose.env 或 webserver 环境。TSLink 将 Host 改为上游地址，外部域名放在 X-Forwarded-Host；开启 PAPERLESS_USE_X_FORWARD_HOST，只信任实际 TSLink 来源 IP。以下 loopback 值适合宿主机后端；容器网络改变来源时换成实际网桥网关 IP。后端应隔离不可信客户端，Django 选择 forwarded host 本身不受 TRUSTED_PROXIES 限制。PAPERLESS_URL 添加域名、CORS、CSRF 信任，不加尾部斜线；SSL header 恢复外部 HTTPS scheme。保留账号登录，不设置自动登录，密钥由 secret manager 配置。上传仍受 TSLink 32 MiB/30 秒限制。

```dotenv
PAPERLESS_URL=https://paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_ALLOWED_HOSTS=paperless-ngx.YOUR-TAILNET.ts.net
PAPERLESS_USE_X_FORWARD_HOST=true
PAPERLESS_TRUSTED_PROXIES=127.0.0.1,::1
PAPERLESS_PROXY_SSL_HEADER=["HTTP_X_FORWARDED_PROTO","https"]
```

[官方文档 1](https://docs.paperless-ngx.com/configuration/) (访问于 2026-10-02). [官方文档 2](https://github.com/paperless-ngx/paperless-ngx/blob/main/src/paperless/settings/__init__.py) (访问于 2026-10-02).

## Vaultwarden

Recipe `vaultwarden`；本地端口 **80, 8080**；默认目标 `http://127.0.0.1:80`；WebSocket：**需要**；建议健康路径 `/alive`。

安全级别：**never_public**。有密码库登录但内容高度敏感；TSLink 策略禁止公开。

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

```sh
tslink apps share syncthing
```

先在 Settings > GUI 设置用户名及密码并保留 loopback 绑定。TSLink 使用上游 loopback Host，应保留 Host 校验，不需要 insecureSkipHostcheck。以下为 UI 值；分享的是管理界面，不是 22000 文件同步端口。

```text
GUI Listen Address: 127.0.0.1:8384
GUI Authentication User: (choose locally)
GUI Authentication Password: (set locally)
```

[官方文档 1](https://docs.syncthing.net/users/config.html) (访问于 2026-10-02). [官方文档 2](https://docs.syncthing.net/users/reverseproxy.html) (访问于 2026-10-02).

## Portainer

Recipe `portainer`；本地端口 **9443, 9000**；默认目标 `http://127.0.0.1:9000`；WebSocket：**需要**；建议健康路径 `/api/status`。

安全级别：**never_public**。容器管理员可控制主机，禁止 Funnel，尤其禁止公开初始化界面。

```sh
tslink apps share portainer
```

先在本机完成首个管理员设置。默认 9443 为自签名 HTTPS，TSLink 不绕过证书校验。将以下服务片段合并进 compose：明确开启 HTTP，仅将 9000 映射到 loopback。TSLink 改写 Host，因此当前版本 CSRF 可信 origin 要求完整 scheme://host；旧版本接受纯域名，应按所用版本文档核对。保留 CSRF、登录及 setup-token 保护，不分享 agent 8000 端口。控制台需 WebSocket。

```yaml
ports:
  - "127.0.0.1:9000:9000"
command: ["--http-enabled", "--trusted-origins", "https://portainer.YOUR-TAILNET.ts.net"]
```

[官方文档 1](https://docs.portainer.io/start/install-ce/server/docker/linux) (访问于 2026-10-02). [官方文档 2](https://docs.portainer.io/advanced/reverse-proxy/traefik) (访问于 2026-10-02). [官方文档 3](https://docs.portainer.io/start/install/server/setup) (访问于 2026-10-02). [官方文档 4](https://docs.portainer.io/advanced/cli) (访问于 2026-10-02). [官方文档 5](https://github.com/portainer/portainer/blob/develop/api/http/csrf/csrf.go) (访问于 2026-10-02).

## Generic web app

Recipe `generic-web`；本地端口 **8080, 3000, 8000**；默认目标 `http://127.0.0.1:8080`；WebSocket：**需要**；建议健康路径 `/`。

安全级别：**never_public**。无法判定认证，所有者审查具体应用前禁止 Funnel。

```sh
tslink apps share generic-web
```

检查具体应用文档中的外部 URL、可信域名/origin 和代理 IP；没有通用配置文件片段。保留认证并使用独立域名。未知应用保持私有；根路径非空 HTML title 只能说明是通用网页。

```text
External URL: https://generic-web.YOUR-TAILNET.ts.net
Authentication: enabled
Trusted proxy: actual loopback source IP
```

[官方文档 1](https://tailscale.com/docs/features/tailscale-funnel) (访问于 2026-10-02).
