package atomicfile

import (
	"os"
	"time"
)

var readAttemptFn = os.ReadFile

// ReadFile retries the transient sharing failures that Windows can return
// while another process atomically replaces a state file. It preserves the
// final error and uses the same bounded wait as replacement itself.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retryWindows(func() (err error) {
		data, err = readAttemptFn(path)
		return err
	}, time.Sleep)
	return data, err
}
