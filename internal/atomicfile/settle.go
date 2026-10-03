package atomicfile

import (
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Native Windows runs showed that while ReplaceFile swaps a target that
// shared-delete readers hold, a fresh by-name open can briefly fail with
// ERROR_FILE_NOT_FOUND. Callers that map "missing" to "empty" would then
// report a configured file as absent. ReadSettled tells that window apart from
// genuine absence by the writer's own evidence: this package's temporary file
// for the target exists from before replacement until replacement consumes it.

// settleAttempts bounds ReadSettled: eight sleeps of 1..128 ms (255 ms total).
const settleAttempts = 9

var settleSleepFn = time.Sleep

// ReadSettled runs read once on non-Windows platforms. On Windows, when read
// reports that path does not exist, it reads again while one of this
// package's temporary files for path is present (a write or replacement is in
// progress), or immediately if path has reappeared. Absence with no such
// temporary file is returned as genuine. Other errors return unchanged. A
// temporary file left by a crashed writer delays a genuine not-exist result
// by at most the bounded sleep budget.
func ReadSettled(path string, read func() error) error {
	if runtime.GOOS != "windows" {
		return read()
	}
	return readSettled(path, read, settleSleepFn)
}

func readSettled(path string, read func() error, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		err := read()
		if err == nil || !errors.Is(err, fs.ErrNotExist) || attempt == settleAttempts-1 {
			return err
		}
		if !ReplacementInProgress(path) {
			if _, statErr := lstatFn(path); statErr != nil {
				return err
			}
			// Replaced between the read and the scan: read the new file now.
			continue
		}
		sleep(time.Millisecond << attempt)
	}
}

// ReplacementInProgress reports whether a temporary file created by this
// package's writers for path is present in path's directory.
func ReplacementInProgress(path string) bool {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return false
	}
	base := filepath.Base(path)
	for _, entry := range entries {
		if isOwnedTempName(entry.Name(), base) {
			return true
		}
	}
	return false
}

// isOwnedTempName matches randomTempName: "." + base + "." + 32 hex + ".tmp".
func isOwnedTempName(name, base string) bool {
	prefix := "." + base + "."
	if len(name) < len(prefix)+len(".tmp") || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	middle := name[len(prefix) : len(name)-len(".tmp")]
	if len(middle) != 32 {
		return false
	}
	_, err := hex.DecodeString(middle)
	return err == nil
}
