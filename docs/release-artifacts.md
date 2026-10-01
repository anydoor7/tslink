# Release Artifacts

There is no public tag/release or populated Homebrew tap yet. Before the first
published release/readback, install from source. After that external gate
passes, GitHub Releases are expected to publish these installable artifacts:

After a stable release is published and the `anydoor7/homebrew-tap` repository is populated, install the macOS cask with:

```bash
brew install --cask anydoor7/tap/tslink
```

| Platform | Artifacts | Notes |
|---|---|---|
| macOS | Homebrew cask and `tar.gz` archives | The Homebrew cask uses GoReleaser `skip_upload: auto`, so pre-release tags can skip tap upload without failing the release. Use the archives for pre-release validation. Stable macOS binaries are signed with a Developer ID certificate and notarized by Apple; Gatekeeper runs them directly, whether they arrive through the cask or a downloaded archive, provided the first run can reach Apple to check the notarization ticket (a bare binary cannot carry a stapled ticket). Pre-release tags may ship unsigned archives: Gatekeeper blocks those, so use them only for validation. |
| Linux | `.deb`, `.rpm`, and `tar.gz` archives | Packages contain the native `tslink` binary. The first `tslink add` registers and starts the user service automatically. |
| Windows | `.zip` archives | Windows support is archive-only today. There is no MSI/MSIX/Winget package or Windows code-signed installer yet. Use `tslink install` from the extracted binary to register Startup autostart. |

Release assets are side-by-side files, not files embedded inside the archives. GoReleaser uploads installable archives/packages, `checksums.txt`, CycloneDX SBOM sidecars for archives, and keyless Sigstore bundle signatures for `checksums.txt` and SBOM sidecars. The signed `checksums.txt` covers both installable artifacts and SBOM sidecars. The release workflow also publishes GitHub artifact attestations for the installable artifacts and supply-chain sidecars.

See [Verify a release](verify-release.md) for artifact, checksum, signature, SBOM, and attestation checks.
