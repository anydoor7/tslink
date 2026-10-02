# Atomic file writes

- `atomicfile.go`: private state-file validation, atomic replacement and typed post-publication errors.
- `atomicfile_test.go`: real files, pre-publication failures, cleanup and permissions.
- `publication_test.go`: published bytes, wrapped sync errors and durable recovery.
- `sync_test_seam.go`: isolated directory-sync failure injection.
- `owner_unix.go`, `owner_windows.go`: platform ownership validation.
- `atomicfile_parent_unix_test.go`, `atomicfile_parent_windows_test.go`: platform parent permissions.
- `atomicfile_parent_junction_test.go`, `atomicfile_parent_junction_windows_test.go`: symlink and junction validation.
- `testmain_test.go`: isolated test process setup.
