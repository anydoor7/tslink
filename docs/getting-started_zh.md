# 入门

## 安装

需要 Go 1.26.6 或更高版本。

```bash
go install github.com/anydoor7/tslink@latest

# 二进制装到 $(go env GOPATH)/bin，该目录默认不在 PATH 中：
export PATH="$PATH:$(go env GOPATH)/bin"
```

Homebrew 与预构建归档随首个 tagged release 提供。在那之前，源码安装是受支持的路径。
从 clone 构建同样可行：

```bash
git clone https://github.com/anydoor7/tslink.git && cd tslink && go install .
```

## 更多示例

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

