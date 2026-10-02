# 一个地址，找到可用的应用

TSLink works with Tailscale, independent project（与 Tailscale 配合使用的独立项目）。可选的 home portal 给每位访客一个可收藏的地址，页面按其当前 Tailscale 身份的实际授权列出私有 HTTP/file 应用，并显示应用地址、最近健康状态和访问期限。

```sh
tslink portal enable --owner you@example.com
# 可选：指定主机名和额外管理员身份
tslink portal enable --hostname family --owner you@example.com --admins helper@example.com
tslink status --urls
# 只关闭入口页监听器
tslink portal disable
```

默认主机名为 `home`。请使用 **status 实际报告的 portal URL**，例如 `https://home.example.ts.net`；重名时 Tailscale 可能重命名节点。enable/disable 只保存配置，运行中的 daemon 自动应用。daemon 未运行时执行 `tslink serve`。新入口节点可能需要独立的浏览器登录，入口注册流程与应用节点独立进行。daemon ready 会保留仍 pending 的入口登录提示；完成或取消只删除匹配本次注册的提示（daemon PID、service 和 auth URL），保留另一节点的待登录提示。确证运行的 portal 快照作为 status 的授权证据，只有入口而没有应用时也适用，但不增加应用数量。`pending` 或 `starting` 不表示地址已经可用。doctor 和两种 status 输出都包含 `portal.enabled`、`hostname`、`state`、`url` 及启动失败代码；daemon 停止、快照过时或不匹配当前 registry 时不提供 URL。

待登录提示按节点名称保存在同一个原子替换的 `auth-handoff.json` 文档中（schema version 2）；发布或移除一个节点的提示不会改变其他节点。旧的单条记录文件仍可读取，下次发布时迁移。`status --json` 和 `status --urls --json` 的 `pending_logins` 列出当前 daemon 所有未过期的待登录项，每项含 `node`、`auth_url` 和 `expires_at`；人类输出列出每个节点和 URL。兼容字段 `auth_url`、`auth_status` 和 `expires_at` 取最早发布且仍待登录的一项；替换同一节点的提示会将其排到最后。当前快照已确证运行的节点不再列为待登录。旧 daemon PID 的记录不参与当前输出，新 daemon 发布时替换旧 PID 记录。ready 不移除待登录项。

入口使用独立节点和 `portal-nodes/<hostname>` 状态目录，不修改应用注册、target、tags、健康设置或请求限制，也不重启已有应用节点。其他应用启动或 auth-key 出错也不会阻止入口关闭或替换。替换前先取消、等待旧 worker 退出并关闭旧节点。更换主机名会保留之前的登录状态。有凭据时入口使用配置的默认 tag，并设置 ephemeral；无凭据时使用持久节点，以便 daemon 重启后保留登录。disable 保留本地节点状态和 owner/admin 身份，不删除远端设备。

## 哪些人能看到应用？

入口和私有 HTTP/file 应用监听器使用同一个 `AppAccessDecisionAt` 模型（`AppAccessAt` 提供其中的监听器放行结果），分别表达实际执行、目录可见性和 grant 期限。grant 使用 F1 的 `PeopleAccessAt` 和 `PersonGrantActiveAt`：到截止时刻立即结束，已持久化的 expiry latch 继续拒绝，tombstone 优先于旧 allow 规则，撤销的人看不到任何应用。未登记的人沿用 service allow list；未 scoped 且 allow list 为空的应用向 Tailnet 对等节点开放。带 tag 的机器只按显式 tag allow 规则判定，不继承 UserProfile 中人类身份的 grant 或管理员权限。

`--owner` 必须是维护应用的人的实际 Tailscale login，必须明确填写，因为 WhoIs 不告诉入口访客在 Tailscale 控制台的 owner/admin 角色。`--admins` 指定额外的 **TSLink 应用管理员**，这些身份可以打开所有私有 HTTP/file 应用，并在入口看到全部已注册应用。这是一项授权选择，请认真核对身份。disable 保留这些角色，people 的撤销 tombstone 仍优先。角色不绕过 Tailscale 网络策略或应用自身登录。raw TCP 不执行 `allow` 或 WhoIs；registry 拒绝 TCP 的 `allowed_users`。公共 Funnel 跳过私有身份检查。这些服务无法由 TSLink 按人限制，所以卡片**只在 owner/admin 的目录中列出**，作为服务清单，并显示："Anyone who can reach this device can connect; TSLink can't limit it per person."（能到达此设备的人都能连接，TSLink 无法按人限制。）其他访客的目录省略这些卡片，不判断或否定他们的网络/公共访问能力。私有 HTTP 和 file 的卡片严格遵循同一执行判定，包括旧 allow 规则、grant、截止时刻、tag 和 tombstone。

先筛选目录授权，再读取可见项的 runtime 和 health；registry 解析工作量仍随文件大小变化，不承诺恒定时间或已排除远程 timing 侧信道。

