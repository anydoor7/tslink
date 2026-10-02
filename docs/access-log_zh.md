# 访问历史

TSLink 与 Tailscale 配合使用，是独立项目。本地访问历史帮助应用拥有者确认谁在何时打开了应用，也能看到某人的授权过期后被拒绝的请求。

```sh
tslink access log --app photos --since 24h
tslink access log --who alice@example.com --decision denied --limit 50 --json
tslink access log --since 2030-01-01T00:00:00Z --until 2030-01-02T00:00:00Z --json
```

`--who` 匹配登录名（仅 ASCII 大小写不敏感）、精确节点名或 tag。`--since` 接受 `24h` 等正 Go duration 或 RFC3339 时间；`--until` 接受 RFC3339。上下界都包含端点。事件按时间倒序返回，默认最多 100 条，上限 10,000 条。汇总涵盖全部匹配的保留事件，不受返回条数限制，包括按人和应用计数、允许/拒绝计数、每个应用最后一次允许访问的时间。拒绝不会刷新 last seen。TCP 每个连接产生两条事件；计数不是独立访客数或访问次数。

CLI 使用现有 versioned envelope，`data` 包含 `events`、`summary`、`truncated`。只读 MCP 工具 `access_log` 和 `access_summary` 接受同样的筛选参数，直接返回相应 payload。两者声明 `readOnlyHint`，无需凭据，不联系远端，不创建文件或锁。F6 可把这些命名工具授予 `viewer`。

## 记录内容与隐私

每个到达服务 handler 的 HTTP 请求产生一条 `kind: http`，文件服务走同一条链。TCP 产生 `tcp_open` 和 `tcp_close`，记录连接 ID、时间、身份与双向字节数。schema version 为 1，包含 UTC 时间、应用/服务、方法、状态、读写字节数、毫秒耗时、允许/拒绝、可选拒绝原因和匹配授权（`person` 或 `legacy_allow`）。公共 typed writer 也接受 `mcp`、`guest`，供后续审计生产者使用。

身份来自 WhoIs：账号登录名、节点名和 tags。tagged 节点不记账号登录名。Funnel 公网连接记为 `public`，不保留 IP；同一 listener 上的 tailnet 连接仍保留 WhoIs 身份。判定使用可信的 `ipn.FunnelConn` transport 标记，穿透 TLS/HTTP2 和 TSLink 连接包装；请求头不能选择身份。身份未知的私有调用者只保留 IPv4 /24 或 IPv6 /48 网段。无鉴权请求的身份补充在后台执行，WhoIs 预算为 250 ms，不给请求服务增加身份查询等待。people/ACL 判定则把其新鲜鉴权身份交给记录。

默认路径模式为 `prefix`，只记录第一个非空路径段，例如 `/album/private/item` 变为 `/album`。先解码再分段，把 `%2F`、`%2f`、字面或编码的反斜杠、嵌套转义保守地当分隔符。错误或过深的转义直接隐藏；疑似 token 的段替换为 `[redacted]`。查询串和 fragment 被删除。主要价值是看谁打开了哪个应用。

`full` 是显式开启的完整脱敏路径。**full 可能保存应用自己的 bearer 路径或敏感名称**，TSLink 无法识别任意应用的 capability。TSLink 自己保留的 bearer 路由名 `guest`、`guests`、`invite`、`invites`、`g`、`link`、`links`，以及 `auth`、`token`，在所有模式下都隐藏后续段，包括编码分隔符。后续 guest 路由必须沿用这些保留名，或在发布前扩展 sanitizer。事件没有 header、cookie、body、凭据和完整 URL 字段；生产者只应传入标识符与稳定代码。位于首段的短 opaque capability 不一定具有 token 形状，这类应用应使用 `off`。

```sh
tslink access path photos prefix
tslink access path docs full     # 先确认应用的 URL 设计
tslink access path sensitive off
tslink access path photos inherit
tslink config set access-log-path-mode prefix
```

