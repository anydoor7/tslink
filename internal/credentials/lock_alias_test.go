package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
)

// RV-C: a keyring-enabled acquisition locks the account lock and then
// <config>/credentials.lock. When TSLINK_CONFIG_DIR names the account lock's
// directory through another spelling (here a symlink), both paths are one
// file; locking it on a second descriptor must not deadlock the process.
func TestCredentialLockThroughAliasedConfigDirectoryDoesNotSelfDeadlock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oldGate := credentialMutationGate
		credentialMutationGate = make(chan struct{}, 1)
		credentialMutationGate <- struct{}{}
		t.Cleanup(func() { credentialMutationGate = oldGate })
		t.Setenv("TSLINK_DISABLE_KEYRING", "0")
		base := t.TempDir()
		realDir := filepath.Join(base, "home", ".tslink")
		if err := os.MkdirAll(realDir, 0o700); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(base, "alias-home-.tslink")
		if err := os.Symlink(realDir, alias); err != nil {
			if runtime.GOOS != "windows" {
				t.Skipf("symlinks unavailable: %v", err)
			}
			alias = strings.ToUpper(realDir)
			actual, e := os.Stat(alias)
			real, re := os.Stat(realDir)
			if e != nil || re != nil || alias == realDir || !os.SameFile(actual, real) {
				t.Fatalf("case alias is not the same file: %v / %v", e, re)
			}
		}
		lockPath := filepath.Join(realDir, "credentials.lock")
		t.Cleanup(SetMutationLockPathForTesting(lockPath))
		t.Setenv(config.ConfigDirEnv, alias)

		start := time.Now()
		unlock, err := acquireCredentialMutationLock()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("aliased config directory: elapsed=%v err=%v", elapsed.Round(time.Millisecond), err)
		}
		defer unlock()
		if elapsed != 0 {
			t.Fatalf("aliased config directory took %v to lock, want immediate", elapsed)
		}
		// The shared file is still exclusively locked, not skipped altogether.
		other, err := os.OpenFile(filepath.Join(alias, "credentials.lock"), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		locked, err := tryLockCredentialFile(other)
		if err != nil {
			t.Fatal(err)
		}
		if locked {
			t.Fatal("a second descriptor locked the credential file while the transaction held it")
		}
	})
}
