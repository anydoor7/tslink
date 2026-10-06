# Release Artifacts

Stable releases, starting with v0.1.0, publish the installable artifacts below
on GitHub Releases and update the `anydoor7/homebrew-tap` cask. Installing from
source with Git and Go remains an alternative.

After a stable release is published and the `anydoor7/homebrew-tap` repository is populated, install with Homebrew on macOS or Linux:

```bash
brew install --cask anydoor7/tap/tslink
```

Keep the fully qualified name. Homebrew refuses casks from a third-party tap it does not trust, and naming the cask in full trusts it, so `brew tap anydoor7/tap` followed by `brew install tslink` stops with an untrusted-tap error. After `brew upgrade`, run `tslink install` again if TSLink runs as a background service: the service records the versioned path of the binary that installed it.

| Platform | Artifacts | Notes |
|---|---|---|
| macOS | Homebrew cask and `tar.gz` archives | The Homebrew cask uses GoReleaser `skip_upload: auto`, so pre-release tags can skip tap upload without failing the release. Use the archives for pre-release validation. Stable macOS binaries are signed with a Developer ID certificate and notarized by Apple; Gatekeeper runs them directly, whether they arrive through the cask or a downloaded archive, provided the first run can reach Apple to check the notarization ticket (a bare binary cannot carry a stapled ticket). Pre-release tags may ship unsigned archives: Gatekeeper blocks those, so use them only for validation. |
| Linux | Homebrew cask, `.deb`, `.rpm`, and `tar.gz` archives | The cask and packages contain the native `tslink` binary. The first `tslink add` registers and starts the user service automatically. |
| Windows | `.zip` archives | Windows support is archive-only today. There is no MSI/MSIX/Winget package or Windows code-signed installer yet, and Homebrew does not run on Windows itself (inside WSL 2 it installs the Linux binary). Run `tslink install` from the extracted binary to register a per-user scheduled task that starts TSLink at sign-in (`--startup` selects the Startup-folder fallback). |

Release assets are side-by-side files, not files embedded inside the archives. GoReleaser uploads installable archives/packages, `checksums.txt`, CycloneDX SBOM sidecars for archives, and keyless Sigstore bundle signatures for `checksums.txt` and SBOM sidecars. The signed `checksums.txt` covers both installable artifacts and SBOM sidecars. The release workflow also publishes GitHub artifact attestations for the installable artifacts and supply-chain sidecars.

The repository's [llms.txt](../llms.txt) stays with the online documentation rather
than in binary archives or deb/rpm packages: its links follow `main`, not a frozen
release manual. [server.json](../server.json) is an unpublished MCP Registry
preparation file, with no supported source-only Go package declared; it also stays
out of release payloads. JSON schema validity does not make it publishable. The
owner must choose a real distribution and explicitly authorize any Registry
submission. Neither file is executable configuration for TSLink.

### CI tiers and the release gate

Pushes to `main` and tags always run the full exact-SHA three-OS Release Candidate gate; `release.yml` publishes only after it succeeds. PRs choose a tier from changed paths: drafts defer heavy checks, docs-only changes run no Go jobs, ordinary Go changes run all Linux-hosted checks, and platform-sensitive changes also run macOS/Windows native tests and compiled contracts. The `ci:full` label forces the full tier on ready PRs. The tier job summary states the tier and reason. Unknown paths or missing diff evidence choose full; deleting OS files or build constraints also chooses full.

`CI_PR_TIER_MODE` is a repository Actions variable, defaulting to `tiered`. In `full` mode every ready non-docs PR runs full; drafts and docs-only PRs remain cheap. Invalid values warn and select full. Main and tags are always full. **The owner will set `CI_PR_TIER_MODE=full` immediately after the repository becomes public.** The authoritative classifier executes from the immutable base checkout, with PR trees read as data; proposed policy tests run separately. A base without a classifier bootstraps to full. Docs permits only root or `docs/` Markdown and images under `docs/assets/` that no Go source references. In merge-base, base and head, every `*.go` file (including tests and Darwin-only files) is scanned as raw Git bytes. A normalized path, basename, or case-sensitive stem substring selects full. The stem is the basename up to the first `.` or `_`: `README` for `README.ja.md`, `platforms` for `platforms.md`. Comments and partial-word matches count. Any change to a `*_zh.md` path selects full so the English-only documentation contract runs. This protects documentation contracts on every platform; Go-referenced documentation changes, including README edits, now run full. Correctness takes precedence over Actions-minute savings. Embedded assets, executable files, build attributes and runtime/test fixtures remain excluded. Raw GoReleaser and release-candidate workflow bytes conservatively exclude docs paths or basenames mentioned anywhere; docs/Markdown or payload-key glob lines exclude all docs candidates. Any symlink anywhere in merge-base/base/head selects full. Leading `./` is normalized for path and embed matching; uncertain inspection selects full. See the [contributor guide](../CONTRIBUTING.md) for the complete rules.

All release targets still receive static analysis, cross-build and vulnerability checks. Consolidated jobs preserve each target's failure and existing artifact names. The always-running `gate` aggregate rejects failures, cancellations and unexpected skips; only checks excluded by the selected tier may be skipped. Branch protection on `main` requires this single aggregate check, reported as `Release candidate gate / gate`. See [Contributing](../CONTRIBUTING.md#continuous-integration) for the classification rules and local policy tests.

See [Verify a release](verify-release.md) for artifact, checksum, signature, SBOM, and attestation checks.
