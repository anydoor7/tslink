package authmode

import (
	"os"
	"runtime"
	"testing"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
)

func TestCredentialUpgradeMarkerRoundTrip(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())

	pending, err := CredentialUpgradePending()
	if err != nil {
		t.Fatalf("CredentialUpgradePending() error = %v", err)
	}
	if pending {
		t.Fatal("CredentialUpgradePending() = true before marker is written")
	}

	if err := MarkCredentialUpgradePending(); err != nil {
		t.Fatalf("MarkCredentialUpgradePending() error = %v", err)
	}
	pending, err = CredentialUpgradePending()
	if err != nil {
		t.Fatalf("CredentialUpgradePending() error = %v", err)
	}
	if !pending {
		t.Fatal("CredentialUpgradePending() = false after marker is written")
	}

	path, err := CredentialUpgradePath()
	if err != nil {
		t.Fatalf("CredentialUpgradePath() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(marker) error = %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != atomicfile.PrivateFileMode {
			t.Fatalf("marker mode = %o, want %o", got, atomicfile.PrivateFileMode)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("marker mode = %v, want regular file on Windows", info.Mode())
	}

	if err := ClearCredentialUpgradePending(); err != nil {
		t.Fatalf("ClearCredentialUpgradePending() error = %v", err)
	}
	pending, err = CredentialUpgradePending()
	if err != nil {
		t.Fatalf("CredentialUpgradePending() error = %v", err)
	}
	if pending {
		t.Fatal("CredentialUpgradePending() = true after marker is cleared")
	}
}
