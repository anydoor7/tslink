# Application recipes

- `catalog.json`: versioned app advice, default host targets, safety policies, fingerprints and dated official sources.
- `catalog.go`: immutable-copy catalog API, validation and `Recipe.HealthPath` recommendation data for future health integration.
- `detect.go`: bounded credential-free loopback HTTP discovery and registry correlation.
- `listen_parse.go`: portable OS output parsers and wildcard-to-loopback conversion.
- `listen_darwin.go`, `listen_linux.go`, `listen_windows.go`, `listen_other.go`: build-tagged OS inventory adapters.
- `recipes_test.go`, `listen_darwin_test.go`, `listen_linux_test.go`, `listen_windows_test.go`: controlled HTTP, listener, catalog and safety tests.
- `testmain_test.go`: shared isolated test environment.
