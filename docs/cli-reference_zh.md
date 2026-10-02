# CLI 参考

## 按人分享

`tslink people add <login-or-email> --apps photos,finance|all [--for 7d] [--invite] [--print-links]` 授予私有 HTTP/文件访问并生成接收者说明。`all` 选当前私有 HTTP/文件应用，排除 TCP/Funnel。`--for` 使用[统一时长语法](durations_zh.md),`--until` 设置绝对期限。新授权默认 24h;`never` 须 `--ack-never` 且限 tailnet 成员。`--invite` 用用户拥有的 API token 创建每应用单次邀请链接；只有显式 `--print-links` 才输出 bearer 链接。

`tslink people list [--json]` 列人员、应用授权、绝对期限、有效状态和撤销记录。`tslink people update <who> [--apps list|all] [--for duration|never] [--invite] [--print-links]` 至少要求 apps、期限或 invite，省略设置时保留原值（未指定期限的新应用默认 24h）。`tslink people remove <who>` 在所有私有 HTTP/文件应用撤销该人，包括匹配的旧 allow 条目。所有命令使用现有 JSON envelope；邀请部分失败返回 `data.complete: false`，保留本地授权。身份校验、现有 WebSocket 连接、时钟变化及 schema 2 降级规则见[人员分享](people_zh.md)。

人员登录字符串须为有效 UTF-8,仅拒绝空串、控制字符和内部空白。无效 UTF-8 使用 `usage_error`;无效 WhoIs 身份拒绝访问。移除首尾 ASCII space/tab/CR/LF/VT/FF,只把 ASCII A-Z 转为小写,不折叠或规范化 Unicode,逐字节比较。支持标点及非 ASCII 地址,Unicode 相似字符保持不同。`people update --invite --replace-invite app=已记录旧ID` 明确确认远端缺失后的替换,保留授权和期限,不能同时用 `--apps` 或 `--for`;MCP 使用 `replace_invites`。确认节点已删除时以 `target_gone` 完成清理并保留证据。缺失证据必须来自 HTTP 200、非空 body 及存在且非 null 的设备/邀请数组;其他 2xx、空/null 或畸形列表延后清理和恢复,不退役旧记录,不记录终态,不发送替换 POST。update 可只指定 `--invite` 继续未完成操作。update/remove 支持重复的 `--reconcile-invite app=id|none`，须拥有者核对未知 POST 结果。remove 先保存本地拒绝，再用 `complete`/`cleanup` 报告未接受邀请的远端清理；无 token 延后清理。`access explain`/`access_explain` 显示脱敏的人员策略并指向 `people list`。持久化状态及不承诺 exactly-once 的说明见人员指南。

## 命令

