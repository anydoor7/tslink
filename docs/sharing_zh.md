# 分享服务

## 一条命令分享

`tslink share` 会判断参数是目录、普通文件、裸端口还是 `host:port`。它会
注册服务且不覆盖已有同名项，在需要时启动 daemon，等待精确 runtime URL，
最后只向 stdout 打印该 URL。share 默认使用 ephemeral node。

```bash
mkdir -p tslink-demo && printf '<h1>TSLink demo</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
tslink share ./tslink-demo/index.html --name demo-file  # 只服务这个文件
tslink share ./tslink-demo --name demo-persistent --ephemeral=false
```

如果要分享端口，请先启动本机应用并确认它监听对应端口，再执行
`tslink share 3000` 或 `tslink share localhost:8080 --name preview`。

两种路径形态的可达范围不同，这是 service 自身的边界，不是列目录的显示偏好。
目录 target 会服务其下全部文件，没有 `index.html` 的目录会渲染目录列表。
普通文件 target 只服务那一个文件：URL 即该文件，service 根路径 302 跳转到它，
其余任何路径都返回 404，包括同目录下的其它文件。registry 用 file service 的
`file` 字段记录这个收窄；没有该字段的条目就是目录 share。

两种形态都不增加 HTTP 调用方限制：tailnet 策略决定谁能连接，而且
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

## 照片和视频上传

为完整上传请求选择足够大的有限上限, 包括 multipart 元数据。例如:

```bash
tslink add photos --proxy localhost:2283 --max-request-body 20GiB --request-read-timeout 2m
tslink share 2283 --name photos --max-request-body 20GiB --request-read-timeout 2m
tslink status --urls
tslink list --verbose
```

未配置的服务使用 32 MiB 大小上限、10s 请求头超时、30s 上传无进展窗口和
60s keep-alive 空闲超时。用 `--request-header-timeout` 调整请求头窗口,
用 `--idle-timeout` 调整 keep-alive。明确选择移除大小上限时, 同时提供
`--max-request-body unlimited --ack-unlimited-request-body`。

上传通过 reverse proxy 直接流向后端, TSLink 不将整个上传缓存在内存中。
每次读取请求体时更新无进展 deadline; 等待后端接收上一块数据的时间不计入,
持续读取成功就可继续上传, 没有总时长截止。响应流和 WebSocket 升级不受上传
截止时间影响。手机持续发送数据时可以超过 30 秒; 停止进展则收到明确的 408。
大小超限返回 413, 包括长度未知的 chunked 请求。
HTTP/1 请求头不完整且超时, 在 handler 启动前返回 408。

服务所有者会看到带服务名和限制的结构化日志, `status --urls` 与
`list --verbose` 显示 runtime warning, `doctor` 指出应调整的标志。
warning 保留至服务节点重启。修改限制时用完整配置重新执行 add, 因为未重复的
标志会重置; share 不复用有效限制不同的服务。失败上传可能已向后端发送部分
数据, 后端需要处理它; 应用自身的限制仍然有效。

CLI JSON 返回以字节数和 duration 字符串表示的有效 `request_limits`。
MCP add/share 接受 `request_limits` 对象, 字段为 `max_body`、`unlimited_ack`、
`header_timeout`、`read_timeout` 和 `idle_timeout`。
应用 recipe 可调用 `registry.RecommendedUploadLimits()`, 为 Immich、Nextcloud、
Jellyfin 推荐 20 GiB 上限和 2m 无进展窗口。recipe 接线独立完成, 仍需考虑应用自身要求。
