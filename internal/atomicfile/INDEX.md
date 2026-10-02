# Atomic file persistence

- `atomicfile.go`: permission checks, exclusive temporary files, write/sync/close, replace and cleanup; `WriteFileWithReplace` retries the same prepared source using the caller's bounded policy.
- `replace_retry_test.go`: prepared-source reuse, caller policy invocation, durable replacement and preparation failure.
- `atomicfile_test.go`, `atomicfile_parent*_test.go`: existing I/O failure, permissions, symlink and junction tests.
- `owner_*.go`: platform ownership, permissions and directory-sync behavior.
