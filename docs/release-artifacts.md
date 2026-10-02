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

### CI tiers and the release gate

Pushes to `main` and tags always run the full exact-SHA three-OS Release Candidate gate; `release.yml` publishes only after it succeeds. PRs choose a tier from changed paths: drafts defer heavy checks, docs-only changes run no Go jobs, ordinary Go changes run all Linux-hosted checks, and platform-sensitive changes also run macOS/Windows native tests and compiled contracts. The `ci:full` label forces the full tier on ready PRs. The tier job summary states the tier and reason. Unknown paths or missing diff evidence choose full; deleting OS files or build constraints also chooses full.

`CI_PR_TIER_MODE` is a repository Actions variable, defaulting to `tiered`. In `full` mode every ready non-docs PR runs full; drafts and docs-only PRs remain cheap. Invalid values warn and select full. Main and tags are always full. **The owner will set `CI_PR_TIER_MODE=full` immediately after the repository becomes public.** The authoritative classifier executes from the immutable base checkout, with PR trees read as data; proposed policy tests run separately. A base without a classifier bootstraps to full. Docs is limited to root/docs Markdown and `docs/assets/` images, excluding embedded assets, executable files, build attributes and runtime/test fixtures. Raw GoReleaser and release-candidate workflow bytes conservatively exclude docs paths or basenames mentioned anywhere; docs/Markdown or payload-key glob lines exclude all docs candidates. Any symlink anywhere in merge-base/base/head selects full. Leading `./` is normalized for path and embed matching; uncertain inspection selects full. See the [contributor guide](../CONTRIBUTING.md) for the complete rules.

All release targets still receive static analysis, cross-build and vulnerability checks. Consolidated jobs preserve each target's failure and existing artifact names. The always-running `gate` aggregate rejects failures, cancellations and unexpected skips; only checks excluded by the selected tier may be skipped. After the repository becomes public, branch protection should require this single aggregate check (verify the reusable caller's displayed `Release candidate gate / gate` context on a hosted run). This workflow change does not configure protection. See [Contributing](../CONTRIBUTING.md#continuous-integration) for the classification rules and local policy tests.

See [Verify a release](verify-release.md) for artifact, checksum, signature, SBOM, and attestation checks.
