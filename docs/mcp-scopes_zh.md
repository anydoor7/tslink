# MCP 作用域

TSLink 与 Tailscale 配合工作，是独立项目。MCP 作用域让所有者把指定 app 的
操作交给 agent；Tailscale 仍控制哪些设备能到达控制面节点。

## 内置角色

| 角色 | 能力 | app 边界 |
|---|---|---|
| `viewer` | `list`、`status`、`health`、`doctor`、`url`、`tags_list`、`access_explain`、`people_list`，以及静态 `recipe_list`、`template_list` | 显式 `apps`；或显式 `inventory: true` 读取全部 app 清单 |
| `app-operator` | viewer 能力，加 `app_restart`、`people_grant`、`people_revoke` | 仅列出的 app；必须有正数 `max_duration` |
| `people-manager` | viewer 能力，加 `people_grant`、`people_revoke` | 仅列出的 app；必须有正数 `max_duration` |
| `owner` | 所有已发布工具，包括 `mcp_audit` | 不受限；不能同时填写 app 或时长限制 |

受限角色不能注册/删除服务、使用 Funnel、改 tag/全局设置、发现主机监听端口、
预览/应用 recipe 或 template、读取全局日志/邀请，或调用原有的整个人员修改工具。
本版本不提供自定义作用域或任意工具授权。高权限邀请仍要求所有者开启
`mcp.allow_elevated_invites`。作用域管理只由所有者编辑配置，不经 MCP 开放。

`people_grant` 接收 `{"who":"alice@example.com","app":"photos","for":"1h"}`。
`people_revoke` 接收 `{"who":"alice@example.com","app":"photos"}`。
它们只修改一个私有 HTTP/file app 的 grant，保留其它 app、期限和邀请历史，
不能解除所有者作出的整个人员撤销。`for` 必须是正数时长，不超过 `max_duration`，
也不能超过绑定本身的截止时间；拒绝 `never`。它们不创建 Tailscale 网络邀请：
已在 tailnet 内的人不需要邀请，其余网络邀请由所有者处理。撤销拒绝后续 HTTP
请求；已接受的网络分享和进行中的流可能保留。TCP 和公网 Funnel 不支持人员 grant。

`app_restart` 接收 `{"app":"photos"}`，排队重启这个 app 的 TSLink 网关节点，
保留已注册节点身份和其它 app。它不会重启第三方应用进程或安装 daemon，
`queued` 也不是重启完成的证明；随后轮询 `status`/`health`。
受限 operator 不能重启已有的公网 Funnel app。

## 给家人的 agent 只读访问

在 `config.json` 保留自己的 owner 条目，再加入家人的精确 WhoIs 登录名：

```json
{
  "mcp": {
    "enabled": true,
    "allow": ["you@example.com"],
    "bindings": [
      {"principal":"family@example.com","role":"viewer","apps":["photos"]}
    ]
  }
}
```

修改 MCP 配置后重启 daemon。家人的 MCP client 从能到达你的 tailnet 的设备
连接 `https://tslink-mcp.<tailnet>.ts.net/mcp`。`tools/list` 只发布 viewer 工具；
`list`、`status`、`health` 只显示 photos，不返回其它 app、计数、grant 或全局
诊断证据。受限 doctor 读取范围内健康观测，不执行凭证、主机、supervisor 或
外部新探测。MCP 只读权限本身不授权打开 photos；app 访问权限另行管理。

## 远程绑定与期限

旧 `mcp.allow` 继续表示 owner。要降低已有登录名的权限，先从 `allow` 删除，
再添加一条 binding；两者出现相同 principal 时拒绝配置。binding 登录名必须
采用规范形式：去掉外层 ASCII 空白，仅把 ASCII A-Z 改成小写，不做 Unicode
case folding。tag 使用精确的 `tag:` 前缀，名称大小写保持不变，包括旧的大写名称；
不通过 case folding 把它转换成更广的授权。
未知字段/角色、重复 binding principal/app、
非法名字、`all` app 选择器、缺少 app 清单和非法期限都会拒绝。

```json
{
  "principal":"colleague@example.com",
  "role":"people-manager",
  "apps":["finance"],
  "max_duration":"8h",
  "issued_at":"2026-10-02T12:00:00Z",
  "for":"7d"
}
```

把 `issued_at` 换成实际授权时间。`for` 经现有 Go duration 加天数的公共 parser
解析，并锚定到这个时间，重启不会续期。也可直接用 RFC3339 `expires_at`，
它与 `issued_at`/`for` 互斥。两种期限都省略表示无限期绑定。过期绑定拒绝新请求；
已放行 HTTP 请求/流在剩余期限结束时取消。期限到达不回滚已开始的远端副作用。
每个 MCP 变更都在锁等待结束后的事务或副作用边界重新检查会话期限和取消状态。
取消请求可回滚已经开始的 share 中本次精确的 tentative 注册；补偿仍在锁内检查 binding 期限。过期后可能保留已开始的注册，owner 可根据审计回执检查。

