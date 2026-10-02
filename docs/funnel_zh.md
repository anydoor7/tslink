# 公开 Funnel 期限

Funnel 将 HTTP 代理公开到互联网,须显式 `--public` 或 MCP `public_ack`。
公开访问不执行人员登录或 allow-list 校验。

```sh
tslink add preview --proxy localhost:3000 --funnel --public --funnel-ttl 90m
tslink add preview --proxy localhost:3000 --funnel --public --funnel-ttl 'until 2030-06-01T18:00:00Z'
tslink extend preview --for 3d
tslink extend preview --for 1h --regrant
```

[统一时长语法和策略](durations_zh.md)接受相对或绝对期限。最短 1h,默认 24h,
默认最长 7d (168h)。推荐 1h、8h、24h、3d、7d。`72h` 仍有效,新公开请求
拒绝 `never`。拥有者可在 `config.json` 设置
`{"durations":{"public_max":"14d"}}`;上限低于默认时须显式提供范围内期限。
MCP `share`、`add`、`recipe_plan`、`recipe_apply` 以及 recipe CLI 使用同一解析器和策略。

`extend` 设为操作时刻加时长,可延长也可缩短;`--until` 设置绝对期限。
已到期 TTL 要求 `--regrant`,也能恢复已由 cleanup 降级为私有的、曾确认公开访问的
Funnel。期限仍在未来且被操作者关闭的 Funnel 不能由此恢复。
结果始终使用版本化 JSON envelope,记录前后两个期限。

持久化 `funnel_expires_at` 仍为绝对 UTC 时间戳,cleanup 降级为私有时保留该值。
历史显式 `"never"` 条目仍可读取,省略新 TTL 时无损保留;可用 `extend` 改为
有限期限。不执行有损迁移。已有 `add` 省略期限保留旧值;重试已过期 add 和重复
share 保留原重新开启语义,并受当前期限策略约束。

Funnel 策略配置及 `--no-auto-provision` 行为保持不变。有限期限不能收回已交付数据,
也不强制关闭已接受的流。TSLink 与 Tailscale 配合工作,是独立项目。
