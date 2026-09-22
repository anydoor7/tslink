# Command test fixtures

Captured-format output used by the command regression tests. Read this directory when
updating a parser for an external tool's output, or the fixture that pins its format.
These files carry fixture bytes and their provenance only; none of them contains a
credential, and no test here validates a real supervisor installation.

- [launchctl/README.md](launchctl/README.md): synthetic `launchctl print` and
  `launchctl print-disabled` captures, plus their provenance and checksums.
