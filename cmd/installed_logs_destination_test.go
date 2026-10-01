package cmd

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/logging"
)

func TestInstalledDaemonLogsReachCLIReader(t *testing.T) {
	t.Setenv("TSLINK_MANAGED_LOGS", "1")
	assertManagedDaemonLogsReachCLIReader(t)
}

func assertManagedDaemonLogsReachCLIReader(t *testing.T) {
	t.Helper()
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	old := slog.Default()
	t.Cleanup(func() { slog.SetDefault(old) })
	logging.Init(false)
	slog.Info("managed daemon log destination control")
	dir, err := config.LogDir()
	if err != nil {
		t.Fatal(err)
	}
	path, err := resolveLogFilePath(dir, "err")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := tailFile(path, 10, "INFO")
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "managed daemon log destination control") {
		t.Fatalf("logs reader must see installed daemon output at %s: lines=%v err=%v", path, lines, err)
	}
}

// Windows Startup cannot be executed on this host. Pin both platform
// renderers' opt-in and exercise the shared producer/consumer above.
func TestInstallersSelectManagedLogDestination(t *testing.T) {
	for file, want := range map[string]string{
		"install_linux.go":   "Environment=TSLINK_MANAGED_LOGS=1",
		"install_windows.go": `shell.Environment("Process")("TSLINK_MANAGED_LOGS") = "1"`,
	} {
		data, err := os.ReadFile(filepath.Join(".", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), want) {
			t.Errorf("%s must select the same managed logs destination: missing %q", file, want)
		}
	}
}
