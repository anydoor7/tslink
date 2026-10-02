# 按人分享应用

先注册应用并完成节点登录，再用接收者在 Tailscale 客户端里使用的**真实登录名**授权：

```sh
tslink people add alice@example.com --apps photos,finance --for 7d
tslink people list --json
tslink people update alice@example.com --apps photos --for 1h
tslink people remove alice@example.com
```

授权变更只改本地；remove 保存本地拒绝后还会尝试清理未接受邀请，不需要存储凭据，也不会安装或启动守护进程。守护进程运行本版本、网络策略允许访问时，已有 tailnet 成员可立即使用授权。输出含可转发给对方的消息；只有当前运行快照能证明地址时才包含准确 URL。否则消息提示拥有者在节点登录完成后用 `tslink status --urls` 取得地址。

`--apps all` 选取**当前已注册的私有 HTTP 代理和文件服务**，排除 TCP 和公开 Funnel，不自动包括以后新增的应用。显式指定 TCP 或 Funnel 时，以 `people_service_unsupported` 原子拒绝。文件服务，包括单文件分享，使用与代理相同的 HTTP WhoIs 校验。

已有有效人员不能重复 add，请用 update。只更新 `--apps` 时保留继续授权应用的期限，新加入的应用期限为 24h；指定 `--for` 会把新期限用于所有选定授权。只更新 `--for` 时保留应用集合。`--for never --ack-never` 明确移除 tailnet 成员期限;访客不允许永久授权。新授权默认 24h。`--until` 设置绝对日期/时间,`--for` 使用[统一时长语法](durations_zh.md)。`extend photos --person alice@example.com --for 36h` 只修改单个应用;已过期须 `--regrant`,撤销人员不能恢复。remove 可重复执行；再次 add 是明确的新授权，须先清理或对账未完成邀请操作。

## Tailnet 外的人：一条拥有者命令、一条消息

```sh
tslink people add alice@example.com --apps photos,finance --for 7d --invite --print-links
```

此命令先保存授权，再为每个应用生成一个单次设备邀请，并合成一条消息。TSLink 请求链接，**Tailscale 不发送邮件**，由你转发消息。对方安装 Tailscale，用指定账号登录，逐个接受应用邀请，保持连接，然后打开应用地址。Tailscale 允许用不同于收件邮箱的账号接受设备邀请；但 TSLink 授权绑定 WhoIs 验证的登录名，其他账号即使拿到链接也会被拒绝。

创建设备邀请需要存储的**用户拥有的 API access token**，OAuth client token 不能创建。使用现有的 stdin 登录方式存储 token，不把凭据放进 argv。没有 token 时，普通 `people add` 仍可授权已有成员；也可在 Tailscale Machines 页面手动生成应用设备的分享链接，再附在无需 token 的消息里。

只有显式指定 `--invite` 才创建邀请。邀请链接是 bearer capability，只在显式 `--print-links` 时输出；否则返回 ID 并提示链接隐藏。可用 `tslink invite list --show-urls` 显式取回隐藏链接。人员注册表和 TSLink 邀请审计日志均不保存链接。

邀请可能部分失败。JSON 返回 `complete: false`、每应用的 `code` 和持久化 `state`，以及成功邀请 ID 和副作用计划。**本地授权仍已保存**，即使外层 envelope 成功也必须读取这些字段。用 `tslink people update <login> --invite` 继续：已完成操作复用 ID，只执行未完成操作。加 `--print-links` 可显式取回现有链接；链接不可用时报告错误，不重新创建。

注册表保存非秘密的人员/应用/节点关联及操作状态（`pending`、`sending`、`unknown`、`complete`、`revoked`、`accepted`、`cancelled`、`target_gone`、`replaced`），从不保存 URL 或 token。POST 前保存 `sending`；崩溃、超时、POST 失败响应或无效响应都须先远端对账。链接模式的设备邀请列表没有收件人信息，因此即使只有一个候选，也不会自动归属。先用 `tslink invite list --json` 检查（`--show-urls` 会显式显示链接），确认应用和邀请，再明确处理：

```sh
# 关联拥有者已核对的现有邀请 ID，不为此应用再次 POST。
tslink people update alice@example.com --invite --reconcile-invite photos=12345
# 已核对没有创建邀请：还要求此设备的邀请列表为空。
tslink people update alice@example.com --invite --reconcile-invite photos=none
# 保持本地拒绝，关联未知结果并清理邀请。
tslink people remove alice@example.com --reconcile-invite photos=12345
```

可为不同应用重复指定 `--reconcile-invite`。`none` 是拥有者明确确认加远端列表检查；API 没有幂等键，也不能证明暂未列出的结果以后不会出现，TSLink **不承诺 exactly-once delivery**。不要用独立 `invite device` 恢复 people 操作，它本来就会创建新邀请。独立的远端工作锁避免本机拥有者进程同时执行同一操作；争用返回 `people_invite_busy`，稍后重试。每批远端工作最多 15 秒，调用者更短的 deadline 优先。

