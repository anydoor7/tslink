# Shared traffic contract fixtures

- `traffic-events.json`: synthetic schema-1 events, in deliberately reversed time order. Includes direct/gateway and laptop/phone identity merging, ASCII-only canonicalization, quoted and Unicode identities, human/public/tagged/unknown buckets, denied-only access, HTTP upgrade, old out-only data, TCP open/close/gate drops and guest/MCP/lifecycle receipts. No real accounts or network calls.
- `traffic-expected.json`: independently specified `summary.traffic` for all events, without filters; also expected with any event limit. HTTP: 13 requests, 10 allowed, 3 denied, 76 bytes in, 135 bytes out. Mixed legacy summary: 21 events, 15 allowed, 6 denied.

Consumers should load the event array without rewriting it. Go tests compact each
element to a line in a temporary `access-log/2030-01-02-000000.jsonl` and call
`Query`. TypeScript/Python consumers should compare their aggregation to the
expected traffic JSON, including null timestamps and empty/non-human login
semantics. Human rows sort by `(subject_kind, login, app)` using ordinal UTF-8
order, with no Unicode folding. Missing `bytes_in` uses the schema-1 numeric zero
default in this fixture; it does not assert that an older ledger's unavailable
upload history can be reconstructed as zero.

Empty histories and read failures are exercised separately by Go tests; the
nonempty fixture serves as their positive control. Expected output must not be
regenerated from the implementation under test.
