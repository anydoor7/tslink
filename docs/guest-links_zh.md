# 浏览器访客链接

TSLink 与 Tailscale 配合使用，是独立项目。访客链接让收件人在浏览器中临时打开一个 HTTP 代理应用，无需安装软件或创建 Tailscale 账号。维护者仍需运行 TSLink 节点，并具备 Tailscale HTTPS 和 Funnel 权限。公网入口使用节点自己的 `*.ts.net` 域名，不需要域名或 VPS。

## 创建与发送

先正常启动私有代理应用，再创建访客授权：

```sh
tslink guest create photos --for 3d --label "Aunt May" --public --print-link --json
```

`--public` 明确确认通过 Funnel 提供公网入口，同时强制访客认证。首次开启 gate 必须确认。命令只修改本地配置；watcher 按已有 provisioning 规则开启 Funnel。用 `status` 和 `doctor` 检查真实可用性。命令本身不安装 daemon、不注册节点，也不调用真实 tailnet API。

`--pin` 从隐藏终端输入或 stdin 读取 6–64 位数字 PIN。请单独发送 PIN，不放进 shell 参数。MCP 接受秘密字符串 `pin`；不要记录 MCP 请求正文。

只有 `--print-link` / MCP `print_link:true` 明确披露 bearer 链接。省略时 `link` 为 null，message 不含 URL。私有 registry 只存随机盐加 SHA-256 token hash，之后无法恢复链接；需要发送时，请显式披露并新建授权。list/show 不显示 hash、salt 或 token。披露前必须已有当前精确的节点 URL，否则命令不写授权并报错。先让私有应用上线，不猜域名。

JSON 使用既有 schema-version-1 envelope。create 的 `data` 包含 `grant`、`link`（字符串或 null）、`message`（可发送的简短说明）、`edge_state`。list 返回 `grants`，show/revoke 返回 `grant`。非秘密授权视图包含 id、app、label、authentication、created_at、expires_at、revoked、expired、pin_required、uses、sessions、last_used_at、pin_failures。

共用 F11 期限策略接受 `1h`、`8h`、`24h`、`3d`、`7d`、组合时长和 `until ...`。最短一小时，默认最长七天。维护者可通过[期限配置](durations_zh.md)修改 `durations.public_max`。拒绝 `never`。每次请求检查授权本身的持久 deadline；session 不会延长授权，已观察到的过期会锁存，时钟回退不能恢复。

## 公网 gate 与迁移

只有 Tailscale Funnel 可信连接标记确认的请求走访客路径，HTTP header 不能冒充标记。没有合法 session 的公网请求只得到通用 401，不接触后端。即使全部链接过期或撤销，gate 仍然强制存在。新增授权可延长公网 listener 的 deadline，各授权期限仍互相独立。

拒绝直接接管已有开放 Funnel。先运行不带 `--funnel` 的 `tslink add photos --proxy <原目标>`，保留应用其他配置；等待 `status` 显示私有 listener，再使用 `--public` 创建链接。guest mode 是 sticky 的，普通 `add` 不能悄悄移除 gate。若明确要恢复私有模式，删除并重新注册服务；旧授权保留为历史，但不能放行缺失或没有 gate 的服务。有 guest 历史的服务名不能变为开放 Funnel，直接改 JSON 也会被拒绝；独立公开发布请用另外的服务名并明确确认。重建 gate 不会抹掉撤销 tombstone 或过期锁存。

tailnet 请求继续按 people/allow 授权，包括 `people_scoped` 和撤销 tombstone。访客 session 不能为 tailnet 调用者授权。带 gate 的代理支持人员授权，普通开放 Funnel 仍不支持。TCP 和文件服务不能创建此类链接。`preserve_host` 对两类请求都使用同一可信节点 authority，缺失时在授权后 fail closed。

## 浏览器流程与边界

256-bit base64url token 打开 `/guest/<token>`。无 PIN 时建立服务端 session，立即 303 跳转到 `/`。有 PIN 时显示通用表单，POST 地址为 `/guest/pin`。表单使用 synchronizer CSRF token，绑定服务端 challenge 和 Secure HttpOnly cookie；浏览器提供 Origin 时必须一致。正文上限 1 KiB。challenge 最长五分钟，且不超过授权到期。