remove 先保存拒绝记录并移除授权，再按已记录的应用节点证明撤销所有未接受邀请。JSON 含 `complete` 和每应用 `cleanup` 状态/错误；人类输出明确说明部分或延后清理。没有 token 仍能本地拒绝，并保留邀请 ID；恢复用户拥有的 token 后重试 remove。已完成清理不重复执行，远端 ID 已不存在视为清理完成。已接受邀请标为 `accepted`，网络分享可能仍存在，须单独在 Tailscale 管理。若创建仍在并发执行，remove 立即报告延后清理；随后重试 remove 清理记录的结果。删除应用不会丢弃其邀请台账。

已记录的 completed 邀请消失或过期时,须由拥有者明确确认替换:

```sh
tslink people update alice@example.com --invite --replace-invite photos=12345 --print-links
```

参数是**已记录的旧 ID**,不是新 ID。TSLink 核对同一个节点的归属并列出邀请;旧 ID 仍存在时(包括已接受)拒绝替换。创建后继前先把旧 attempt 标为 `replaced`,完整保留所有授权和期限。替换不能同时指定 `--apps` 或 `--for`。不同应用可重复指定标志;MCP 使用 `people_update`、`invite: true` 和 `replace_invites: {"photos":"12345"}`。重试同一个旧 ID 会继续或复用其后继,不会再次替换;后继结果未知仍须对账。为兼容,completed 记录上的显式 `--reconcile-invite app=none` 也确认替换,但额外要求此设备的整个邀请列表为空。普通重试不会自动替换。

成功且完整的 devices 列表确认已记录节点及其 hostname 均不存在时,remove 保存终态 `target_gone`,保留旧应用、hostname、node ID、invite ID 和 attempt,此后可重新授权其他应用。失败、截断、格式错误的列表及归属不符仍是未完成,须重试。Terminal attempts 是不可覆盖的历史;同一人员/应用/节点的后继使用递增 `attempt`,省略表示原始 attempt 0。它保留各 attempt 的最终结果,不是每次状态变更的完整事件日志。

缺失证据必须满足 HTTP 200、非空 body 和存在且非 null 的有效数组(设备列表的 `devices`,邀请列表的顶层数组)。204、206、其他 2xx、空/null body 或无效集合都不能证明缺失:不退役旧尝试,不记录终态,不发送替换 POST。这适用于替换、移除清理及显式 `none` 恢复。无 body 的 POST/DELETE 响应保留原有独立处理。

## 为什么保留按应用邀请

| 设计 | 拥有者与接收者操作 | 应用兼容性 | 撤销 |
| --- | --- | --- | --- |
| 每个应用独立节点，合并链接（已实现） | 每人一条拥有者命令和消息；N 个应用仍需 N 次 API 创建、N 次接收者接受 | 保持根 URL、Host、跳转、Cookie 和现有 WebSocket 代理 | 本地 WhoIs 授权拒绝后续请求，即使设备分享仍已接受 |
| 只分享一个 home/gateway 节点 | 每人一次邀请和接受 | 路径前缀需要应用 base-path 配置或重写根路径资源、跳转、Cookie、WebSocket URL；每应用主机名需要接收者 tailnet 可达的 DNS 和证书，仅分享网关不会让其他应用节点自动可达 | 同样需要本地身份校验，并防止绕过网关直连应用 |

合并链接保留现有的每应用网络隔离，也不要求改造任意应用。它消除的是 N×M **拥有者 CLI 操作**，仍有 N×M 底层邀请和接收者接受。只接受一次的网关仍是未来设计，本包不声称已实现。Tailscale 文档说明外部人员只看到被分享的设备，访问共享设备须使用完整 tailnet 域名。

## 强制校验与到期

接受任意有效 UTF-8 登录字符串,仅拒绝空字符串、控制字符和内部空白。CLI、MCP 和 Store 在修改前以 `usage_error` 拒绝无效 UTF-8;无效 WhoIs 登录名为权威拒绝,不能回退到旧 allow。移除首尾 ASCII 空白(space、tab、CR、LF、VT、FF),只把 ASCII A-Z 转为小写;不进行 Unicode 大小写折叠或 Unicode normalization,逐字节精确比较。支持撇号、`!`、`~`、其他标点及非 ASCII 地址。U+212A KELVIN SIGN 与 `k` 不同,U+0130 dotted capital I 与 `i` 不同,相似字符不能取得另一登录名的授权。未作为人员管理的登录名保留旧 allow 回退,登录规则也使用同样的精确比较。Tagged 设备仍是机器身份,不能使用人员授权。

仅为升级兼容,父版本 schema-2 已保存的登录名(即使含旧版允许的内部 Unicode 空白或 C1 控制字符)仍可加载、按原字节匹配和移除。新输入不能创建这些键;兼容路径不折叠或重写已有字节。父版本 JSON writer 在保存前已将无效 UTF-8 替换为 U+FFFD;读取保留磁盘实际保存的 Unicode 身份,不修复或重写它。

`access explain` 和 MCP `access_explain` 显示脱敏的人员范围、授权/拒绝记录数量及已知登录名覆盖旧规则的语义，并指向 `people list --json` 查看详细策略。完全没有人员策略的服务保留旧解释。