| 命令 | 描述 |
|------|------|
| `tslink login` | 存储可选的 Tier 2 API 访问令牌或 OAuth 客户端密钥 |
| `tslink logout` | 清除认证状态 |
| `tslink add <name> --proxy host:port` | 暴露本地 Web 服务 |
| `tslink add <name> --dir /path` | 暴露文件目录 |
| `tslink add <name> --tcp host:port` | 暴露 TCP 服务（数据库、SSH 等） |
| `tslink remove <name>` | 移除已注册的服务，并报告 protected/manual 远端清理指引 |
| `tslink list` | 列出本机已注册的服务 |
| `tslink list --tailnet` | 只读：列出整个 tailnet 中所有带 TSLink 标签的设备，包括其它机器的服务和孤儿节点（需要已存储的 API 凭证） |
| `tslink share <path\|port\|host:port>` | 分享一个本地路径或 Web 端口，并打印它的 tailnet URL |
| `tslink url <name>` | 打印某个服务的准确运行时 URL |
| `tslink cleanup` | 回收过期的 Funnel 暴露和 TSLink 拥有的资源；默认只预览，传 `--dry-run=false` 才实际执行；即使没有本地服务在用，也保留共享的 Funnel ACL grant |
| `tslink serve` | 启动网关（前台） |
| `tslink serve --daemon` | 启动网关（后台） |
| `tslink serve --mcp` | 启动网关，并在专用的仅限 tailnet 节点上提供远程 MCP 控制面（必须配置 `mcp.allow`） |
| `tslink stop` | 停止网关 |
| `tslink status` | 显示网关状态 |
| `tslink status --urls` | 显示 owner-only 服务 URL、暴露模式、allow 摘要、后端和 warning code |
| `tslink doctor` | 诊断凭证、daemon、注册表、runtime snapshot、暴露模式、目标安全性和 Tailscale SSH 开启状态；可能补写缺失的凭证 metadata |
| `tslink access explain <service>` | 解释某个服务的本地访问模型，以及仍属于外部策略/后端认证的部分 |
| `tslink logs` | 查看最近的网关日志 |
| `tslink template list` | 列出内置个人服务模板 |
| `tslink template show <name>` | 预览内置模板 |
| `tslink template apply <name> --yes` | 只添加缺失的模板服务，不覆盖已有服务；不加 `--yes` 或传 `--dry-run` 只预览 |
| `tslink tags list` | 列出所有服务及其标签 |
| `tslink tags pull` | 使用 API 访问令牌从 Tailscale ACL 拉取远端标签；OAuth-only 模式会跳过 |
| `tslink tags add <service> <tag>` | 为服务追加一个标签 |
| `tslink tags set <service> <tag>` | 替换服务的全部标签 |
| `tslink tags set-default <tag>` | 修改新服务的默认标签 |
| `tslink tags delete-remote <tag> --force --manage-acl` | 通过本地安全检查和显式远端写入 opt-in 后从 Tailscale ACL 全局移除 ACL 标签所有者规则 |
| `tslink invite user <email>` | 邀请一位用户加入 tailnet；需要 user-owned API 访问令牌 |
| `tslink invite device <service> <email>` | 把某个 TSLink 拥有的服务设备分享给外部用户；需要确切的节点归属证明 |
| `tslink invite list` | 列出未完成的用户邀请和 TSLink 拥有的设备邀请 |
| `tslink invite revoke <id> --kind <user\|device>` | 撤销一个用户或设备邀请 |
| `tslink invite resend <id> --kind <user\|device>` | 重新发送用户或设备的邮件邀请 |
| `tslink mcp` | 面向 agent 的本地 stdio MCP server；不开网络 listener，不需要 `mcp.allow` |
| `tslink config` | 管理全局配置，子命令为 set、get、list |
| `tslink manifest` | 打印每个命令、flag、退出码和 error code 的机器可读描述 |
| `tslink registry check [path]` | 严格校验一个 `registry.json`，不做任何修改 |
| `tslink install` | 用户登录时自启（macOS LaunchAgent / Linux systemd / Windows Task Scheduler） |
| `tslink uninstall` | 移除自启 |

首次运行时，默认 registry 文件尚不存在是有效的空状态。显式传入不存在的
`registry check <path>` 会报 `not_found`（退出码 5）。registry JSON 语法、字段类型
或尾部数据有误时，会报 `usage_error`（退出码 2），并给出文件路径和修复指引。
`list`、`status` 和 `doctor` 会在健康服务旁显示有误条目；修改 registry 前应先修复或移除它。

### 退出码

| 退出码 | 含义 |
|------|------|
| `0` | 成功 |
| `1` | 一般运行时错误 |
| `2` | 用法、参数或 flag 错误 |
| `3` | 认证或授权错误 |
| `4` | 冲突，例如 daemon 已在运行 |
| `5` | 请求的资源不存在 |
| `64` | 诊断 warning 阈值 |
| `65` | 诊断 critical 阈值 |

### add 命令标志

未使用 `--recipe` 时，对已存在的名字执行 `tslink add` 会替换那个 service：替换只保留这次给出的 flag，没有重复写的 `--allow`、`--tags`、`--funnel` 等都会丢掉。JSON 结果列出 `replaced_fields`，访问权限或 node 身份改变时会给出警告。

