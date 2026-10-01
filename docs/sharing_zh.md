# 分享服务

## 一条命令分享

`tslink share` 会判断参数是目录、普通文件、裸端口还是 `host:port`。它会
注册服务且不覆盖已有同名项，在需要时启动 daemon，等待精确 runtime URL，
最后只向 stdout 打印该 URL。share 默认使用 ephemeral node。

```bash
tslink share ./build                # 服务整个 ./build 目录树，可浏览目录
tslink share ./report.html          # 只服务 report.html，同目录其它文件不可达
tslink share 3000
tslink share localhost:8080 --name preview
tslink share ./build --ephemeral=false
```

两种路径形态的可达范围不同，这是 service 自身的边界，不是列目录的显示偏好。
目录 target 会服务其下全部文件，没有 `index.html` 的目录会渲染目录列表。
普通文件 target 只服务那一个文件：URL 即该文件，service 根路径 302 跳转到它，
其余任何路径都返回 404，包括同目录下的其它文件。registry 用 file service 的
`file` 字段记录这个收窄；没有该字段的条目就是目录 share。

两种形态都不限制**谁**可以读：tailnet 的每个成员都能取到该 share，而且
`tslink share` 没有 `--allow` 参数。要限制目录的读者，改用
`tslink add <name> --dir <directory> --allow <principal>` 注册，或在 MCP
`share` 工具里传 `allow`。

TSLink 拒绝服务它自己的配置目录、配置目录里的目录，以及包含配置目录的目录
（默认布局下包括你的 home 目录），错误码是 `path_exposes_config_dir`；
`add --dir`、`share`、MCP 工具和 daemon 都做这项检查。检查比较的是解析符号链接
之后的路径，所以 TSLink 会像对待普通文件一样，服务你放在配置目录之外的硬链接。

零凭证首次运行时，stdout 的唯一一行是 Tailscale 授权 URL；stderr 会给出
精确的 `tslink url <name> --wait` 后续命令。使用 `--json` 时，这是包含
`auth_url` 的成功 `status:"needs_login"` 结果，不是认证错误。对同一 target
重试会复用已有 service，不会持续创建带数字后缀的孤儿 node。

