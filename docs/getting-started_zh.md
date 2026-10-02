# 入门

## 开始前

准备 Go 1.26.6+ 和 Git，以及一个已
[启用 MagicDNS 与 HTTPS](https://tailscale.com/docs/how-to/set-up-https-certificates)
的 Tailscale 账户。接收设备需要登录你的 tailnet，并由网络策略允许连接。
发布端的 TSLink 已内嵌 Tailscale，无需另装 Tailscale 应用。

## 安装

下面的安装和页面创建示例使用 **bash 或 zsh**。
Windows 用户请参阅[平台支持](platforms_zh.md)。

尚未发布预构建安装包或 Homebrew cask。请从源码安装：

```bash
git clone https://github.com/anydoor7/tslink.git
cd tslink
go install .
export PATH="$PATH:$(go env GOPATH)/bin"
```

## 分享第一个页面

TSLink 可以直接提供页面访问，无需另外运行 Web server：

```bash
mkdir -p tslink-demo
printf '<h1>Hello from TSLink</h1>\n' > tslink-demo/index.html
tslink share ./tslink-demo --name demo
```

首次使用时，`share` 可能输出 Tailscale 节点授权地址。打开它完成授权；
若 tailnet 开启了设备审批，还需要管理员批准新节点。
然后获取服务地址，在允许访问的 tailnet 设备上打开：

```bash
tslink url demo --wait
```

首次分享无需 API token。`share` 会按需安装并启动后台服务。
Linux 需要可用的 systemd 用户服务管理器；手动运行方式见
[守护进程生命周期](daemon-lifecycle_zh.md)。

## 更多示例

下列 proxy 示例要求本机已有应用监听相应端口。

```bash
# 暴露文件目录
tslink add documents --dir ~/Documents

# 通过 TCP 代理暴露数据库
tslink add mydb --tcp localhost:5432

# 临时节点（停止后自动从 tailnet 移除）
tslink add demo --proxy localhost:8080 --ephemeral

# 基于身份的 HTTP 访问控制（仅 proxy/file）
tslink add internal --proxy localhost:9090 --allow user@example.com,tag:admin

# 通过 Tailscale Funnel 公开暴露（必须显式确认）。
# 默认公开 24h；--funnel-ttl 1h|8h|24h|72h|7d|never
# （never 在 registry 里存为 "funnel_expires_at": "never"）
tslink add public --proxy localhost:3000 --funnel --public

# ACL 标签
tslink add api --proxy localhost:8000 --tags tag:webserver,tag:production
```

Funnel ACL 自动配置默认开启，只作用于已确认公网暴露的服务。它可能在 tailnet policy file 中写入共享的 `tag:tslink-funnel` tag owner 和 `nodeAttrs` Funnel grant。给 `tslink add --funnel`、`tslink serve` 或 `tslink install` 传 `--no-auto-provision`，可关闭对应服务或 daemon 设置路径的自动配置；此时 tailnet policy 必须已包含所需规则。普通 tag ACL 写入仍需要 `--manage-acl`。
