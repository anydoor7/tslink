package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/testenv"
)

// configDirFixture gives the process a fresh home whose TSLink config
// directory is in its default place inside it, with node state present.
func configDirFixture(t *testing.T) (home, configDir string) {
	t.Helper()
	home = t.TempDir()
	configDir = testenv.SetHome(t, home)
	if err := os.MkdirAll(filepath.Join(configDir, "nodes", "svc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "nodes", "svc", "tailscaled.state"), []byte("node key"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, configDir
}

func assertConfigDirRefusal(t *testing.T, err error, root, configDir string) {
	t.Helper()
	if err == nil {
		t.Fatalf("share root %q accepted, want %s", root, CodePathExposesConfigDir)
	}
	if code, _ := ErrorCode(err); code != CodePathExposesConfigDir {
		t.Fatalf("share root %q error code = %q (%v), want %s", root, code, err, CodePathExposesConfigDir)
	}
	if !strings.Contains(err.Error(), strconv.Quote(configDir)) {
		t.Fatalf("refusal %q does not name the config directory %q", err, configDir)
	}
}

// TestFileRootRefusesTSLinkConfigDirectory is A2's probe: a directory share
// serves its whole tree, so a root that is, contains, or lies inside the
// config directory exposes other services' node keys.
func TestFileRootRefusesTSLinkConfigDirectory(t *testing.T) {
	home, configDir := configDirFixture(t)
	link := filepath.Join(t.TempDir(), "config-link")
	if err := os.Symlink(configDir, link); err != nil {
		t.Fatal(err)
	}
	homeLink := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, homeLink); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"the config directory":         configDir,
		"a subdirectory of it":         filepath.Join(configDir, "nodes"),
		"its parent":                   filepath.Dir(configDir),
		"home with the default layout": home,
		"a symlink to it":              link,
		"a path through that symlink":  filepath.Join(link, "nodes"),
		"home through a symlink":       homeLink,
	} {
		t.Run(name, func(t *testing.T) {
			assertConfigDirRefusal(t, ValidateFileRoot(root), root, configDir)
			svc := Service{Name: "files", Type: TypeFile, Path: root}
			assertConfigDirRefusal(t, ValidateService(svc), root, configDir)
		})
	}

	unrelated := t.TempDir()
	if err := ValidateFileRoot(unrelated); err != nil {
		t.Fatalf("unrelated directory refused: %v", err)
	}
	if IsHomeDir(unrelated) {
		t.Fatalf("IsHomeDir(%q) = true", unrelated)
	}
}

// TestFileRootConfigDirCheckSeesThroughCase covers a case-insensitive file
// system, where another spelling names the same directory.
func TestFileRootConfigDirCheckSeesThroughCase(t *testing.T) {
	_, configDir := configDirFixture(t)
	upper := filepath.Join(filepath.Dir(configDir), strings.ToUpper(filepath.Base(configDir)))
	if _, err := os.Stat(upper); err != nil {
		t.Skipf("file system is case-sensitive (%v); nothing to check", err)
	}
	assertConfigDirRefusal(t, ValidateFileRoot(upper), upper, configDir)
}

// TestSingleFileShareInsideConfigDirIsRefused keeps a single-file share of a
// file inside the config directory out too, while a single file whose parent
// merely contains the config directory serves only that file and is allowed.
func TestSingleFileShareInsideConfigDirIsRefused(t *testing.T) {
	home, configDir := configDirFixture(t)
	if err := os.WriteFile(filepath.Join(configDir, "apikey"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := Service{Name: "key", Type: TypeFile, Path: configDir, File: "apikey"}
	assertConfigDirRefusal(t, ValidateService(inside), configDir, configDir)

	if runtime.GOOS != "windows" {
		// A symlinked name inside an allowed parent that resolves into the
		// config directory is the same exposure.
		if err := os.Symlink(filepath.Join(configDir, "apikey"), filepath.Join(home, "innocent.txt")); err != nil {
			t.Fatal(err)
		}
		linked := Service{Name: "linked", Type: TypeFile, Path: home, File: "innocent.txt"}
		assertConfigDirRefusal(t, ValidateService(linked), home, configDir)
	}

	notes := Service{Name: "notes", Type: TypeFile, Path: home, File: "notes.txt"}
	if err := ValidateService(notes); err != nil {
		t.Fatalf("single file in home refused: %v", err)
	}
}

// TestHomeRootAcceptedWhenConfigDirIsElsewhere: with TSLINK_CONFIG_DIR outside
// home, sharing home no longer exposes TSLink's state, so it is accepted and
// IsHomeDir lets the callers warn.
func TestHomeRootAcceptedWhenConfigDirIsElsewhere(t *testing.T) {
	home, _ := configDirFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "tslink-config")
	t.Setenv(config.ConfigDirEnv, elsewhere)
	if err := ValidateFileRoot(home); err != nil {
		t.Fatalf("home refused with the config directory elsewhere: %v", err)
	}
	if !IsHomeDir(home) {
		t.Fatalf("IsHomeDir(%q) = false", home)
	}
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	assertConfigDirRefusal(t, ValidateFileRoot(elsewhere), elsewhere, elsewhere)
}
