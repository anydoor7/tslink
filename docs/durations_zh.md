# 分享与访问时长

TSLink 与 Tailscale 配合工作,是独立项目。所有分享和访问期限统一使用
`internal/duration` 中的语法和策略。

| 形式 | 示例及含义 |
| --- | --- |
| 相对 | `90m`、`36h`、`3d`、`1w`、`1d12h`、`1.5h` |
| 绝对 | `until 2030-06-01`、`until 2030-06-01T18:00`、`until 2030-06-01T18:00:00Z` |
| 永久 | `never`,仅限 tailnet 成员授权,并须 `--ack-never` 或 MCP `ack_never: true` |

组合单位必须唯一且从大到小:`w,d,h,m,s,ms,us,ns`。支持小数,按纳秒精确计算,
拒绝溢出。`1h30m` 有效;`30m1h`、`1h1h`、带正负号或组件之间有空格的值无效。
忽略外层空白。一天始终为经过的 24 小时,一周为 168 小时,跨夏令时也相同。
帮助与 MCP schema 推荐 **1h、8h、24h、3d、7d**,这些不是枚举限制。
旧的有限 Funnel 值,包括 `72h`,保持含义;输出的 `168h0m0s` 也能解析。

只有日期时表示该日期**开始时的本地午夜**。无偏移的时间使用操作者进程的本地时区
(支持的平台可用 `TZ` 设置),包括 MCP 服务进程,不使用接收者时区。
夏令时跳时造成的不存在时间和回拨造成的重复时间都会拒绝,包括半小时变化。
请给出 RFC3339 偏移明确选择瞬间,如 `until 2028-11-05T01:30:00-04:00`
或 `-05:00`。无效闰日以及早于或等于操作时刻的期限会拒绝。
期限仍保存为绝对 UTC 时间戳,以后更改时区不改变期限。

所有新分享和访问期限距操作时刻至少 **1h**。访客和公开 Funnel 默认最长
**7 x 24h**;恰好 7d 有效,7d 加 1s 拒绝。tailnet 成员的有限期限没有额外上限,
但须能被解析器表示。新 people 授权及 Funnel 默认 24h;如果配置上限低于 24h,
须显式提供上限内的期限。

拥有者可在 `config.json` 中配置访客/公开上限,保留其他字段:

```json
{"durations":{"public_max":"14d"}}
```

省略 `durations` 使用 7d。存在时 `public_max` 必须为至少 1h 的相对值。
缺失、空或 null 值、绝对日期、`never`、溢出、错误类型及未知字段在读取和保存时
都会拒绝。无效配置会拒绝期限变更,不静默回退到 7d。配置只影响后续操作,
不追溯改写已有期限。

没有设备邀请历史的 people 登录由拥有者指定为 tailnet 成员。`--invite` 或该人
任意已记录的设备邀请历史使用访客策略,后续 update 和 extend 也相同。
这是保守判定:接受邀请不证明已经成为 tailnet 成员。
访客和所有公开访问都不能 `never`,即使已确认。已有永久状态仍无损读取与保留,
新公开 `never` 请求拒绝。update 省略期限保留旧值,包括历史永久授权;
新增应用默认获得有限的 24h 期限。

已有成员授权首次追加 `--invite` 时,保留期限也须符合访客策略;原期限为永久、过长或剩余不足 1h 时,须显式提供有限 `--for`/`--until`。已有邀请的重试保留原期限。

## 重设期限

```sh
tslink people add alice@example.com --apps photos --for 90m
tslink people update alice@example.com --until 2030-06-01T18:00:00Z
tslink people update alice@example.com --for never --ack-never
tslink extend photos --person alice@example.com --for 36h
tslink extend preview --until 2030-06-01T18:00:00Z
tslink extend photos --person alice@example.com --for 1h --regrant
```

`--for` 与 `--until` 互斥。`--for` 也接受引号包围的 `until ...`。
`extend --person` 选取该人在单个应用上的授权;省略时选取该应用的 Funnel TTL。
相对值设为**操作时刻加时长**,不在旧期限上累加,因此可延长或缩短。
策略边界始终相对操作时刻。
已过期期限(含恰好到期)及已保存的 people 到期锁存都要求 `--regrant`。
该标志只重设到期状态,不能清除人员撤销墓碑。已到期、确认过公开访问且已降级为
私有的 Funnel 可用 `--regrant` 恢复;期限仍在未来、被操作者关闭的 Funnel 不能
由此命令恢复。people `update` 保留 F1 的显式替换/续期语义;单个应用及更严格的
重新授权检查使用 `extend`。不发送任何邀请。

`extend` 始终输出已有 version-1 JSON envelope。数据包括 `service`、可选 `who`、
`audience`、`previous_expires_at`、`expires_at`、`regranted` 和 `changed_at`。
两个期限字段始终存在,`null` 表示永久。错误使用已有稳定代码
(`usage_error`、`conflict`、`not_found`)及失败 envelope。
MCP `extend` 接受 `service`、可选 `who`、`for`/`until` 二选一及可选
`regrant`/`ack_never`,输出数据字段相同。

注册表在现有锁内解析并保存。保存后的 `DurationChange` 是未来访问日志的窄接口,
不依赖访问日志功能,也不新增计时器。执行期限仍使用已有 people 请求时检查和
Funnel 生命周期对账。

## 各标志的语法

| 标志 / MCP 参数 | 语法 |
| --- | --- |
| `add` / `apps share` / `add --recipe --funnel-ttl`; MCP `add`、`share`、`recipe_plan`、`recipe_apply` 的 `funnel_ttl` | 本文期限语法及公开策略 |
| `people add/update --for`、`extend --for`; MCP `for` | 本文期限语法及受众策略 |
| `people add/update --until`、`extend --until`; MCP `until` | 不带 `until ` 前缀的绝对形式 |
| `add/share/url --wait`、MCP `url wait`、MCP `logs since`、`login --expires-in`、`mcp.events_keepalive` | 原操作时长语法:Go duration 加 `d`,保留各自范围;不接受 `until`、`never`,不应用访问期限策略 |
| `--health-interval`、`--health-timeout`、`--request-header-timeout`、`--request-read-timeout`、`--idle-timeout` 及对应配置/MCP 字段 | 原 Go duration 语法及范围 |
| `login --expires-at` | 原凭证元数据语法,仅 RFC3339 |

凭证到期元数据、日志窗口、轮询、健康检查、请求超时及 keepalive 不是分享或访问期限,
语法和语义保持不变。F10 访客链接及 F12 审批可调用 `Policy.Resolve` 或
`Policy.Check`,传入 `Guest`、`Public` 或 `TailnetMember`;本文不实现其工作流。
