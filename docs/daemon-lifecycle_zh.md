# 守护进程生命周期

## 从零到一个 URL

创建演示页面并分享，无需 API token：

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

TSLink 会注册这个目录，并在 daemon 未运行时启动它。首次使用会打印 Tailscale 授权 URL。
打开该地址批准节点。如果服务 URL 仍待生成，可执行 `tslink url demo --wait`，
然后在其他 tailnet 设备上打开服务 URL。

不需要 API token，不需要 OAuth client，不用去 admin console。节点以你的身份入网，
因此它自己不需要任何 ACL 策略。

如果你更想显式注册服务并长期保留：

下列 proxy 示例要求本机已有应用监听 3000 端口。

```bash
# 1. 暴露本地 Web 服务
tslink add myapp --proxy localhost:3000

# 首次使用会自动安装后台服务并打印精确 URL。
# 如果需要 Tailscale 入网，打开打印的授权 URL 批准节点。

# 从任何设备访问 https://myapp.<your-tailnet>.ts.net
```

`add`、`share` 和 `template apply --yes` 在后台服务未运行时自动安装并启动它，
自动安装不要求 TTY。Linux 需要有可用 systemd 用户管理器和 `XDG_RUNTIME_DIR` 的登录会话；
CI 或没有用户总线时，使用 `--no-daemon-install` 并手动运行 `tslink serve`。
安装会向 stderr 声明监管器、安装路径、配置目录和
撤销命令 `tslink uninstall`；`--json` 的 stdout 保持单条结果。使用
`--no-daemon-install` 可跳过安装。离线 `add` 只保存配置，返回
`daemon_running:false` 和修复指引，不显示绿勾；带此参数的 `share` 要求已有服务在运行。
`add` 默认等待至多 30 秒取得精确 URL 或入网授权 URL；`--wait=0` 可关闭等待。

MCP 的 `add`、`share`、`template_apply` 同样自举，并提供 `no_daemon_install`。
这些 tool 完成后台服务安装后，在结果中通过 `daemon_installed` 返回 `manager`、`path` 和 `undo`。
若后续步骤失败，MCP failure 的 `data` 仍带有这份安装回执。
MCP `add` 自举完成后返回当前 URL/入网证据，不额外等待 URL；仍 pending 时可调用 `url`。
`add` 和模板先写 registry 再安装，安装失败保留已保存的配置。显式 `install` 也支持尚无
registry 的新环境。失败消息会说明监管定义是否残留：Linux 可能留下 enabled 且正在
重试的 unit；macOS 新装校验失败会在清理成功时撤回 job/plist。重试前检查 `tslink logs`
和 `tslink doctor`；Linux 还可用 `journalctl --user -u tslink.service`。
新安装或重新安装的 Linux 和 Windows 服务定义会写入 `tslink logs` 读取的日志文件；
升级这两个平台后，已有定义需要再运行 `tslink install` 才会选择该文件。
安装器验证监管状态稳定，自举再验证新鲜业务证据及稳定窗口；两者均不保证未来不会崩溃。

`status` 与 `doctor` 的文本和 JSON 都报告 `supervision`：监管器、自启动、
`autostart_scope`、重启策略与探测说明。无法确认监管的运行进程记为 `manual`；
没有运行进程且无可验证监管记为 `none`。`autostart_scope` 回答单个 autostart 布尔量
无法回答的问题：`boot` 表示无人登录时也随开机返回，`login` 表示要等这个用户登录，
`unknown` 表示无法判定。macOS LaunchAgent 与 Windows 启动项恒为 `login`；
systemd user unit 只有开启 lingering 才是 `boot`，否则报 `login` 并给出
`loginctl enable-linger "$USER"`。TSLink 只报告 lingering，不代为修改，
因为它作用于该用户的所有服务。
已有注册服务但无监管时 doctor 判 error；确认后台服务未运行时延后后端探测。PID 身份或监管状态无法确认时则给 warning 并保留探针，
先检查运行二进制与日志，再决定是否安装/重启。任何节点正在入网时，`url` 会返回授权动作，避免重复等待。
此时 `url` 的后续动作是 `tslink install`。

没有注册服务、也未启用 MCP node 时，人类可读的 `status` 会提示使用 `tslink add`
或 `tslink share`。只安装空的 daemon 不会产生可入网的服务 node。

macOS 注册登录时启动的 LaunchAgent；Linux 启用 systemd user unit。Linux 若要开机无需
登录、退出登录后也继续运行，需执行一次 `loginctl enable-linger "$USER"`。
Windows 自动安装后立即运行 Startup 脚本，后续登录时再次启动；它不监管崩溃重启，也无法
证明当前 PID 的归属。后端应用本身仍需设置开机启动，首次 Tailscale 入网仍需授权。
安装文件绑定绝对 `TSLINK_CONFIG_DIR`，自动安装拒绝覆盖另一配置的监管器。
Homebrew 不注册第二套服务管理器。

