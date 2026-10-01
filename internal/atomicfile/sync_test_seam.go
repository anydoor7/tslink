package atomicfile

// SetDirectorySyncForTest replaces the post-rename directory sync in an
// isolated test process. Call the returned function to restore it.
func SetDirectorySyncForTest(fn func(string) error) func() {
	previous := syncDirFn
	syncDirFn = fn
	return func() { syncDirFn = previous }
}
