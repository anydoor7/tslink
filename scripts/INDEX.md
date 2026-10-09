
# Platform acceptance scripts

- [windows-supervision-smoke.ps1](windows-supervision-smoke.ps1)
- [windows-supervision-smoke-reader-test.ps1](windows-supervision-smoke-reader-test.ps1): isolated native reader and lifecycle-observer controls; no service installation.

- `check-portal-requests.cjs`: 390px light/dark request-card browser regression; consumes real listener fixtures.
- `windows-publish-preflight.py`: read-only stable-release App coverage and branch checks for the tap and Scoop bucket.
- `winget-manifests.py`: owner-run stable release checksum verification and winget 1.12.0 YAML generation.
- `winget-submit.sh`: owner-run fork sync and submission using existing gh authentication.
- `winget-manifests-test.py`, `winget-submit-test.py`: offline checksum/verification and fake-command submission controls; no remote writes.
