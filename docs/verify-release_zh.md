# 验证 TSLink 发布产物

#### 验证发布完整性

下面命令需要 `gh` 2.49 或更新版本并支持 `gh attestation verify`，`cosign` 支持 `verify-blob --bundle`，以及 `sha256sum` 或 `shasum`。将 `<version>` 替换为 GitHub Release tag，将 `<artifact>` 替换为该 release 里的产物文件名。

Sigstore 证书的信任根是 [GitHub Actions OIDC issuer](https://token.actions.githubusercontent.com)。验证会钉住本仓库指定 tag 的精确 release workflow 身份（下方命令用 `$repo` 和 `$version` 组成），以及 GitHub attestation signer workflow `github.com/anydoor7/tslink/.github/workflows/release.yml`。tag ref 绑定要求匹配的签名或 attestation 来自该 tag 的 release workflow。

```bash
set -euo pipefail

repo="anydoor7/tslink"
version="<version>"
artifact="<artifact>"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

mkdir -p "tslink-$version-verify"
cd "tslink-$version-verify"

gh release download "$version" --repo "$repo" \
  --pattern "$artifact" \
  --pattern "checksums.txt" \
  --pattern "checksums.txt.sigstore.json"

require_file "$artifact"
require_file "checksums.txt"
require_file "checksums.txt.sigstore.json"
verify_checksum "$artifact"

cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$artifact" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```

归档 SBOM sidecar 是独立 release asset，需要单独验证。请在归档验证后的同一个验证目录中运行，使用相同的 `version` 和 `artifact`。

```bash
set -euo pipefail

repo="anydoor7/tslink"
version="<version>"
artifact="<artifact>"
sbom="$artifact.sbom.json"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    echo "missing checksum tool: install sha256sum or shasum" >&2
    exit 1
  fi
}

require_file() {
  if [ ! -f "$1" ]; then
    echo "missing downloaded release asset: $1" >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  require_file "$file"
  require_file "checksums.txt"

  expected="$(awk -v file="$file" '$2 == file {print $1}' checksums.txt)"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $file in checksums.txt" >&2
    exit 1
  fi

  actual="$(sha256_file "$file")"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "actual:   $actual" >&2
    exit 1
  fi
}

gh release download "$version" --repo "$repo" \
  --pattern "$sbom" \
  --pattern "$sbom.sigstore.json"

require_file "$sbom"
require_file "$sbom.sigstore.json"
verify_checksum "$sbom"

cosign verify-blob "$sbom" \
  --bundle "$sbom.sigstore.json" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --certificate-identity "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"

gh attestation verify "$sbom" \
  --repo "$repo" \
  --source-ref "refs/tags/$version" \
  --signer-workflow "github.com/$repo/.github/workflows/release.yml"
```