session cookie 名为 `__Host-TSLinkGuest`，Secure、HttpOnly、SameSite=Lax、host-only、path `/`。`__Host-TSLinkGuestPIN` 保存待验证 challenge。转发前去掉两者，后端试图设置这些保留名也会被移除。其他应用 cookie 的值和属性保留。公网转发去掉 Referer、`X-TSLink-Guest-*` header，以及保留 query 名 `token`、`pin`、`guest_token`、`guest_pin`。这些名字和 `/guest/` 路由在 gated 服务上由 TSLink 保留。gate 消费 PIN 表单，正文和 bearer 路径均不转发。包括后端响应在内，所有访客响应设置 `Referrer-Policy: no-referrer`。公网访客不会获得 Tailscale 身份 header。

PIN 使用带盐 PBKDF2-HMAC-SHA256，100,000 次迭代。每个授权有持久五次尝试窗口；每个来源跨授权最多十次；窗口均为十五分钟，成功检查也计入。来源使用可信 Funnel 连接的原客户端地址，不信转发 header；缺失来源共用保守的 `unknown` bucket。授权级锁定跨重启保留；来源 bucket、challenge、session 在内存中，重启需重新打开链接，授权仍保留。内存 map 和授权账本各有 4096 项上限。registry 忙、缺失、损坏或不安全时拒绝访问。

## 撤销、检查、审计

```sh
tslink guest list --json
tslink guest show <id> --json
tslink guest revoke <id> --json
tslink access log --app photos --json
```

撤销对该 ID 永久有效。每个后续请求重新检查；撤销前已经授权并在途的请求可能完成，不强行断开已建立的 stream。续期或重新发送时创建新授权。计数记录成功建立的 session 和授权后的应用请求；后端之后失败也计入 `uses`。写入计数必须成功才向后端转发。

MCP 工具为 `guest_create`、`guest_list`、`guest_show`、`guest_revoke`，登记为 owner-only。当前 MCP principal 均为 owner；集成 F6 reduced scopes 时，必须先映射明确的 owner-only 注册表再授权其他角色。本包不包含 portal 集成。

`status` 显示活跃授权的 label/app/deadline，不含秘密。`doctor` 在到期前 24 小时及当前 runtime 无法证明配置 gate 的 Funnel 活跃时告警。F4 收到 `kind=guest`、link ID、app、decision 和稳定 reason：allowed、expired、revoked、bad_pin、rate_limited，以及 invalid_token、session_required、csrf、unavailable。同时保留普通 HTTP completion 事件。token/PIN/cookie 值不进入 access event 或 daemon log。F4 有界异步 writer 在故障时可能丢事件，应检查它的 health。计数是持久本地状态，不代表审计历史完整。

## 选择共享方式

| 需求 | 浏览器访客链接 | 人员授权 |
| --- | --- | --- |
| 收件人操作 | 浏览器，可选输入 PIN | 安装 Tailscale、登录，可能接受设备邀请 |
| 身份 | 任何持有链接/PIN 的人；label 只是维护者备注 | 每次请求验证真实 Tailscale login |
| 范围 | 恰好一个 HTTP 代理应用 | 指定人员的多个受支持应用 |
| 期限 | 有限，默认最长 7d | 深度长期访问；永久 tailnet-member 授权需确认 |
| 公网入口 | Funnel，加必须的访客认证 | tailnet 网络加人员授权 |
| 转发能力 | 可被复制与转发 | 绑定验证后的 login |

短期来访或几天访问用 guest link。反复使用、需要知道是谁访问时用 people grant。label 或 PIN 不验证真实身份。未实现 OIDC；独立的 `authentication:"token"` 字段给未来在维护者机器完成的登录层留出扩展点，不改 Tailscale login 语义。

实现事实核查于 2026-10-02：[Tailscale Funnel](https://tailscale.com/docs/features/tailscale-funnel)、[ListenFunnel](https://pkg.go.dev/tailscale.com/tsnet#Server.ListenFunnel)、[FunnelConn](https://pkg.go.dev/tailscale.com/ipn#FunnelConn)。固定 v1.102.4 源码确认 ListenFunnel 默认也接受 tailnet 连接，FunnelConn.Src 是原始客户端地址。测试只用本地 listener、fake WhoIs 和 fake Funnel 标记，没有使用真实 Funnel 或 tailnet。
