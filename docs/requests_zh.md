# 申请应用或延长使用时间

TSLink works with Tailscale, independent project。已加入主人 tailnet 的真人访客，可以在[应用总入口](portal_zh.md)申请私有应用。主人一次操作即可批准并选择期限。这不会改变 Tailscale 网络策略，也不替代应用自己的登录。

## 主人设置

每个应用的申请发现功能**默认关闭**。主人显式允许在表单中公开应用名称：

```sh
tslink add photos --proxy localhost:3000 --requestable
tslink portal enable --owner owner@example.com
```

替换已有注册时保留原 target 和其他设置；`add` 会替换注册。`--requestable=false` 从申请表单隐藏应用，也隐藏访客的旧申请记录。其他无权访问的应用仍隐藏：名称、数量、URL 和后端观察结果都不输出。只有私有 HTTP proxy/file 应用可供申请；TCP 和公共 Funnel 拒绝此标志。它公开名称，不公开 URL，也不授予权限。

访客保持 Tailscale 已连接，打开入口地址，选择应用，可从带标签的选项中选择 1 小时、1 天、3 天、7 天，或由主人选择，并可填写短备注，点击 **Send request**。已有应用也可用同一表单申请更多时间。页面显示等待、批准、拒绝或超时状态。批准后，授权有效时会显示应用；地址可能仍需完成注册。访客可以没有任何已有 TSLink 授权；批准会创建本地人员记录。

仅限真人 tailnet 成员。带 tag 的机器、shared-in peer、WhoIs 身份缺失或异常、已撤销人员，以及 F11 持久化分类为 guest 的人员，均不能提交。tailnet 外的设备共享接收者不在范围内。不会根据一次申请推断人员已升级为成员。没有 OIDC 或访客 bearer link。

## 一次批准

```sh
tslink requests list
tslink requests list --json
tslink requests approve <id> --for 3d
tslink requests deny <id> --reason "Please ask again next week."
```

agent 可调用仅 owner 的 `requests_list`、`requests_approve`（`id`、`for`，可选 `ack_never`）和 `requests_deny`（`id`，可选 `reason`）。远程调用要求 WhoIs 无 tag 身份精确匹配入口配置的 owner；本地 CLI/stdio MCP 沿用可信 owner 进程约定。入口 admin 不自动获得审批权。HTTP MCP 通过 `portal_enable` 修改入口 owner/admin 设置，也必须是当前 owner；其他调用者返回 `access_request_owner_required`。owner 尚未设置时不能远程认领。本地 CLI 和 stdio MCP 是可信的首次设置与恢复通道，包括替换丢失的 owner login。F6 scope 映射留到集成。

HTTP MCP 在 registry 写事务内检查申请列表、批准和拒绝权限：调用者必须是当前入口 owner，且不带 tag、未被撤销。新增、更新、移除、恢复人员或修改授权时，如果目标是当前 owner 或入口 admin，同样必须由该 owner 操作，并在锁内按变更前状态检查；已撤销的 owner 不能远程恢复自己。拒绝返回 `access_request_owner_required`。本地 CLI 和 stdio MCP 仍是可信恢复通道，普通人员的恢复规则不变。

批准只修改**一个应用授权**，保留其他应用、期限、邀请历史和持久 guest 分类。已撤销人员和不再允许申请的应用会拒绝。增量 F1 `ChangePersonAppWithLifetime` 契约及 F11 策略与申请决定在同一 registry 锁内执行；授权和状态一起提交。相对期限从批准时刻起算，不从提交时刻或旧期限起算。批准显式续授过期 app grant，但不会清除人员撤销 tombstone。

时长使用[F11 语法](durations_zh.md)：`90m`、`36h`、`3d`、`1w`、`1d12h` 或 `until <date/time>`。最短 1h。`durations.public_max` 也约束持久 guest（默认 7d）；成员遵循成员策略。永久成员审批需要 `--for never --ack-never`（MCP `ack_never: true`），guest 始终不能永久授权。访客填写的时长只是建议，最终由主人选择。绝对时长建议保存为 UTC RFC3339，以免重启或时区变化改变含义。

相同决定及参数重试会返回原结果（`changed: false`）。不同期限、相反决定或对过期申请批准，返回 `access_request_decided`（exit 4）。重试不会延长期限或重复发决定事件。CLI/MCP 使用既有 schema-version-1 envelope。备注是明确标注的不可信数据，不能当成 agent 指令。CLI 人类输出过滤终端控制字符；JSON 保留原文并做 JSON 转义。

## 通知与保留

新申请通过既有[F2 通知器](health-and-alerts_zh.md)发送，配置在私有 `alerts.json`：绝对路径的 command argv 或 webhook 二选一。kind 为 `access_requested`；`request` 带 `id`、`who`、`app`、`requested_duration`、`status` 和 `at`。沿用 F2 有界进程/webhook 处理。不发送备注、拒绝理由、bearer URL、凭据或通知目标。通知尽力而为且最多一次：先保存申请，失败或崩溃后访客重试不会再发通知。HTTP `X-TSLink-Notification` 为 `none`、`sent` 或 `failed`；保存的收件箱始终为真相来源。

已授权 MCP events stream 会收到 `update`；snapshot 的 `access_requests` 携带同样安全的 typed summary。既有 MCP event principals 是可信操作人员；F6 筛选留到集成。备注/理由只在申请详情中提供，不推送到通知或事件。也可告诉 agent，按指定期限批准某个申请。

同一人员/app 只允许一个 pending 申请，每人每小时最多 5 个新申请，全局每小时最多 100 个。检查在锁内进行并跨重启保留；重复申请不生成记录、不发送通知。备注和拒绝理由最多 500 个 Unicode 字符，时长最多 128 bytes，表单最多 8 KiB，并受 F8 listener limits 保护。POST 同时要求匹配的 HTTPS Origin，以及绑定 WhoIs 身份和 canonical host、两小时过期的 HMAC 表单 token。入口重启或 token 过期后须重新加载表单。

pending 申请 7 天后过期。决定从决定时刻保留 30 天（expired 从七天截止点起算）。读取、提交和审批时应用保留规则；成功返回的过期状态会持久锁存，陈旧批准也会锁存。维护拿不到写锁时，列表返回可重试的 `access_request_busy`（exit 4），不返回未落盘的 expired 状态；等待写入完成后重试。入口继续显示普通可访问应用，并提示申请暂不可用，重新加载成功后恢复。队列有界：最多 1,000 条，registry reader/writer 上限为 4 MiB。容量不足时拒绝新增，没有部分状态。无需独立申请数据库、worker 或 timer。

基线没有 F4。集成可接入提交后的 `internal/server.accessRequestRecordedFn` 和决定后的 `cmd.requestDecidedFn`。两者接收窄 `registry.RequestEvent`，在成功保存后同步执行，默认无动作。本包不声称已保存 access-log records。

如果某个服务登记损坏，请求维护会暂停，直到主人修复，避免类型化写入丢弃损坏的登记。入口页继续显示可访问的有效应用，并说明请求暂不可用。
