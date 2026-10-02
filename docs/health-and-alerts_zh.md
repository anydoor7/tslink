# 应用健康、到期预警与通知

TSLink daemon 运行时会检查每个 service 的 backend。Web 端口仍在监听，并不
代表应用可用。建议提供一个开销小、只读、能验证必要依赖的 endpoint：

```sh
tslink add photos --proxy localhost:3000 --health-path /ready \
  --health-status-min 200 --health-status-max 299 --health-body ready \
  --health-timeout 5s --health-interval 1m
tslink status --json
tslink list --verbose
tslink doctor --json
```

HTTP 默认 GET `/`、预期状态 200 到 299、不检查正文、5 秒超时、每分钟一次。
路径按 reverse proxy 的规则拼接到 backend base path，保留 backend query。
探针直接访问同一个 backend，不注入 Tailscale 调用者身份，也不跟随重定向。
HTTP/TCP 在任何 I/O 前执行 registry 的 target 安全校验，被拒绝的 target
不会进入调度；enrollment 或 policy 失败的 service 仍检查 backend。
应用有重定向时，应调整预期状态范围或选择适当的 endpoint。需要调用者认证的
应用可能需要独立的本地 readiness endpoint。这些检查不能证明远端访问、ACL
或 Tailscale frontend 的 TLS 可用。

`preserve_host` 服务的探针发送与代理相同的 canonical Host 和
X-Forwarded-Host。可信 runtime 名称缺失或无效时，在 backend I/O 前返回
`health_canonical_host_unavailable`。Doctor 只使用已验证、未过期且与注册
endpoint 匹配的 snapshot 名称。探针校验 request-limit 配置，发送无正文
GET，符合所有合法请求体上限。客户端上传、header 和 idle 限制不约束 backend
响应读取；仍使用 health timeout 和 64 KiB 响应上限。新建 recipe 使用 catalog
健康路径，可显式覆盖；复用已有服务保留原设置。

`--health-body` 检查前 64 KiB 内的子串。超时范围 100ms 到 30s，间隔范围
10s 到 1d，且不得小于超时。HTTP 路径、状态和正文选项只用于 proxy；TCP
仍检查连接，file 在打开前检查类型，打开后重新检查对象类型和文件身份。
Unix 使用非阻塞 open 拒绝替换成 FIFO 的对象；Windows 拒绝 device handle
与无法解析成普通文件/目录的 irregular 对象；已有的受支持目录 junction
和 symlink 按 registry 的 Stat 规则解析，打开前后通过 handle 捕获并比较身份，
操作系统标记为 irregular 的 junction 和 mount point 只要解析到可读目录也能通过检查，
拒绝路径替换。超时和间隔也适用于 TCP/file。无法中断的文件系统
调用会占用 worker slot，但不会阻止其他结果发布或 monitor 退出；跨调度周期
最多保留四个仍在执行的 backend 调用。
路径不能包含 query、fragment 或其他 host。子串应使用非敏感内容，不要把
secret 放进 argv。Registry 的 `health` 与 MCP `add` 接受相同字段：`path`、
`status_min`、`status_max`、`body_contains`、`timeout`、`interval`。
对已有名字重新 add 时，health 设置也会被替换。

检查成功为 `healthy`；连续失败一到两次为 `degraded`，三次为 `down`，成功
即清零失败次数。CLI JSON、MCP `status`/`list` 和 `/events` 的 `health`
包含 `last_checked`、稳定错误码 `last_error` 和 `consecutive_failures`。
没有检查结果为 `unknown`；结果超过两倍间隔加超时，也显示 unknown。Monitor
会在结果越过该边界时唤醒已连接的 `/events` 客户端，即使无法尝试新的检查。节点
授权、URL 就绪与应用健康分别报告。`doctor` 保留 TCP 检查，并额外运行一次
HTTP 业务探针，外部 target 仍要求现有的显式 opt-in。后台最多四个并行 worker，
在首次 registry 同步后启动。每 10 秒调度到期检查，所以实际间隔可能多出最多
10 秒；大量慢 backend 会进一步延长这一间隔。已完成的结果按就绪批次发布，最多用 50ms 收集
一个批次，慢检查继续执行。每批最多提交一次 journal、发布一次 snapshot；
写入失败时保留待保存状态，在后续调度周期重试，即使没有新检查到期。
持久化成功后，状态没有变化时不写盘。

每个 service 同时最多一个真实 backend read。拿到 worker slot 后才开始 I/O
超时；排队另有五秒准入上限。没有容量、排队超时、或上一次 read 尚未退出时，
结果为未尝试（not attempted）：保留失败计数和 `last_checked`。已经准入的
read 超过 I/O 预算才算 checked timeout；无法中断的调用在真正退出前保留 slot，
迟到结果不能发布。

任一池的四个 slot 全部被已超时调用占用时，`alerts.monitor_error` 显示
`health_monitor_saturated`。Journal 每次饱和只记一次 monitor 级
`monitor_saturated`，容量释放后记一次 `monitor_recovered`。Doctor 返回
warning（没有其他 critical 时 exit 64）。池满会暂停该池的新读取，不会把
未尝试的应用检查记成 down。

## 到期预警

每个 service 的 `node_key` 包含到期时间、剩余完整 24 小时天数、来源、预警和
下一步。日期来自 embedded LocalClient；TSLink 不从 service 创建时间或默认
寿命推算。阈值按剩余时长判断：14 天为 `warning_14d`，3 天为 `critical_3d`，
截止时间为 `expired`。节点 14 天预警使 `doctor` 返回 exit 64，3 天或过期
返回 exit 65。`status` 保持信息查询语义。

