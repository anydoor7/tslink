package atomicfile

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

var renameAttemptFn = os.Rename

// Windows can refuse replacement while a short-lived reader has the target
// open. Retry only sharing/access failures, keeping the old file and temp file
// intact. Persistent permission failures remain errors after at most 240 ms
// of waiting; the caller removes its unpublished temp file.
func renameFile(oldpath, newpath string) error {
	return retryRename(oldpath, newpath, renameAttemptFn, time.Sleep)
}

func retryRename(oldpath, newpath string, rename func(string, string) error, wait func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		err := rename(oldpath, newpath)
		if err == nil || attempt == 24 || !(errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION)) {
			return err
		}
		wait(10 * time.Millisecond)
	}
}
