# 守护进程生命周期

registry watcher 会在首次权威同步前启动。节点构造期间，相同 registry 状态的通知
会复用这次启动；更新的状态仍会替代它。就绪结果对应成功应用的状态。
首次同步失败时会释放待完成的目标，让后续尝试可以重新同步相同状态。

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

# tailnet 策略允许的设备可以访问 https://myapp.<your-tailnet>.ts.net
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
`unknown` 表示无法判定。macOS LaunchAgent、Windows 计划任务和 Startup 启动项恒为 `login`；
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
Windows 默认通过 Task Scheduler 立即启动内置 supervisor，并在后续登录时启动，要求用户
保持登录。supervisor 对 daemon 崩溃执行有上限的指数退避。`--startup` 降级不提供崩溃
重启，也无法证明当前 PID 的归属。后端应用本身仍需设置开机启动，首次 Tailscale 入网仍需授权。
安装文件绑定绝对 `TSLINK_CONFIG_DIR`，自动安装拒绝覆盖另一配置的监管器。
Homebrew 不注册第二套服务管理器。

## Windows 监管与迁移

Windows 默认监管器是 `windows-task-scheduler`。只有加载的用户、配置、动作、启用状态和
重启策略与实际运行的内置 supervisor 通过检查，`supervision` 才报告
`autostart_scope:login`、`restart_on_exit:true`。对运行中的 daemon，还会核对其自身 PID
身份、程序路径、到 supervisor 的父链，以及 supervisor 到任务 engine 的父链。
未知或不匹配状态报告 `manual`/`none` 并给出诊断。

计划任务负责登录启动。Windows VM 实测中，强杀直接启动的 daemon 和独立的 exit-1
对照均未触发 Task Scheduler 重启。保留每 60 秒 / 255 次的 `RestartOnFailure` 设置作为
启动失败的后备措施；该设置不能证明 supervisor 自身崩溃后会恢复。

隐藏的 Windows supervisor 用完全相同的环境和配置目录启动前台 `serve` 子进程，daemon
继续发布自己的 PID。配置目录锁阻止重复 supervisor；Windows Job Object 在 supervisor
在创建子进程时就将它归入 job；supervisor 消失时会终止整个子进程树，即使启动尚未完成。
此机制要求 Windows 10 或更新版本。daemon 意外退出（包括退出码 0）后按 1、2、4、8、16、32
秒退避，最高 60 秒。一次运行至少 5 分钟会重置失败计数。连续 8 次不稳定运行触发断路器，
停止恢复并让 supervisor 成功退出，任务重试无法撤销断路器。断路器跨登录保留；检查日志
后运行 `tslink install` 重置。`status` 和 `doctor` 显示 `supervisor_pid`、`runtime_state`
（`starting`、`running`、`restarting`、`stopped`、`circuit_open` 或 `failed`）与
`failure_reason`。退避期间 daemon 不在和 supervisor 不在是不同状态，detail 显示下次启动时间。

迁移 Startup 安装时，先停止 daemon，再运行 `tslink install`。老版本没有 shutdown event
时，新版 `stop` 会显式报错；先核实旧 daemon 身份并单独停止，再安装。安装器验证任务后
才删除 `Startup\tslink.vbs`。重新安装会更新任务并优雅重启已验证的任务所属 daemon；
手动或无关 daemon 被视为冲突，不会接管。注册、启动或稳定性校验失败时保留
`%APPDATA%\tslink-supervisor\task.xml`，用于检查和重试。Windows 升级失败不会恢复旧任务
或已替换的程序；停止/禁用的任务可能需要成功重新安装才能恢复运行。
属于 TSLink 的任务即使被禁用或重启设置需要修复，仍可用 `tslink install` 修复，或用
`tslink uninstall` 移除。禁用任务不会报告健康监管。停止或删除报错后，解决报告的原因，
再重试任一命令；归属仍要求同一用户、配置和精确的启动动作。旧版直接启动 daemon 的
任务可修复和移除，但不报告已验证的崩溃恢复。优雅停止后 PID 文件缺失
是合法状态；格式错误或不可读的 PID 证据仍会阻止安装。

`tslink stop` 在验证身份和发送停止请求期间始终保留进程句柄，停止事件绑定到该实例记录的
创建时间。它设置只允许当前用户和 SYSTEM 访问的 Windows 命名停止
事件。它先停止 supervisor，包括 daemon PID 不存在时的退避等待；supervisor 取消恢复，
并请求直接子进程执行正常 tsnet 清理。手动 daemon 沿用既有停止事件。无需控制台、端口
或管理员权限。子进程停止最多等待 5 秒（另加有上限的启动事件等待），supervisor 停止最多
等待 10 秒。失败时保留检查证据；supervisor 因错误退出后 Job Object 回收已归入它的
子进程。`tslink uninstall` 先禁用任务，停止两个进程，确认无运行实例后删除任务和本地定义。
检查、归属或停止失败时保留定义，任务可能已经禁用。只有 Startup 的卸载只移除自启动，
不会接管当前进程。

Task Scheduler 不可用时，仍可显式选择 `tslink install --startup`。这个降级没有崩溃恢复；
存在注册服务时 doctor 报 `daemon_restart_unavailable`。选项比较见
[Windows 平台选择](platforms_zh.md#windows-无人值守运行)。

在干净、可丢弃的 Windows 用户会话运行非交互 smoke：

```powershell
go build -o .\tslink.exe .
powershell -NoProfile -File .\scripts\windows-supervision-smoke.ps1 -Binary .\tslink.exe
```

脚本拒绝已有 TSLink 监管文件/任务和凭据，用隔离的空 registry，不创建 Tailscale 节点。
它核对 daemon 的 runtime 业务产物，杀死 daemon 后观察延迟重启，证明优雅停止后保持停止，
再安装并卸载。明确打印 `PASS`/`FAIL`，退出码为 0/1，大约需要 2 分钟。
Windows CI 执行此 smoke 和原生 unit/race 测试；有管理员能力的 CI 用户本身不能证明普通
用户的安装权限，仍应在 VM 的普通用户会话运行同一脚本。

Microsoft 参考，访问日期 **2026-10-02**：
[RestartOnFailure 范围](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-tsch/2ff4aa5a-7bc4-449f-bbb1-27475645867f)、
[Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)、
[任务 instance engine](https://learn.microsoft.com/en-us/windows/win32/taskschd/runningtask-enginepid)、
[运行实例](https://learn.microsoft.com/en-us/windows/win32/taskschd/registeredtask-getinstances)、
[命名事件与安全](https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createeventw)、
[全局对象命名空间](https://learn.microsoft.com/en-us/windows/win32/termserv/kernel-object-namespaces)。