节点到期每分钟刷新，独立于 backend 的 health interval，LocalAPI 使用另一组
最多四个并行调用。该池同样使用每 service 的 in-flight guard 与独立的
五秒排队上限；准入后享有完整五秒 I/O 预算。未尝试时保留缓存日期和轮询时间，
已经尝试但失败才报告 unknown。替换 service 节点立即使旧日期失效，直到新节点报告日期前
保持 unknown。Sharing 编辑保留 backend 的连续失败计数。

Pinned `tailscale.com v1.102.4` 的字段是
[`PeerStatus.KeyExpiry`，ipn/ipnstate/ipnstate.go:336-338](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnstate/ipnstate.go#L336-L338)。
Self status 在
[`ipn/ipnlocal/local.go:1517-1530`](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnlocal/local.go#L1517-L1530)
调用 `peerStatusFromNode`，后者在
[`local.go:1654-1656`](https://github.com/tailscale/tailscale/blob/v1.102.4/ipn/ipnlocal/local.go#L1654-L1656)
复制非零的 `Node.KeyExpiry`。
[`LocalClient.StatusWithoutPeers`，client/local/local.go:779-788](https://github.com/tailscale/tailscale/blob/v1.102.4/client/local/local.go#L779-L788)
读取 `/localapi/v0/status?peers=false`。没有 self/deadline 或本地读取失败时
为 unknown，包括可能关闭 expiry 的情况；缺少字段不能证明永不过期。

存储凭据保持原有 metadata 语义：用户提供的 API token 到期时间保留来源
`user`，按最大寿命推算的日期保留 `assumed_max`。新增
`credentials.*.early_warning` 使用相同 14 天/3 天阈值，不把估算改称远端
报告。凭据 3 天预警使 doctor 进入 critical。OAuth client secret 的 metadata
可以表示没有计划到期时间，但仍可能被撤销。Legacy auth key 或无法读取/匹配
的 metadata 没有可信日期。SDK 自动续期的临时 OAuth access token 与存储的
client secret 不是同一个到期对象。运行 `tslink doctor --json`，在 admin
console 检查 service 节点，或使用
`printf %s "$TOKEN" | tslink login --api-key-stdin --expires-in <actual-duration>`
更新 token。TSLink 不自动关闭 expiry，也不自行重新授权节点。

[Tailscale key expiry](https://tailscale.com/kb/1028/key-expiry) 说明新 domain
默认 180 天和 admin-console 操作。Enrollment auth key 到期与已经授权的
节点到期是两件事，见 [auth keys](https://tailscale.com/kb/1085/auth-keys)。
来源核查日期为 2026-10-01。

## 可选通知

默认没有外部 notifier。Down、recovery 和到期阈值事件仍记录在
`health-alert-state.json`，并出现在 status JSON、MCP 和 `/events` 的
snapshot/update 中。文件保留最近 100 个事件、去重及 monitor 状态。Status 和 doctor
都以 durable journal 为准，daemon 停止或 runtime snapshot 落后时也一样。
运行中可用的 snapshot 只能补充未能落盘的写入错误，不能覆盖持久化事件、
monitor 状态或当前 journal 错误。
事件流需要现有显式启用的 MCP control plane，并沿用
其身份授权；通知不会新开 listener。

需要通知时，在 TSLink config 目录创建由 owner 控制的 `alerts.json`
（POSIX 权限 0600），重启 daemon。两种通道只能选一个：

```json
{"command":["/absolute/path/to/notify","owner-channel"]}
```

或：

```json
{"webhook":"https://your-notifier.example/owner-hook"}
```

Command 直接执行 argv，不经过 shell；事件 JSON 通过 stdin 与
`TSLINK_ALERT_JSON` 提供，并设置 `TSLINK_ALERT_KIND`、
`TSLINK_ALERT_SERVICE`。Command 继承 daemon 的环境与权限。Webhook
接收 JSON POST，`Content-Type: application/json`，不跟随重定向。投递由一个
worker 执行，队列最多 16 个事件，monitor 不等待 notifier。每次调用的 I/O
deadline 为 10 秒；command 的 pipe 清理额外最多 250ms。Unix command 使用
独立进程组，取消或直接 command 退出后终止该组。Windows 取消时终止直接
command；后代可能继续运行，但输出直接进入 null device，不会占住输出 pipe。
主动离开 Unix 进程组的后代也可能继续运行。取消（包括 daemon 退出）计为发送
失败。Command 输出与 webhook response body 丢弃。诊断中 destination
为 `[redacted]`，通知错误仅有稳定码。Config 和 command 参数仍是磁盘上的
私密输入，后者也存在于所启动进程的 argv 中。

退出时最多等待已取消的 delivery worker 一秒。OS 调用无法中断时，monitor
仍可退出；worker 的迟到结果不能修改 journal 或把已取消事件改成 sent。

所有状态转换进入本地 journal；外部发送全局最多每分钟一次，同一个
service/event-kind/subject 最多每五分钟一次。抑制的发送标记为
`rate_limited`，不排队、不重试。持续 down 不生成新 down 事件；每个日期和
来源的每个 expiry 阈值只生成一次。更新日期后开始新的到期周期。
允许投递的事件先为 `pending`，完成后为 `sent` 或 `failed`；队列已满或
退出时取消的投递记为 `failed`，不会重试。
事件和发送占位先落盘再发送，重启不会重发已提交告警。外部通知是 best effort、
at most once：落盘到发送之间崩溃可能丢失一次通知。状态文件不可写时禁止外发，
报告 `alert_state_write_failed`。Notifier 配置错误时关闭 notifier 并显示
错误码。监测不会重启 backend，应用的 supervision 需要单独配置。
