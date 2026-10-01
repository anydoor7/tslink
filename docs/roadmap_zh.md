# 路线图

## 路线图

以下功能尚未进入已交付的运行路径：

| 领域 | 当前状态 |
|---|---|
| Docker 标签 | 未实现。 |
| Middleware | 未实现；目前没有对应的注册表字段或 flag。 |
| Admin dashboard / REST API | 没有交付 dashboard 或 REST handler；仅限 tailnet 的 MCP 控制面（`tslink serve --mcp`）是唯一的远程管理面。未来的 dashboard 或 REST 工作必须显式标为 experimental，并补端到端测试。 |
| Prometheus `/metrics` | 未实现；没有请求 instrumentation，也没有 scrape endpoint。 |
| Custom domain / ACME | 未实现；目前没有对应的注册表字段或 flag。 |
| Cluster sync | 未实现。 |
| 成员 portal 或服务目录 | 未实现。 |
| Marketplace 或第三方模板注册表 | 未实现。 |
| Docker 镜像 | 尚未发布。 |
| Headscale 端到端验证 | 待完成。 |
| 其他 Layer 2 模块 | 待集成测试。 |
