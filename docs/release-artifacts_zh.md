# 发布产物

当前还没有公开 tag/release，Homebrew tap 也尚未发布可安装产物。首个公开
release/readback 前请从源码安装。该外部 gate 通过后，GitHub Releases 预计发布以下可安装产物：

稳定版发布且 `anydoor7/homebrew-tap` 仓库已有 cask 后，macOS 可以使用以下命令安装：

```bash
brew install --cask anydoor7/tap/tslink
```

| 平台 | 产物 | 说明 |
|---|---|---|
| macOS | Homebrew cask 和 `tar.gz` 归档 | Homebrew cask 使用 GoReleaser `skip_upload: auto`，pre-release tag 可以跳过 tap upload 且不让发布失败。预发布验证优先使用归档产物。稳定版 macOS 二进制带 Developer ID 签名并经 Apple notarize，无论通过 cask 还是下载归档安装，Gatekeeper 都直接放行，前提是首次运行能联网向 Apple 查 notarization 票据（裸二进制无法 staple 票据）。pre-release tag 可能发出未签名归档，Gatekeeper 会拦，只用于验证。 |
| Linux | `.deb`、`.rpm` 和 `tar.gz` 归档 | 包内包含原生 `tslink` 二进制。安装后用 `tslink install` 注册 user service。 |
| Windows | `.zip` 归档 | Windows 当前是 archive-only 支持。尚未提供 MSI/MSIX/Winget 包或 Windows 代码签名安装器。解压后用 `tslink install` 注册 Startup 自启动。 |

发布产物是并列的 release assets，不是嵌入归档内部的文件。GoReleaser 会上传可安装归档/包、`checksums.txt`、归档对应的 CycloneDX SBOM sidecar，以及 `checksums.txt` 和 SBOM sidecar 的 keyless Sigstore bundle 签名。签名后的 `checksums.txt` 覆盖可安装产物和 SBOM sidecar。release workflow 还会为可安装产物和供应链 sidecar 发布 GitHub artifact attestations。

### CI 分层与发布门槛

推送到 `main` 和 tag 始终运行基于精确 SHA 的完整三平台 Release Candidate gate；`release.yml` 只有在它成功后才发布。PR 根据修改路径选择 tier：draft 暂缓重检查，纯文档修改不运行 Go jobs，普通 Go 修改运行所有 Linux 托管的检查，平台敏感修改还运行 macOS/Windows 原生测试与编译后二进制契约。`ci:full` label 强制非 draft PR 使用 full tier。tier job summary 会列出选择及原因。未知路径或缺少 diff 证据时选择 full；删除平台文件或 build constraints 也选择 full。

仓库 Actions variable `CI_PR_TIER_MODE` 默认为 `tiered`。设为 `full` 后,所有非 docs-only 的 ready PR 运行 full; draft 和 docs-only PR 仍保持轻量。非法值打印 warning 并选择 full。Main/tag 始终为 full。**仓库公开后,owner 会立即设置 `CI_PR_TIER_MODE=full`。** 权威分类器从不可变的 base checkout 执行,PR tree 仅作为 Git 数据读取;提议的 policy tests 在独立 job 中运行。Base 尚无分类器时 bootstrap 选择 full。Docs 仅允许没有 Go 源码引用的根目录或 `docs/` Markdown,以及 `docs/assets/` 图片。从 merge-base、base 和 head 的 Git 对象读取所有 `*.go` 文件原始字节,包括测试和 Darwin-only 文件。规范化路径、basename 或区分大小写的 stem 作为子串出现时选择 full。Stem 是 basename 第一个 `.` 或 `_` 之前的部分: `README.ja.md` 对应 `README`, `platforms_zh.md` 对应 `platforms`。注释和单词内部匹配也算引用。为覆盖所有平台上的文档契约,Go 源码引用的文档修改(包括 README)现在运行 full;正确性优先于 Actions minutes 节省。 Embed 资产、可执行文件、build attributes 和 runtime/test fixtures 仍被排除。GoReleaser 和 release-candidate workflow 原始字节中出现的 docs 路径或 basename 保守选择 full; docs/Markdown 或 payload key 所在行含 glob 时排除所有 docs 候选。Merge-base/base/head 任意位置存在 symlink 时选择 full。路径和 embed 匹配去掉开头的 `./`,检查不确定时选择 full。完整中文规则见 [贡献指南](../CONTRIBUTING_zh.md)。

所有发布目标仍接受静态分析、交叉编译与漏洞检查。合并 jobs 后，各目标的失败仍会让 job 失败，artifact 名称保持一致。始终运行的 `gate` 汇总会拒绝失败、取消和意外跳过，只有当前 tier 排除的检查可以跳过。仓库公开后，branch protection 应只要求这个汇总 check（先在 hosted run 核实 reusable caller 显示的 `Release candidate gate / gate` context）。本次 workflow 修改不配置 branch protection。分类规则和本地策略测试见 [Contributing](../CONTRIBUTING.md#continuous-integration)。

发布产物、checksum、签名、SBOM 和 attestation 的验证步骤见[验证发布产物](verify-release_zh.md)。
