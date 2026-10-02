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

默认记录路径，永不含查询串或 fragment。已知 guest/invite/auth/token/link bearer 路由、凭据形状及长路径段会隐藏。记录没有 headers（含 User-Agent）、cookies、body、bearer links、tokens 或完整 URL 字段。路径名仍可能包含应用特有的敏感名称，对这类应用可关闭路径记录：

```sh
tslink access path photos false
tslink access path photos inherit
tslink config set access-log-path false
```

全局关闭优先于单应用设置。单应用设置随 registry watcher 生效；全局设置需重启 `serve`。`tslink config set access-log-enabled false` 关闭记录写入，已有历史仍可查询，直到被淘汰。

`acl` 表示 TSLink 的旧 HTTP allow-list；`people` 表示 people 拒绝或 people registry/身份不可用；`expired` 表示匹配人的授权已过期；`limits` 表示上传/读取限制或 TCP 连接数限制；`preserve_host_unavailable` 表示规范主机名不可用。应用自己返回 403 仍是网关允许的请求。Tailnet policy 丢弃的网络包、handler dispatch 前被拒绝的不完整 headers 不能形成 HTTP 请求记录。F2 健康探针直接访问本地 backend，不经过访问链；用户通过网关打开 `/health` 则正常记录。

HTTP 字节数是实际读取的 body 与实际写出的响应，不含 HTTP headers 和 framing，不推测提前拒绝后未读取的上传部分。HTTP upgrade 在 hijack 后计明文 stream 字节，包含升级响应握手。TCP 记录实际 stream 字节。长连接在 HTTP handler 或连接结束时发布完成记录。

## 有界本地存储与健康状态

记录位于 config dir 的 `access-log/YYYY-MM-DD-NNNNNN.jsonl`，沿用 TSLink 工具设置 POSIX 0700/0600。Windows 继承拥有者 config dir 的目录 ACL，TSLink 不声称验证了仅用户可访问的 DACL。目录只允许一个 daemon writer；第二个 writer 不等待，直接拒绝。CLI/MCP 读取不会改权限或修复文件。

| CLI config key | `config.json` 中 `access_log` 下的 key | 默认 | 合法值 |
|---|---|---|---|
| `access-log-enabled` | `enabled` | true | true/false |
| `access-log-path` | `record_path` | true | true/false |
| `access-log-retention-days` | `retention_days` | 30 | 1–3650 |
| `access-log-max-bytes` | `max_bytes` | 67108864 | 65536–1073741824 |
| `access-log-queue-size` | `queue_size` | 1024 | 1–65536 |

CLI 空值恢复默认。JSON 数值 key 缺失或为零选默认；未知 key、错误类型、负数与越界值在 load/save 时拒绝。服务 key `access_log_path` 为 boolean；缺失或 null 则继承。

单段最多 1 MiB，较小 cap 时使用 cap 的四分之一。按最旧段优先物理删除。保留期以 UTC 日历日计算：30 天表示今天及前 29 天。启动、追加前及空闲时每 250 ms 淘汰。cap 限制 JSONL 数据，另有小型 health 文件和 writer lock。

每条 JSON 以单次 newline append 写入并同步文件；macOS/Linux 新段另同步目录。启动时截断末尾不完整记录，再追加。完整行损坏会显式标记不健康并计入丢弃；查询报错，不把损坏静默隐藏。Windows 使用 `File.Sync`，目录同步边界与现有 atomicfile 一致。突然退出可丢失内存队列事件；断电持久性仍受 OS/文件系统限制。

请求服务只放入有界队列；队列满立即丢弃。磁盘失败也计入 drops，保留请求服务。`status`（含 `--urls`）和 `doctor` 的 `access_log` 包含 `enabled`、`last_write`、`drops`、`size_bytes`、`updated_at` 与稳定 `error`。Doctor 对已知 drops 或 I/O 失败告警。独立后台 publisher 即使在 append/身份补充停滞时也发布 drops。健康快照每 250 ms 和 drain 时写入，不能证明已停止的 daemon 正在写。正常重启保留 drops；崩溃可能丢失上一快照后的计数。关闭时最多等待 drain 一秒。不外送记录，也不接入网络 flow logs。

## 集成 writer 契约

`internal/accesslog.Writer.Record(Event) bool` 是公共生产者接口，不阻塞；false 表示已计数的丢弃。传入 typed metadata、支持的 `kind`、decision 和稳定 reason，不传 payload 或 credential。缺失时间由 store 填 UTC，并执行共同清理。`Store.RecordResolved` 供 daemon 异步补充身份；callback 必须有界，只捕获身份/地址 metadata。集成通过 `Server.AccessLogWriter` 获取已有 daemon writer，只有拥有目录的 daemon 创建 store；只读工具调用 `Query`，不创建 writer。