首次 people 授权会为应用设置 `people_scoped`，即使撤销所有授权也保留标记。之后未知来访者须有有效授权或显式旧 `--allow` 规则；空旧 allow 列表不再使此应用对所有人开放。显式 allow 用户或 tag 继续可用。但对于已登记的非 tagged 登录名，该人员的应用集合是最终依据，覆盖旧 allow 规则：移出应用、到期或撤销均拒绝。Tagged 设备不能冒充人员。

remove 会删除所有应用中匹配的旧 allow 条目，并保留拒绝记录，也拒绝该账号访问原先无限制的私有 HTTP/文件应用。删除服务会清除其授权，保留其他人员数据；重建同名应用不会恢复旧授权。普通 add 保留 `people_scoped`，拒绝把已按人授权的服务转成公开 Funnel 或 TCP。其他人员及显式 allow 的旧账号继续可用。网络策略仍决定可达性；本功能不修改远端 ACL、不删除已接受的设备分享、不撤销 tailnet 成员资格，也不能限制原始 TCP 或公开 Funnel。

授权保存绝对 UTC `expires_at`。请求时间达到期限即拒绝，无需等待 30 秒周期检查。启动、周期检查及请求都会保存 `expired` 标记。时钟前跳可能提前到期；在期限尚未被观察到时回拨时钟，可能延长实际经过的访问时间。一旦到期被观察并保存，回拨和重启不能恢复，须明确 update `--for` 续期。注册表读失败或到期写失败时拒绝请求。恢复旧配置备份仍可能恢复旧授权，这与其他本地策略文件相同。

逐个 HTTP 请求和 WebSocket upgrade 会校验；已接受的下载、响应流和 WebSocket 连接可以继续完成。撤销不会收回已交付的数据，也不强制关闭已升级连接；撤销或到期后重新连接会失败。

## JSON、MCP 与注册表兼容性

CLI JSON 使用现有 `schema_version: 1` 结果 envelope。`data.person` 含规范化 `login`、`revoked`、`grants`；每个授权含 `app`、可选 `expires_at`/`expired`、`active`、可选准确 `url`。add/update 另含 `invites`、`complete`、`message`、`invite_requirement`；list 返回 `data.people`，包括拒绝记录；人员视图还含非秘密的 `invites` 操作记录。remove 返回 `login`、`removed`、`revoked`、`complete`、`cleanup`；邀请视图含 `state` 和可选候选 `reconcile_ids`，候选只是证据，不能证明收件人归属。

MCP 提供 `people_add`、`people_list`、`people_update`、`people_remove`。变更工具说明须确认人员、应用和期限。add/update 可能缩小旧访问范围，因此是 destructive；续期及邀请非幂等，可选邀请使它们具有 open-world 提示。list 完全只读，包括文件权限；remove 是 destructive、幂等，含可选远端清理，因此有 open-world 提示。update 可只指定 invite 进行重试；update/remove 接受 `reconcile_invites` 应用到 ID（或 `none`）对象，须拥有者明确核对。这里不接受 exit-node、可重复链接或 tailnet 管理角色参数；现有 invite 工具继续使用拥有者配置的 `mcp.allow_elevated_invites` 守卫。

启用人员数据的注册表写入 schema version 2，新增顶层 `people` 和服务字段 `people_scoped`。已有 schema-2 注册表(包括邀请台账前的版本)读取不改字节。人员对象有可选 `invites`;后继记录增加可选 `attempt` 和新终态。不认识字段或状态的旧 reader 会拒绝而非丢弃。Schema version 本身不能协商功能;降级须使用兼容 reader 或另行备份的注册表。旧 version 0/1 可加载且不改变字节，只有服务的普通写入仍保留 version 1，未知字段继续严格拒绝。旧二进制会拒绝 version 2 或未知人员字段，不能静默覆盖丢失数据。降级前先停止新版守护进程，明确决定丢弃人员授权后才恢复另外备份的 version 1 注册表；旧守护进程不能校验这些授权，不能只替换运行中安装的 CLI 二进制。

输入错误、人员/应用不存在及状态冲突,在 CLI 和 MCP 中一致使用 `usage_error`、`not_found`、`conflict`。

导出的 `registry.Person`、`PersonGrant`、`PersonGrantActiveAt` 及只读 `registry.Preflight` 是后续生命周期和审计功能的扩展点。

邀请 API 和设备分享于 2026-10-02 核对，其他来源于 2026-10-01 核对：

- [设备分享](https://tailscale.com/docs/features/sharing)：接收者账号、完整主机名、bearer 链接及网络层撤销。
- [Tailscale API](https://tailscale.com/api)、[当前 OpenAPI](https://api.tailscale.com/api/v2?outputOpenapiSchema=true)：`POST /device/{deviceId}/device-invites` 不支持 OAuth client 创建，email 可选，另有 `multiUse`/`allowExitNode`、设备邀请列表及 `DELETE /device-invites/{deviceInviteId}`；schema 没有客户端幂等键。
- [Trust credentials](https://tailscale.com/docs/reference/trust-credentials)：邀请读取/删除 scope 不代表可创建。
- [Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve)：HTTP 反向代理及来访者身份 header。
