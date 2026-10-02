# 贡献指南: CI

完整开发流程、贡献者权利和许可规则见 [CONTRIBUTING.md](CONTRIBUTING.md)。本页说明 CI 策略。

`main` 和 tag 始终运行基于精确 SHA 的三平台 full gate。PR 使用合并基点计算修改路径:

| Tier | 检查 |
|---|---|
| Draft | 分类和 gate 汇总; ready for review 后运行重检查。 |
| Docs | 分类和 gate 汇总; 不运行 Go jobs。 |
| Go | Linux 原生 build/vet/test/race/coverage/shuffle、gofmt/tidy、三平台 staticcheck、六目标 cross-build 和漏洞扫描、Linux compiled contracts、artifact、GoReleaser 和 manifest 检查,以及独立 policy tests。 |
| Full | Go tier 全部检查,加 macOS/Windows 原生测试和 compiled contracts、Darwin manifest 严格检查和三平台 manifest 比较。 |

Docs 仅允许根目录或 `docs/` 下的 Markdown,以及 `docs/assets/` 下的图片。由 `.goreleaser*` 和 release presence check 推导的发布 payload、embed 资产、可执行文件、`.gitattributes` 和 runtime/test fixtures 不属于 docs。修改路径在 merge-base、base 或 head tree 中为 symlink 时选择 full。Embed directives 从 base/head Git 数据读取,解析不确定时选择 full。

平台相关文件及包、install/supervision/daemon、`go.mod`、`go.sum`、`.github/**`、`tools/**` 和生成 manifest 选择 full。旧树、删除路径、base 分支新增的平台证据也会检查。未知路径或读取失败选择 full。`ci:full` label 强制 ready PR 使用 full; draft 仍暂缓重检查。

分类器只从独立的 PR base SHA checkout 执行,checkout 使用 `persist-credentials: false`; PR tree 仅作为 Git 数据读取。PR 提议修改的 classifier tests 在 go/full tier 的独立 job 中运行,不会决定权威 tier。Base 尚无分类器时,bootstrap 选择 full。Main/tag 直接选择 full。

仓库 Actions variable `CI_PR_TIER_MODE` 默认为 `tiered`。设为 `full` 后,所有非 docs-only 的 ready PR 都运行 full gate; draft 和 docs-only PR 仍保持轻量。`ci:full` 仍可让 ready docs PR 运行 full。非法值会打印 warning 并选择 full。Main/tag 始终为 full。**仓库公开后,owner 会立即设置 `CI_PR_TIER_MODE=full`。** 此变量不配置 branch protection。

`gate` 汇总要求当前 tier 的每个 job 成功,拒绝失败、取消和意外跳过。仓库公开后,branch protection 应要求单一汇总 check; 首次 hosted run 需核实显示的 `Release candidate gate / gate` context。各目标 artifact 名称及失败传播保持一致。`setup-go` 基于 `go.sum` 缓存模块和 build outputs,测试通过 `-count=1` 强制执行。

本地验证:

```bash
python3 -m unittest discover -s .github/scripts -p 'test_ci_tier.py' -v
GOTOOLCHAIN=go1.26.6 go test -count=1 ./internal/release/...
```
