# People sharing files

This index records the people sharing lifecycle implementation and regression tests in this directory. Other package files are described by the repository architecture guide.

- [invites.go](invites.go)
- [membership.go](membership.go): Complete Users directory membership classification; unknown and unavailable fail closed.
- [membership_test.go](membership_test.go): Real loopback SDK directory requests, shared users, offline members and incomplete-directory controls.
- [people_invites.go](people_invites.go)
- [people_invites_test.go](people_invites_test.go): Remote ownership, list, partial body, accepted share and DELETE failure probes.
- [review3_lists_test.go](review3_lists_test.go): complete-list or UTF-8 boundary regression evidence.
