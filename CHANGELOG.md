# Changelog

All notable changes to TSLink are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - unreleased

Initial public release.

### Changed

- `add` now waits up to 30 seconds by default. Scripts that only need to
  register configuration should pass `--wait=0`.
- Windows reports `windows-startup` when its Startup registration matches the
  current config. `doctor` warns that crash restart is unavailable on that
  platform instead of asking for a reinstall that cannot fix it.
