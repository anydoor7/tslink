# CLI 参考

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
| `tslink install` | 开机自启（macOS LaunchAgent / Linux systemd / Windows 启动文件夹） |
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

对已存在的名字执行 `tslink add` 会替换那个 service：替换只保留这次给出的 flag，没有重复写的 `--allow`、`--tags`、`--funnel` 等都会丢掉。JSON 结果列出 `replaced_fields`，访问权限或 node 身份改变时会给出警告。

| 标志 | 描述 |
|------|------|
| `--proxy host:port` | 反向代理到本地 HTTP 服务 |
| `--dir /path` | 文件目录服务 |
| `--tcp host:port` | 原始 TCP 转发 |
| `--dry-run` | 校验并打印服务，不写入注册表 |
| `--ephemeral` | 临时节点，停止后自动从 tailnet 移除 |
| `--tags tag:a,tag:b` | ACL 标签，用于 Tailscale 网络策略 |
| `--allow user@,tag:x` | proxy/file 服务的 HTTP 访问控制；TCP 会拒绝该标志，因为原始 TCP 使用 Tailscale ACL 标签和目标服务自身认证 |
| `--control-url URL` | 服务级控制服务器覆盖，例如 Headscale。用已存储的 Tailscale 凭证铸造的 auth key 只发给 Tailscale 自己的控制服务器；存有这类凭证时，TSLink 以 `credential_control_url_mismatch` 拒绝指向其他控制服务器的 service |
| `--funnel` | 通过 Tailscale Funnel 暴露到公网（仅限 proxy，必须同时传 `--public`） |
| `--public` | 显式确认 `--funnel` 的公网暴露；没有 `--funnel` 时无效 |
| `--funnel-ttl 1h\|8h\|24h\|72h\|7d\|never` | Funnel 公网期限，默认 `24h`；需要 `--funnel` |
| `--no-auto-provision` | 关闭该服务的 Funnel policy 自动配置；需要 `--funnel` |
| `--no-daemon-install` | 只保存配置，不安装或启动 daemon |
| `--health-path /ready` | 拼接到 proxy backend base path 的 HTTP 业务探针路径，默认 `/` |
| `--health-status-min N`、`--health-status-max N` | 预期 HTTP 状态范围，默认 200..299，仅 proxy |
| `--health-body text` | 前 64 KiB 内的预期子串，默认不检查，仅 proxy |
| `--health-timeout duration` | 探针超时 100ms..30s，默认 `5s` |
| `--health-interval duration` | 检查间隔 10s..1d 且不小于超时，默认 `1m` |
| `--wait duration` | 等待 URL 或授权 URL；默认 `30s`，`0` 表示不等待 |
| `--json` | 打印版本化的结果 envelope |

应用健康 (`healthy`/`degraded`/`down`/`unknown`)、检查时间和连续失败次数出现在 status、list JSON、`list --verbose`、MCP 与 `/events`。节点 key 和凭据采用 14 天/3 天到期预警，提供下一步并保留 metadata 来源。`doctor` 增加实时 HTTP 业务探针，3 天到期预警为 critical (exit 65)。Owner 通知通过 `alerts.json` 显式启用；事件与重启去重状态默认持久化。详见[健康与通知](health-and-alerts_zh.md)。

HTTP/TCP 探针执行 registry 的 target 安全校验。节点到期独立于 `--health-interval` 刷新，替换节点后旧日期立即失效。Status 从 durable journal 读取事件。通知采用有界队列；command 的 deadline 为 10 秒，pipe 清理额外最多 250ms，取消计为失败。
