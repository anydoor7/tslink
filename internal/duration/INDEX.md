# Duration parsing

- `duration.go`, `value.go`: unchanged operational Go-plus-days parser and Cobra value.
- `lifetime.go`: relative/absolute/never access lifetimes and shared audience policy; explicit clock input.
- `duration_test.go`: operational grammar and printed round trips.
- `lifetime_test.go`: grammar, policy, DST, leap days and real TZ subprocess tests.
- `testmain_test.go`: test environment isolation.