服务 JSON 使用 `access_log_path_mode`，全局使用 `access_log.path_mode`，接受 `prefix`、`full`、`off`。单服务可覆盖全局 prefix/full；全局 off 是硬关闭。旧键继续可用：全局 `record_path: false` / `config set access-log-path false`，或服务 `access_log_path: false` / `access path <app> false`，均映射为 off，优先于 mode。旧 true、缺失/null 继承新模式，默认 prefix，不会隐式开启 full。`access path <app> inherit` 清除两个服务键；显式模式命令会清除旧服务 boolean。

单应用设置随 registry watcher 生效；全局设置需重启 `serve`。`tslink config set access-log-enabled false` 关闭记录写入，已有历史仍可查询，直到被淘汰。

`acl` 表示 TSLink 的旧 HTTP allow-list；`people` 表示 people 拒绝或 people registry/身份不可用；`expired` 表示匹配人的授权已过期；`limits` 表示上传/读取限制或 TCP 连接数限制；`preserve_host_unavailable` 表示规范主机名不可用。应用自己返回 403 仍是网关允许的请求。Tailnet policy 丢弃的网络包、handler dispatch 前被拒绝的不完整 headers 不能形成 HTTP 请求记录。F2 健康探针直接访问本地 backend，不经过访问链；用户通过网关打开 `/health` 则正常记录。

HTTP 字节数是实际读取的 body 与实际写出的响应，不含 HTTP headers 和 framing，不推测提前拒绝后未读取的上传部分。HTTP upgrade 在 hijack 后计明文 stream 字节，包含升级响应握手。TCP 记录实际 stream 字节。长连接在 HTTP handler 或连接结束时发布完成记录。

## 有界本地存储与健康状态

记录位于 config dir 的 `access-log/YYYY-MM-DD-NNNNNN.jsonl`，沿用 TSLink 工具设置 POSIX 0700/0600。Windows 继承拥有者 config dir 的目录 ACL，TSLink 不声称验证了仅用户可访问的 DACL。目录只允许一个 daemon writer；第二个 writer 不等待，直接拒绝。CLI/MCP 读取不会改权限或修复文件。

| CLI config key | `config.json` 中 `access_log` 下的 key | 默认 | 合法值 |
|---|---|---|---|
| `access-log-enabled` | `enabled` | true | true/false |
| `access-log-path` | `record_path` | inherit | 旧 true/false |
| `access-log-path-mode` | `path_mode` | prefix | prefix/full/off |
| `access-log-retention-days` | `retention_days` | 30 | 1–3650 |
| `access-log-max-bytes` | `max_bytes` | 67108864 | 65536–1073741824 |
| `access-log-queue-size` | `queue_size` | 1024 | 1–65536 |

CLI 空值恢复默认。JSON 数值 key 缺失或为零选默认；未知 key、错误类型、负数与越界值在 load/save 时拒绝。服务键为 `access_log_path_mode` 和兼容的 `access_log_path` boolean。

单段最多 1 MiB，较小 cap 时使用 cap 的四分之一。按最旧段优先物理删除。保留期以 UTC 日历日计算：30 天表示今天及前 29 天。启动、追加前及空闲时每 250 ms 淘汰。cap 限制 JSONL 数据，另有小型 health 文件和 writer lock。

每条 JSON 以单次 newline append 写入并同步文件；macOS/Linux 新段另同步目录。启动时截断末尾不完整记录，再追加。完整行损坏会显式标记不健康并计入丢弃；查询报错，不把损坏静默隐藏。Windows 使用 `File.Sync`，目录同步边界与现有 atomicfile 一致。突然退出可丢失内存队列事件；断电持久性仍受 OS/文件系统限制。

请求服务只放入有界队列；队列满立即丢弃。磁盘失败计入 drops，保留请求服务。daemon 初始化失败后仍保留同一个 writer handle，每次 lifecycle tick（通常 30 秒）重试，恢复后已有 listener 立即使用恢复的 writer，无需重启 listener。故障期间请求继续服务，丢失记录计数明确可见。

