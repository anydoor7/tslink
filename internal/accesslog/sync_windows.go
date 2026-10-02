package accesslog

// File.Sync flushes append contents on Windows. Directory handles cannot be
// flushed through os.File; this matches the existing atomicfile boundary.
func syncDirectory(string) error { return nil }
