# Changelog

All notable changes to TSLink are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - unreleased

Initial public release.

### Security

- `tslink share <file>` now serves only that one file. The registry records
  the served file in a new `file` field; the daemon answers `GET /<file>`,
  redirects `GET /` to it, and returns 404 for every other path, so sibling
  files and directory listings are no longer reachable through a single-file
  share. Shares created by earlier releases have no `file` field and keep
  serving their parent directory until they are shared again; re-run
  `tslink share <file>` for any existing single-file share you rely on.
- The daemon's own log files are created 0600, an existing wider log is
  narrowed on the next start, and rotated archives never carry more than
  owner-only permissions. Logs contain access records, invite recipients and
  authorization URLs.
- `tslink remove` deletes the service's local node identity directory once the
  remote device deletion is confirmed and no daemon holds that node; the
  daemon's reconcile pass does the same for orphaned identities it can prove
  stale. Directories left behind by earlier releases are not swept.
- Proxy and TCP targets that point at a link-local address (`169.254.0.0/16`,
  `fe80::/10`), an unspecified address (`0.0.0.0`, `::`), the
  `metadata.google.internal` hostname, or a non-canonical numeric spelling of
  such an address (`0xA9FEA9FE`, `169.254.43518`, ...) are now refused at
  registration with the stable error code `link_local_target_refused`.
  Registered services that already target such an address are excluded when
  the daemon loads the registry and reported as a service issue with the same
  code by `tslink doctor` and `tslink status`; if an entry such as
  `http://169.254.10.10:80` was a deliberate directly-attached device, change
  its target in `registry.json` to an address the daemon should reach instead.
  Validation never resolves DNS: a hostname that resolves to such an address
  (for example via a public wildcard DNS service) is still accepted, and this
  boundary is documented in SECURITY.md.
- `golang.org/x/crypto` moved to v0.57.0, past the `x/crypto/ssh` advisories
  GO-2026-6355, GO-2026-6354 and GO-2026-6303 (none were reachable from TSLink
  code).

### Added

- `.github/workflows/ci.yml` runs the Release Candidate gate on every pull
  request and on every push to `main`.

### Changed

- `add` now waits up to 30 seconds by default. Scripts that only need to
  register configuration should pass `--wait=0`.
- Windows reports `windows-startup` when its Startup registration matches the
  current config. `doctor` warns that crash restart is unavailable on that
  platform instead of asking for a reinstall that cannot fix it.
