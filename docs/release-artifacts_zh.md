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

发布产物、checksum、签名、SBOM 和 attestation 的验证步骤见[验证发布产物](verify-release_zh.md)。