`status`（含 `--urls`）和 `doctor` 从独立于 access-log 目录的 `runtime.json` 读取匹配当前 daemon PID/启动时间窗口的健康状态，包含 `current`、`enabled`、`last_write`、`drops`、`size_bytes`、`updated_at`、稳定 `error` 和 `missing_history`（`start`、可选 `end`、`reason`）。没有 end 表示初始化仍不可用；恢复后保留闭合窗口。当前计数每个 daemon 实例重新开始。runtime 快照缺失、损坏或属于旧实例时返回 `access_log_runtime_unavailable`，不会用旧成功 health 文件替代。停止实例的 health 文件只作为历史（`current: false`）。如果 config dir 本身不可读写，就无法证明当前健康状态，消费者报告 unavailable。Doctor 对 drops、缺失窗口及 I/O 失败告警。

store 另每 250 ms 和 drain 时发布历史 health 文件，append/身份补充停滞时也能发布；daemon 原有 health monitor 发布变化的当前状态。快照不能证明崩溃后历史完整。关闭时最多等待 drain 一秒。记录只在本地，不外送，也不接入网络 flow logs。

## 集成 writer 契约

`internal/accesslog.Writer.Record(Event) bool` 是公共生产者接口，不阻塞；false 表示已计数的丢弃。传入 typed metadata、支持的 `kind`、decision 和稳定 reason，不传 payload 或 credential。缺失时间由 store 填 UTC，并执行共同清理。`Store.RecordResolved` 供 daemon 异步补充身份；callback 必须有界，只捕获身份/地址 metadata。集成通过 `Server.AccessLogWriter` 获取已有 daemon writer，只有拥有目录的 daemon 创建 store；只读工具调用 `Query`，不创建 writer。


HTTP 字段保留原意，审计使用 typed 可选 `mcp` 与 `guest` 对象。MCP 包含 `principal`（login、tag 或 F6 的 `local-user:<OS user>`）、`role`（同时兼容旧 `scope`）、调用者 `identity`（`login`、`node`）、`capabilities`（`role`、`apps`、`inventory`、`max_duration`）、`tool`、`apps`、`result`（`status` 为 ok/denied/error，`code` 为稳定代码）、可选 scope deadline、audit ID 和 `phase`（intent/completion，兼容旧 started）。Guest 包含非秘密 `link_id`、`app`、`decision`（allowed/denied）、稳定 `reason`（包含 allow 原因），没有 token 字段。HTTP 原因的清理不覆盖审计原因；生产者 slices 入队前复制。MCP app 筛选和汇总包含操作的所有应用，总计数每个 event 加一次，不按应用数递增。Intent 和 completion receipt 是各自独立的事件。

F6 `mcpaudit.Entry` 的映射不占用 HTTP 或 identity 字段：

| F6 entry | 公共事件 |
|---|---|
| `Kind` | `kind`（`mcp`） |
| `ID`、`Time`、`Principal`、`Role` | `mcp.id`、`time`、`mcp.principal`、`mcp.role` |
| `Identity`、`Phase` | `mcp.identity`（login/node）、`mcp.phase`（intent/completion） |
| 旧 `Who`、`Scope` | `mcp.principal`、`mcp.scope` |
| `Capabilities`、`ScopeExpiresAt` | `mcp.capabilities`、`mcp.scope_expires_at` |
| `Tool`、`Apps` | `mcp.tool`、`mcp.apps` |
| `Result == ok` | `mcp.result = {status: ok, code: ok}` |
| `Result == denied` | `mcp.result = {status: denied, code: denied}` |
| `Result == mcp_scope_denied` | `mcp.result = {status: denied, code: mcp_scope_denied}` |
| `Result == started` | `mcp.phase = intent`（旧 started），`mcp.result = {status: ok, code: started}`（intent，不代表完成成功） |
| 其他 Result 稳定代码 | `mcp.result = {status: error, code: <原代码>}` |

集成约束：**独立 stdio MCP 进程绝不能在 daemon 的目录打开第二个 access-log writer**。daemon 事件传递接口实现前，stdio 审计继续只写 F6 自己有界、带锁的 `mcpaudit.Journal`；access-log 查询不自动导入该 journal。daemon 内生产者可调用 `Server.AccessLogWriter().Record`。未来 transport adapter 必须把 typed events 交给 owning writer，并按非秘密 audit ID 确认交付和去重，之后才可替换 stdio journal。本改动定义公共契约，尚未实现 F6/F10 生产者或 daemon transport。
