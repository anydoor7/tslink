# 凭证与标签

## 凭证层级

TSLink 有两层认证模式：

- **Tier 1 零凭证（默认）**：user-owned node，不 advertise tags，也不调用远端 ACL API。适合临时展示页面或 ephemeral share。每个新的 service node 都有自己的 enrollment URL；单服务 quick share 只需一次 browser click。Tailscale 的 user-owned node key 会过期，因此持续运行数月的节点最终可能需要重新认证。
- **Tier 2 存储凭证（需主动开启）**：保留 tagged、per-service 的启动行为，适合 durable multi-service 安装。只有需要这一层时才运行 `tslink login`。

Tier 2 接受以下任一种管理员凭证：

- **API 访问令牌**：前缀为 `tskey-api-*`，在 [管理后台 → Keys](https://login.tailscale.com/admin/settings/keys) 生成。当前自动化能力最完整，包括通过 Tailscale API 管理标签和设备。它会周期性过期。
- **OAuth 客户端密钥**：前缀为 `tskey-client-*`，在 [管理后台 → OAuth](https://login.tailscale.com/admin/settings/oauth) 生成。它不会过期，但 TSLink 当前的 Tailscale 标签/设备自动化在该模式下更窄，因为这些操作依赖 Tailscale REST API。用于无人值守前请先验证所需的标签/设备操作。

`tslink login` 会交互式引导你完成任一 Tier 2 凭证路径；它不会先做一次无实际作用的临时 browser login。凭证优先存储在系统钥匙串（macOS Keychain / Linux secret service / Windows 凭据管理器）中。在 macOS 与 Linux 上，只有 TSLink 能证明残留的钥匙串凭证不存在或已清除，受限权限文件回退才会成功。钥匙串完全无法访问或状态不确定时，login 会明确失败，以免未核实的文件值取代已有凭证；请恢复钥匙串访问后重试。仅有 headless 环境并不保证回退。Windows 没有文件回退：TSLink 无法在本地证明该文件的 DACL 只允许当前用户访问，所以凭据管理器不可用时 `tslink login` 会直接失败。

非交互式自动化优先使用 stdin。环境变量只适合由 secret manager 在进程启动前预注入；不要在 shell 命令里 inline secret 值，否则可能进入 shell history：

```bash
printf %s "$TSLINK_API_KEY" | tslink login --api-key-stdin
printf %s "$TSLINK_CLIENT_SECRET" | tslink login --client-secret-stdin
```

兼容性保留的 `--api-key` 和 `--client-secret` flags 仍可用，但命令行参数可能被其它本机进程看到，不作为推荐路径。

## 标签管理

TSLink 默认管理本地服务标签。普通远端 tag ACL 写入需要 `--manage-acl`；已确认公网暴露的 Funnel 服务使用上文所述的默认开启的 policy 自动配置。

零凭证 Tier 1 会保留 registry 中的 tags 配置，但 user-owned node 不 advertise 这些 tags，也不会调用远端 tag/ACL API。下面的标签行为适用于存储凭证的 Tier 2。

- **默认标签**：`tslink add` 未指定 `--tags` 的服务自动应用 `tag:tsmain`。
- **远端 ACL 读取**：`tslink tags pull` 只在 API 访问令牌模式下拉取远端 ACL 标签；OAuth-only 模式会跳过远端读取并提示需要 API 访问令牌。
- **远端 ACL 写入**：`tslink login --manage-acl`、`tslink serve --manage-acl` 和 `tslink tags delete-remote --manage-acl` 才会 opt in typed whole-policy ACL writes，并输出 machine-readable side-effect plan。这个 flag 管理普通 tag 写入；已确认公网暴露的 Funnel 服务另行使用默认开启的 policy 自动配置。
- **严格标签语法**：标签必须匹配 `tag:<lowercase-hyphen-name>`，只使用小写字母、数字和连字符。将 `tag:Web`、`tag:db_main` 或 `web` 这类旧值迁移为 `tslink tags set <service> tag:<lowercase-hyphen-name>`，也可以直接编辑 `registry.json`。无效旧标签会让 `tslink serve` 验证失败，必须先修复才能启动网关。
- **运行时认证刷新**：标签、临时节点设置和有效控制服务器 URL 变化时，受影响节点会删除本地状态并用新的每服务认证材料重启。切换凭证模式或修改旧版 `authkey` 文件后仍需重启 `tslink serve` 进程。

使用 `tslink tags` 查看和自定义标签分配：

```bash
# 查看所有服务及其标签
tslink tags list

# 拉取 Tailscale ACL 中当前定义的标签（需要 API 访问令牌；OAuth-only 模式会跳过）
tslink tags pull

# 为某个服务添加标签（节点自动重启）
tslink tags add myapp tag:production

# 替换某个服务的全部标签
tslink tags set myapp tag:webserver

# 修改新服务的默认标签
tslink tags set-default tag:myteam

# 通过本地安全检查和显式 ACL 管理 opt-in 后全局移除 ACL 标签所有者规则
tslink tags delete-remote tag:old-tag --force --manage-acl
```