| 标志 | 描述 |
|------|------|
| `--proxy host:port` | 反向代理到本地 HTTP 服务 |
| `--preserve-host[=false]` | 仅 proxy：转发节点自身可信的外部 canonical Host；普通 add/share 默认 false，recipe 自带默认值；显式 false 可覆盖 recipe。 |
| `--dir /path` | 文件目录服务 |
| `--tcp host:port` | 原始 TCP 转发 |
| `--dry-run` | 校验并打印服务，不写入注册表 |
| `--ephemeral` | 临时节点，停止后自动从 tailnet 移除 |
| `--tags tag:a,tag:b` | ACL 标签，用于 Tailscale 网络策略 |
| `--allow user@,tag:x` | proxy/file 服务的 HTTP 访问控制；TCP 会拒绝该标志，因为原始 TCP 使用 Tailscale ACL 标签和目标服务自身认证 |
| `--control-url URL` | 服务级控制服务器覆盖，例如 Headscale。用已存储的 Tailscale 凭证铸造的 auth key 只发给 Tailscale 自己的控制服务器；存有这类凭证时，TSLink 以 `credential_control_url_mismatch` 拒绝指向其他控制服务器的 service |
| `--funnel` | 通过 Tailscale Funnel 暴露到公网（仅限 proxy，必须同时传 `--public`） |
| `--public` | 显式确认 `--funnel` 的公网暴露；没有 `--funnel` 时无效 |
| `--funnel-ttl <lifetime>` | 相对值或 `until <日期/时间>`;推荐 1h、8h、24h、3d、7d;最短 1h,默认 24h,默认上限 7d;拒绝 never;需要 `--funnel` |
| `--no-auto-provision` | 关闭该服务的 Funnel policy 自动配置；需要 `--funnel` |
| `--no-daemon-install` | 只保存配置，不安装或启动 daemon |
| `--health-path /ready` | 拼接到 proxy backend base path 的 HTTP 业务探针路径，默认 `/` |
| `--health-status-min N`、`--health-status-max N` | 预期 HTTP 状态范围，默认 200..299，仅 proxy |
| `--health-body text` | 前 64 KiB 内的预期子串，默认不检查，仅 proxy |
| `--health-timeout duration` | worker 准入后的 I/O 超时 100ms..30s，默认 `5s` |
| `--health-interval duration` | 检查间隔 10s..1d 且不小于超时，默认 `1m` |
| `--wait duration` | 等待 URL 或授权 URL；默认 `30s`，`0` 表示不等待 |
| `--json` | 打印版本化的结果 envelope |

应用健康 (`healthy`/`degraded`/`down`/`unknown`)、检查时间和连续失败次数出现在 status、list JSON、`list --verbose`、MCP 与 `/events`。节点 key 和凭据采用 14 天/3 天到期预警，提供下一步并保留 metadata 来源。`doctor` 增加实时 HTTP 业务探针，3 天到期预警为 critical (exit 65)。Owner 通知通过 `alerts.json` 显式启用；事件与重启去重状态默认持久化。详见[健康与通知](health-and-alerts_zh.md)。

HTTP/TCP 探针执行 registry 的 target 安全校验。节点到期独立于 `--health-interval` 刷新，替换节点后旧日期立即失效。每 service 每个池最多一个 read；排队另有五秒上限，未尝试不更新失败计数或检查时间。池被卡住时，`alerts.monitor_error=health_monitor_saturated` 与 monitor 饱和/恢复事件分别报告，doctor 为 warning。就绪结果批量落盘，无变化不写。Status 和 doctor 从 durable journal 读取事件和 monitor 状态，snapshot 仅可补充写盘错误。通知采用有界队列；command 的 deadline 为 10 秒，pipe 清理额外最多 250ms，取消计为失败。

### 应用 recipes

| 命令或标志 | 行为 |
|---|---|
| `tslink apps list` | 版本化 catalog，包含配置片段、安全策略和带访问日期的官方文档 |
| `tslink apps detect` | 无凭据地向 OS TCP listener 的 loopback HTTP 发请求，返回置信度及已有注册 |
| `tslink apps share <id>` | 用建议的名称及目标预览单个 recipe |
| `tslink add [name] --recipe <id>` | 等价预览，可用位置参数自定义名称 |
| `--yes` | 应用计划，保留已有同名服务 |
| `--dry-run` | 只预览，即使同时有 `--yes` |
| `--proxy host:port` | 覆盖 recipe 的 loopback HTTP(S) 宿主机目标 |
| `--name name` | `apps share` 的名称覆盖；`add` 用位置参数 |
| `--force-unsafe-public` | 危险：覆盖 `never_public` 策略；必须有 `--funnel --public`，可能向所有人暴露主机控制或私密数据 |

Recipes 支持 `--allow`、`--tags`、`--ephemeral`、`--control-url`、已有 Funnel 确认/TTL/自动配置标志及 `--no-daemon-install`。`add --recipe` 拒绝 `--dir`、`--tcp`、`--wait`；应用后用 `tslink url` 轮询。无 `--recipe` 的普通 `add` 保留替换行为，拒绝 recipe 专用标志。

