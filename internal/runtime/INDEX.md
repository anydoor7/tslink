# Runtime evidence

- `snapshot.go`: daemon snapshots and fingerprints, including the independent portal state.
- `portal_test.go`: portal configuration invalidates stale runtime evidence; canonical app URL normalization/refusal.
- Other ownership and snapshot modules are described by the repository architecture guide.

- Runtime snapshots and ownership reads use shared state-file reads; snapshot saves use the common prepared-file and replacement helpers.