每个 HTTP 请求都会先执行 WhoIs，再路由工具。tag principal 只匹配 node tags，
不会匹配拼写相同的登录名。显式 login binding（包括旧 login 条目）优先。
否则合并检查 `bindings` 和旧 `allow` 中所有匹配的 tag principal；匹配多个 tag
就返回 HTTP 403。单个旧 allow 条目仍表示 owner。旧 owner tag 对每个带此 tag 的设备都开放广泛权限；迁移
到受限角色时移除它。Doctor 对所有 owner tag、过期 binding，以及含多个不同 tag principal、可能
同时匹配同一 node 的配置发出 warning；它不会查询实时 node tags。

## 本地受限会话

启动进程的 OS 用户默认是 owner。每个进程可显式降低权限：

```sh
tslink mcp --scope viewer --apps photos
tslink mcp --scope app-operator --apps photos --max-duration 8h
tslink mcp --scope people-manager --apps finance --max-duration 2h
tslink mcp --scope viewer --inventory
```

stdio operator/manager 默认 `max_duration` 为 24h。Viewer 读取全部清单必须显式
启用 inventory；空 app 清单不授予任何权限。MCP 参数不能改变启动作用域。
为不受信任的 agent 配置这些 MCP 参数时，也要限制其 shell 和文件能力。
这是 MCP 权限边界，不是 OS 沙箱；以所有者身份运行的其它进程仍能读取配置
或另外启动 owner 会话。

## 拒绝、返回数据与审计

app/期限/时长拒绝使用稳定码 `mcp_scope_denied`。隐藏工具与不存在的工具行为
相同：旧协议返回 JSON-RPC `-32602`；当前按 header 路由的 HTTP 传输返回 SDK
的 unknown-tool HTTP 400。HTTP 身份拒绝返回 403，带
`error.data.code: mcp_scope_denied`。工具授权入口在返回前同时过滤 text 和
structured result。受限 client 使用轮询；所有者的全局 `/events` 对其返回 404。

每个进入授权入口的已知写操作记录时间、调用者、角色、实际能力、工具、app
引用和稳定结果。日志位于配置目录 `mcp-audit.json`，最多 1,024 条且不超过
1 MiB，轮换最旧记录。副作用前先持久化 `started`，再以同一 ID 记录完成结果；
只有 started 而无完成表示结果未知。不存原始参数、链接、凭证、target 或错误
消息。协议层的未知工具不会执行写操作。

所有者可用 `tslink mcp-audit --json`（正常版本化 envelope）或 `mcp_audit`
读取。开始记录失败以 `mcp_audit_unavailable` 拒绝变更；完成记录失败明确说明
变更可能已发生。并发写入通过文件锁串行化，等待有上限。截断/损坏、特殊文件
和超限日志都会保留并拒绝。`status` 显示不含秘密的 binding，`doctor` 报告风险。

审计适配口是 `internal/mcpaudit.Journal.Record(ctx, Entry)`，便于纳入通用 access
log。时长解析集中在 `internal/mcpscope.ParseDuration`，便于替换公共语法。

2026-10-02 查阅的 Tailscale 文档：[tsnet LocalClient 与 WhoIs](https://tailscale.com/docs/reference/tsnet-server-api)
和[设备 tag](https://tailscale.com/docs/features/tags)。

受限 operator 和 manager 只能修改 people store 中已存在人员的授权。只有 owner 可以创建人员；稳定码 `mcp_person_owner_required` 提示先请 owner 添加人员。撤销未知 login 是 no-op，不创建人员，也不改变其它 app。

审计用 `identity.login`、`identity.node` 记录调用者，用 `principal` 记录匹配的授权对象。`kind=mcp`、`role`、`tool`、`apps`、`result`、`phase` 是类型化元数据。share intent 只记录显式请求的名字；尚未分配时 app 清单为空。completion 记录实际生成或复用的名字，包括并发分配结果。不记录原始参数、target、邀请链接或 secret。

Daemon bootstrap 在 scope 检查、安装冲突检查、安装前后的 supervision 检查、定义文件写入和 manager 命令中保留调用者会话。每次 manager 查询前检查会话，并在调用者 context 上追加原有超时，因此取消会中止正在查询的子进程。恢复已有安装和失败清理可以在取消或过期后继续，仍有超时限制。旧日志的 `who`/`scope` 按 `principal`/`role` 读取，不推断缺失的调用者身份；日志读取拒绝父目录链中任何普通文件阻塞。

共享 status 读取在 MCP `status`、`health`、owner 与受限 `doctor`、`list`、`url`、share/add 结果轮询及 owner event snapshot 中保留调用者 context。取消会中止 supervision 查询，每次启动查询子进程前也会检查会话过期。受限 doctor 从共享 status reader 投影 app 观察值；reader 同时检查 supervision 和已存凭证，不运行完整 doctor 的主机发现或外部探针。MCP 检查拒绝保留 `mcp_scope_denied`；没有 MCP 会话的调用者在 setup 检查失败（包括取消）时，保留 `daemon_setup_failed` 和恢复指引。

有时限的 manager 补偿从调用者派生 context，保留身份与 scope。只有恢复已捕获的既有状态和禁用不确定替换任务使用独立生命周期；正常 manager 读取和替换任务激活仍受请求取消与过期约束。