计划/应用 JSON data 包含 `recipe`、`requested`、`service`、`action`、`dry_run`、`applied`、`warnings`、`next`。只有此次创建服务才返回 `applied=true`；`skip_existing` 展示保留的实际配置。探测包含 `listeners`、`matches`、`complete`、`warnings`，部分扫描返回 `complete=false`。新建 recipe 服务默认使用 catalog 健康路径；`apps share`、`add --recipe` 支持 `--health-*` 与 request-limit 覆盖，MCP `recipe_plan`/`recipe_apply` 支持 `health`、`request_limits`。复用保留已有配置，包括 people scope 和授权。

MCP 工具为 `recipe_list`、只读 `apps_detect`、`recipe_plan`、`recipe_apply`；计划/应用接收 `recipe_id`、可选 `name`/`target`、字符串 `allow`/`tags` 及上述 flag 的 snake_case 参数。先 plan 后 apply。已有通用 `template` 命令及 `template_list/plan/apply` 工具继续工作。见[应用设置与限制](apps_zh.md)。

`share <port|host:port>` 也支持 `--preserve-host`（默认 false）；文件/目录 share 拒绝 true。启用后，Host 和 X-Forwarded-Host 均使用节点自身的外部 canonical DNS 名称，不使用客户端 authority。优先取 runtime 的第一个证书域名，否则取节点 DNS FQDN，与分享的 HTTPS URL 一致，Funnel 也采用此规则。名称转为小写，去掉末尾点，不带端口。名称缺失或无效时返回 HTTP 503 `canonical_host_unavailable`，不请求后端。客户端别名及其他 authority 均按 canonical 名称转发，不额外返回 421。默认模式保留上游 Host 改写及原有的传入 authority X-Forwarded-Host 行为；两种模式的 X-Forwarded-Proto/For 均来自真实请求，Origin 不变。复用时 Host 策略不同会报冲突。已有服务及通用 templates 保持上游 Host 改写。Registry 的 `preserve_host` 是可选 proxy 布尔字段，缺省为 false；recipe 可通过 `--preserve-host=false` 或 MCP `preserve_host:false` 覆盖。Status/list 服务投影及 `access explain` 显示配置策略；全局失败且 registry 不可读时策略未知，status 省略该字段。
### HTTP 请求限制 (add 和 share)

| 标志 | 默认值 | 含义 |
|------|--------|------|
| `--max-request-body 20GiB` | `32MiB` | 上传大小上限; 支持正整数字节、B、KiB/MiB/GiB/TiB 或十进制 KB/MB/GB/TB |
| `--ack-unlimited-request-body` | false | 与 `--max-request-body unlimited` 一起使用, 明确确认移除大小上限 |
| `--request-header-timeout 20s` | `10s` | 接收完整请求头的最长时间 |
| `--request-read-timeout 2m` | `30s` | 正在读取上传内容时允许无进展的最长时间; 持续上传没有总时长截止 |
| `--idle-timeout 90s` | `60s` | HTTP keep-alive 请求之间的空闲时间 |

超时必须是正数 Go duration, 例如 `30s` 或 `2m`。适用于 proxy/file 服务;
raw TCP 不接受 HTTP 请求限制。add 替换同名服务时, 未重复的限制恢复默认值;
share 只复用有效限制相同的服务。
限制冲突会列出不同的标志和当前值、请求值; 用完整服务配置执行 add 进行修改。
未使用或被拒绝的 HTTP/1 请求体有最多 1s 的绝对清理期限;
`--request-read-timeout` 小于 1s 时采用该值。清理未完成则关闭连接,
不会对已接受的上传施加总时长超时。

`add --json`、`share --json`、`status --urls --json` 和 `list --verbose --json`
在 `request_limits` 中返回 `max_body_bytes`、`header_timeout`、`read_timeout`
和 `idle_timeout`。`max_body_bytes:-1` 表示已确认的无限制。
普通 `status --urls` 和 `list --verbose` 也显示有效限制。envelope 保持 schema version 1。
MCP add/share 接受可选对象 `request_limits: {"max_body":"20GiB","read_timeout":"2m"}`;
无限制必须提供 `{"max_body":"unlimited","unlimited_ack":true}`。省略的字段使用默认值。

