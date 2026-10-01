# 平台支持

## 前置条件

- [Tailscale 账户](https://tailscale.com)（个人使用免费）
- 你要访问的设备上安装 Tailscale（手机、平板等）
- 从源码构建需要 Go 1.26.6+

## 平台支持

| 平台 | 守护进程 | 自动启动 | 停止行为 |
|------|---------|---------|---------|
| macOS | `--daemon` | LaunchAgent | SIGTERM 优雅停止 |
| Linux | `--daemon` | systemd user service | SIGTERM 优雅停止 |
| Windows | `--daemon` | 启动文件夹 | 强制终止进程 |

配置与状态目录在 macOS 和 Linux 上是 `~/.config/tslink/`，在 Windows 上是 `%AppData%\tslink\`；设置 `TSLINK_CONFIG_DIR` 可以改用其他目录。本 README 其他地方写的 `~/.config/tslink/` 都指这个目录。Windows 上 TSLink 不会移动旧的 `%USERPROFILE%\.config\tslink\`：只有旧目录时，每个命令都以 `legacy_config_dir_present` 停止，并给出要执行的那一条 `move` 命令；两个目录同时存在时，拒绝二选一并报出两个路径。

这个目录里的 `credentials.lock` 让多个 TSLink 进程依次修改 credential：`tslink login`、`tslink logout` 和 `tslink doctor --probe-remote` 会创建它；`tslink serve` 和读取 credential 状态的命令，只在为已存储但还没有 metadata 记录的 credential 补写记录，或 `serve` 发现旧的 `apikey` 文件时，才会创建它。启用系统 keychain 时，也就是默认情况下，TSLink 还会锁住 OS 账户 home 目录下的 `.tslink/credentials.lock`，Windows 上对应 `%USERPROFILE%\.tslink\`，这个位置不随 `TSLINK_CONFIG_DIR` 或 `$HOME` 改变；两个文件都是空文件，创建后一直保留。`node-identities/` 为每个 service 保存一条记录，写明它的 node 启动时用的 tags、ephemeral 设置和 control URL，其中 tags 包括自动派生的 `tag:tslink-funnel`；任何一项改变，包括 daemon 停止期间做的改动，都会让 daemon 清掉这个 node 的状态，让它重新加入 tailnet。在未存储凭证的 Tier 1 上，node 不 advertise tags，所以只有 ephemeral 设置或 control URL 的改变会这样做。移除一个 service 之后，只要它的 `nodes/<name>/` 状态已经删掉，无论是 `tslink remove` 还是 daemon 删的，daemon 都会在下一次同步时删除它的记录；这份状态何时删除见 `tslink remove --help`。只是从 `registry.json` 里消失、没有经过 `tslink remove` 的 service 会保留 node 状态：registry 丢失、换成别的副本或手改出错，都不会删掉 node 身份。

只读 MCP `list`、`status`、`url` 和 `doctor` 会描述缺失的凭证 metadata，
但不会补写记录，也不会创建 `credential-meta.json` 或 `credentials.lock`。
CLI `status` 和 `doctor` 在需要时仍会补写；只有 credential store 仍保存读取时的值，
补写才会落盘。新注册的 `share` 等待 URL 时，`registry.json.tentative-<name>`
标记它可能回滚的注册项。相同的 `share` 或未修改该注册项的 `add` 会确认保留该项；
`share` 完成或注册项被复用时，该标记会移除。

macOS LaunchAgent 安装会使用 launchd `KeepAlive` 和 `ThrottleInterval=30`。如果 LaunchAgent 仍在安装状态，运行 `tslink stop` 后 launchd 会重启 TSLink。想禁用自启动时，先运行 `tslink uninstall`，再运行 `tslink stop`。`gui/$(id -u)` 是否可用取决于该 uid 是否存在桌面（Aqua）session，而不是调用者是否通过 SSH 连接；没有 Aqua session 时，`tslink install` 会先尝试 `gui/$(id -u)`，如果该 launchd domain 不可用则回退到 `user/$(id -u)`。Linux headless user service 如需登出后继续运行，可能需要执行 `loginctl enable-linger "$USER"`；如果 lingering 只为 TSLink 启用，卸载后运行 `loginctl disable-linger "$USER"`。

所有平台都支持通过重新运行 `tslink install` 来升级。macOS 上，TSLink 会在 launchd handoff 前保存已有 plist，并且只有 pidfile PID 与 `launchctl print` 报告的 PID 一致时，才把运行中的 daemon 视为 launchd 所有。升级中的 post-bootstrap verification 失败时，TSLink 会恢复旧 plist，并重新加载此前确认由 launchd 管理的 job；但它无法恢复在命令运行前已经被替换的可执行二进制。首次安装发生同类 verification 失败时，TSLink 会 bootout 新 job，并且只在 bootout 成功后删除新 plist。如果清理未完成，plist 会保留，使 `tslink uninstall` 能再次尝试。升级或卸载无法检查某个 launchd domain 时，默认会保留 plist 并以 `launchctl_domain_unavailable` 拒绝；JSON failure `data` 会给出 `unavailable_domain`、`force_available`、精确的 `force_command` 与 `force_risk`。只有在接受残余 daemon 风险时才运行相应的 `--force` 命令。强制卸载后，domain 恢复可寻址时先运行 `launchctl print gui/<uid>/com.tslink.daemon`（或相应的 `user/<uid>` target）检查；如果 job 仍已加载，再运行 `launchctl bootout gui/<uid>/com.tslink.daemon`（或相应的 `user/<uid>` target）将其移除。真实的 `launchctl` 错误即使在 `--force` 下也仍然致命。

Linux 上，TSLink 同样会在替换前保存已有 systemd user unit。如果 `daemon-reload`、`enable`、`restart` 或 restart 后验证失败，TSLink 会停止失败的 service，原子恢复旧 unit，重新加载 systemd，并重启此前已确认由 systemd 管理的 service。两个平台都无法恢复在 `tslink install` 运行前已被替换的可执行二进制。修复报出的原因后，重新运行 `tslink install`。

