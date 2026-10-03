# Atomic file persistence

- `atomicfile.go`: permission checks, exclusive temporary files, write/sync/close, replace and cleanup; `WriteFileWithReplace` retries the same prepared source using the caller's bounded policy.
- `replace_retry_test.go`: prepared-source reuse, caller policy invocation, durable replacement and preparation failure.
- `shared_windows.go`, `shared_other.go`: `OpenSharedRead` (read/write/delete sharing on Windows) and `ReplaceFile` (`FileRenameInfoEx` with POSIX semantics, `os.Rename` fallback); shared by the registry and the Windows supervisor state writer. `WriteFileInExistingDirWithReplace` in `atomicfile.go` pairs parent validation with a caller-owned replace step.
- `shared_test.go`, `shared_windows_test.go`: caller retry of one prepared source, failure preservation, held-snapshot replacement with an `os.Rename` control, legacy-reader blocking, unsupported-class fallback and long Unicode paths.
- `atomicfile_test.go`, `atomicfile_parent*_test.go`: existing I/O failure, permissions, symlink and junction tests.
- `owner_*.go`: platform ownership, permissions and directory-sync behavior.
- `settle.go`: `ReadSettled` rereads a Windows not-exist result while this package's temporary file for the target is present (a replacement is in progress), bounded at 255 ms of sleep; `ReplacementInProgress` recognises only `randomTempName` temporaries.
- `settle_test.go`: genuine absence, in-progress wait, reappeared target, stale-temporary bound and unchanged non-not-exist errors, all with injected sleeps; `ConvergePrivateFile` treats a name that vanishes between Lstat and chmod as missing.