页面不输出隐藏应用名称、总数、后端地址、邀请 bearer link 或其他人的期限。HTML 和 `GET /api/apps` 使用同一个筛选结果。无效 service 条目不显示；registry 无法读取或格式损坏时拒绝服务，只显示通用的暂不可用提示。可见应用的 canonical 地址尚未就绪时显示 “Address not ready”。健康状态采用 F2 的 `healthy`、`degraded`、`down`、`unknown` 及 status 相同的新鲜度规则。健康表示后端最近一次探测结果，不证明某位访客的网络路径畅通。

入口每次请求重读 grant；申请列表读取可持久化申请过期/保留状态，受保护的 POST 保存新申请；截止时间立即生效，expiry latch 由已有 app listener 和 daemon lifecycle 路径持久化。如果这些路径尚未写 latch 时系统时钟回退，入口依照 F1 当前期限规则判定。授权不使用缓存，每次重新调用 WhoIs，查询最多等待五秒。

HTTPS 应用提供打开链接。TCP 服务显示与 `tslink url` 一致的 `host:port` 连接地址，并提示使用相应客户端；浏览器不能直接打开原始 TCP 服务。

Funnel 到期按 effective service 类型判定：恢复私有监听器后，即使 registry 尚保留旧 Funnel 标记，也执行同一私有身份规则。handoff 只读取最多 64 KiB 的普通文件；Unix 拒绝 symlink 并使用 nonblocking open，避免 FIFO 阻塞取消流程。

## 发给访客一个地址

入口开启后，`tslink people add/update` 的通俗指导会加入 portal。runtime URL 已确证时请访客收藏这一个地址；登录尚未完成时，指导会让 owner 用 `tslink status --urls` 获取地址。

指导保留本次授权更新之前确证的地址，因此首次 grant 也会包含已经运行的 home 入口链接。

访客需要安装并连接 Tailscale，使用获得 grant 的 login 登录。对 Tailnet 之外的人，必须通过 Tailscale Machines 页面 **同时分享 home 节点和各应用节点**。应用邀请不会自动让入口可达，入口也不发送邀请或修改 ACL。只分享入口不会授予其应用链接的网络访问权。[Tailscale 设备分享](https://tailscale.com/docs/features/sharing) 只允许访问被分享的那台机器，并受网络策略约束。另一 Tailnet 中带 tag 的机器不能使用面向用户的设备分享。

## Agent 访问和安全规则

```sh
curl https://home.example.ts.net/api/apps
```

响应使用 TSLink 标准版本化 envelope：

```json
{"type":"tslink.result","ok":true,"schema_version":1,"command":"portal apps","code":0,"data":{"apps":[{"name":"photos","url":"https://photos.example.ts.net","health":"healthy","expires_at":"2030-01-02T12:00:00Z","expiry":"Access ends January 2, 2030 at 12:00 UTC."}]}}
```

MCP 的 `portal_enable`（必填 `owner`，可选 `hostname`、`admins`、`funnel`）和 `portal_disable` 使用 CLI 的同一实现。`funnel: true`、`tslink portal enable --funnel` 显式返回 `portal_funnel_refused`；持久配置中的 `portal.funnel: true` 也拒绝加载。生产只使用 tsnet `ListenTLS`，没有公开 Funnel 或宿主机网络接口监听器。

页面由服务端渲染，CSS 内嵌，适配手机和系统亮/暗模式，没有脚本、框架、CDN 或外部资源。HTML 全部转义。CSP 只放行内嵌样式的精确 hash，禁止脚本、frame 和外部资源；form 仅允许提交到同一 origin。所有响应含 `Cache-Control: private, no-store`、`nosniff` 和 `no-referrer`。Host 必须匹配可信节点 canonical authority，HTTP absolute-form 请求也检查。Origin 如果存在，必须只有一项且为同一 HTTPS origin；opaque、HTTP 或外部 origin 均拒绝。

GET 和 HEAD 提供目录。`POST /access-requests` 接收最多 8 KiB 的表单，要求 WhoIs 识别的真人 tailnet 成员、匹配的 HTTPS Origin 和绑定身份/host、两小时过期的 CSRF token。表单只列主人显式标记 `requestable` 的应用；隐藏应用以及关闭申请后的旧申请历史均不显示。访客查看自己的申请状态，主人通过 CLI/MCP 一次批准并选择期限，详见[申请文档](requests_zh.md)。独立 HTTP 服务沿用 F8 默认 32 MiB 请求大小上限、10 秒 header 期限、30 秒 body 读取空闲期限、60 秒 keep-alive 空闲超时，以及应用节点相同的 listener/request budget wrapper。入口 registry reader 拒绝符号链接和特殊文件，读取上限为 4 MiB。应用自身登录、浏览器访客链接和多主机发现不在本包范围内。

人员指南支持 `--qr` 和 `--qr-png <file>`，使用入口确证 URL。四步中英手机指南及 bearer link 规则见[人员文档](people_zh.md)。