大小超限返回 413; 上传停止进展或请求头未及时完成返回 408。
结构化日志记录服务名、限制、状态码及 code (`request_body_limit`、
`request_read_timeout`、`request_header_timeout`)。每类限制首次命中会保留在当前节点
的 runtime warning 中; status 和 verbose list 显示 warning, doctor 建议对应标志。
节点重启后清除这些 warning。流式上传拒绝时后端可能已接收部分内容。
应用后端及公网 relay 自身的限制仍然有效。

Windows `tslink install --startup` 显式选择下次登录启动、无崩溃恢复的 Startup 降级。默认 `install` 使用 Task Scheduler 启动内置 supervisor 并验证立即启动。`stop` 停止两个进程，包括崩溃退避期间；`install` 重置已触发的崩溃循环断路器。见[daemon 生命周期](daemon-lifecycle_zh.md#windows-监管与迁移)。

## 修改期限

`tslink extend <service> [--person <login>] (--for <lifetime> | --until <date/time>) [--regrant] [--ack-never]` 修改单个人员授权或 Funnel TTL。相对值从操作时刻起算,可缩短或延长。已过期须 `--regrant`,撤销人员不能恢复。始终输出版本化 JSON envelope。MCP `extend` 使用 `service`、`who`、`for`/`until`、`regrant` 和 `ack_never`。夏令时、配置校验、策略 API 以及健康/超时/keepalive 标志的语法见[时长文档](durations_zh.md),公开期限见[Funnel](funnel_zh.md)。

已有私有服务改为公开时,省略 `--funnel-ttl` 使用有限 24h 默认值。只有已决定的公开期限才会保留,包括历史显式公开 `never`;dry-run 和 MCP add 规则相同。首次 `--invite` 在授权事务中保存持久访客分类,早于远端操作;后续 update/extend 保留访客策略。邀请发送状态转换前发生并发授权变更时返回 `conflict`。
## 本地访问历史

`tslink access log [--app X] [--who Y] [--since 24h|RFC3339] [--until RFC3339] [--decision allowed|denied] [--limit N] [--json]` 返回倒序事件、按人计数及每个应用最后允许访问时间。默认 limit 100（1–10000），汇总计全部匹配项。标准 envelope 的 `data` 包含 `events`、`summary`、`truncated`。MCP：`access_log`、`access_summary`，均为只读，可授予 viewer scopes。

`tslink access path <app> <prefix|full|off|inherit|true|false>` 设置单应用路径模式，默认 prefix；full 可能保存应用自己的 bearer 路径，off 不记录路径。全局 `config set` key：`access-log-enabled`、`access-log-path`（兼容 boolean），`access-log-path-mode`（prefix/full/off）；`access-log-retention-days`（默认 30）、`access-log-max-bytes`（默认 67108864）、`access-log-queue-size`（默认 1024）。空值恢复默认；全局改动需重启 `serve`。`status`、`doctor` 包含 `access_log` 当前实例健康信息（current、最后写入、drops、size、缺失历史窗口；旧成功快照不能替代当前失败）。严格范围、隐私、持久性及事件计数语义见 [access-log_zh.md](access-log_zh.md)。

## 浏览器访客链接

`tslink guest create <app> --for <lifetime> [--label "Aunt May"] [--pin] [--public] [--print-link] [--json]` 创建有限期限的单应用浏览器授权。首次开启必须的 Funnel gate 需 `--public`，已有开放 Funnel 须先关闭。PIN 从隐藏终端输入或 stdin 读取。只有 `--print-link` 披露 bearer URL，且需当前精确节点 URL。`guest list`、`guest show <id>`、`guest revoke <id>` 不返回 token hash 或 token。MCP owner-only 工具：`guest_create`、`guest_list`、`guest_show`、`guest_revoke`。稳定字段、迁移、cookie、PIN 限流及与 people grant 的比较见[访客链接](guest-links_zh.md)。

非 JSON 的 guest list/show/revoke 显示 label、app、本地到期时间与具名时区和相对期限、status、uses。revoke 明确确认 ID，create 的发送说明包含易读到期时间。JSON 不变。

访客计数持久化错误进入 daemon log，并在 `status` / `status urls --json` 的服务 warnings 中显示为 `guest_counters_persistence_failed`。替换后的目录 sync 失败时，已可见计数的批次被确认，持久性未确认，批次不会重复应用；后续成功 registry 写入会清除此告警。
