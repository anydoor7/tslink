# Atomic file writes

- `atomicfile.go`: private state-file validation, atomic replacement and typed post-publication errors.
- `atomicfile_test.go`: real files, pre-publication failures, cleanup and permissions.
- `publication_test.go`: published bytes, wrapped sync errors and durable recovery.
- `sync_test_seam.go`: isolated directory-sync failure injection.
- `owner_unix.go`, `owner_windows.go`: platform ownership validation.
- `atomicfile_parent_unix_test.go`, `atomicfile_parent_windows_test.go`: platform parent permissions.
- `atomicfile_parent_junction_test.go`, `atomicfile_parent_junction_windows_test.go`: symlink and junction validation.
- `testmain_test.go`: isolated test process setup.
- `rename_unix.go`, `rename_windows.go`: bounded Windows retries for transient replacement sharing failures.
- `rename_windows_test.go`: real held-reader replacement, persistent failure preservation and exact retry bounds.
- `read_unix.go`, `read_windows.go`: snapshot reads with bounded Windows sharing retries.
- `read_windows_test.go`: real exclusive-handle read failure and recovery, permanent errors and retry bounds.
