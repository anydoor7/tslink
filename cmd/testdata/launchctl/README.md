# launchctl fixtures

Synthetic captures in `launchctl print` and `launchctl print-disabled` output format,
used by the daemon supervision regressions in `../../supervision_r2_darwin_test.go`.
Read them when changing launchd state parsing or disabled-override parsing. They are
text only: no test in this repository runs a launchctl mutation against a real machine.

- `launchctl-print-prod.txt`: a `launchctl print` record for a running LaunchAgent,
  including the nested resource and jetsam coalition blocks that carry their own
  `state = active` lines. Parsers must read the top-level `state` and `pid`, not the
  first match anywhere in the record.
- `launchctl-print-disabled-prod.txt`: a `launchctl print-disabled` record with
  `=> enabled` and `=> disabled` values. Tests substitute a label and value into this
  structure to exercise TSLink overrides; the fixture on disk stays unchanged.

Every hostname, path, process identifier, environment variable, and service label in
both files is synthetic. The label list in the disabled fixture keeps the shapes a
parser can meet on a real host (team-identifier prefixes, underscores, hyphens, mixed
case, multi-segment reverse DNS) without describing any real machine.

SHA-256:

- launchctl-print-prod.txt: `92a4c744a48ee09d61e4eebf2bdc6f4f6dd4b04c4c0e00f25bda72d167cc615c`
- launchctl-print-disabled-prod.txt: `51e444f1ef142be2abdd5cc76c675a5be6a58c06bf8cd2f364918aa52d6541b1`
