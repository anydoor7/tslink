//go:build windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"golang.org/x/sys/windows"
)

// Import only the observer functions; never run the service-installing script
// body. The actual private-state writer owns the prepared replacement below.
func TestSmokePIDObserverReplacement(t *testing.T) {
	source := os.Getenv("CONVERGENCE_SMOKE_SCRIPT")
	if source == "" {
		source, _ = filepath.Abs("../../scripts/windows-supervision-smoke.ps1")
	}
	root := t.TempDir()
	path := filepath.Join(root, "tslink.pid")
	probe := filepath.Join(root, "observer.ps1")
	const body = `param([string]$Source,[string]$Path,[switch]$ReplaceWindow)
$ErrorActionPreference='Stop'
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($Source,[ref]$tokens,[ref]$errors)
if($errors.Count){throw ($errors|Out-String)}
foreach($name in @('Read-SharedText','Read-PendingText','Get-NewDaemonPID')) {
  $nodes=@($ast.FindAll({param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name},$false))
  if($nodes.Count -eq 1){. ([scriptblock]::Create($nodes[0].Extent.Text))}
  elseif($name -ne 'Read-PendingText'){throw 'observer absent'}
}
if($ReplaceWindow) {
  function Test-Path([string]$Path) {
    $exists=Microsoft.PowerShell.Management\Test-Path $Path
    if($exists){[IO.File]::Delete($Path)}
    return $exists
  }
}
Write-Output ('PID='+(Get-NewDaemonPID $Path 0))
`
	if err := os.WriteFile(probe, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	observe := func(window bool, want string) {
		args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "RemoteSigned", "-File", probe, "-Source", source, "-Path", path}
		if window {
			args = append(args, "-ReplaceWindow")
		}
		out, err := exec.Command("powershell.exe", args...).CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != want {
			t.Errorf("observer window=%t want=%s err=%v output=%s", window, want, err, out)
		}
	}
	if err := WritePIDForProcess(path, 111); err != nil {
		t.Fatal(err)
	}
	observe(false, "PID=111")
	wide, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	observe(false, "PID=0") // A temporary sharing conflict is not a new PID.
	windows.CloseHandle(h)
	observe(false, "PID=111")
	err = atomicfile.WriteFileWithReplace(path, []byte("222\n"), func(temp, target string) error {
		if !atomicfile.ReplacementInProgress(path) {
			t.Fatal("actual writer temporary file absent")
		}
		observe(true, "PID=0") // Pin disappearance between stat and open.
		return atomicfile.ReplaceFile(temp, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	observe(false, "PID=222")
}

func TestPIDPublicationSharing(t *testing.T) {
	for _, identity := range []bool{false, true} {
		name := "pid"
		if identity {
			name = "identity"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tslink.pid")
			if err := WritePID(path); err != nil {
				t.Fatal(err)
			}
			target := path
			write := func() error { return WritePIDForProcess(path, os.Getpid()) }
			if identity {
				target = processIdentityPath(path)
				write = func() error { return WritePID(path) }
			}
			shared, err := atomicfile.OpenSharedRead(target)
			if err != nil {
				t.Fatal(err)
			}
			err = write()
			shared.Close()
			if err != nil {
				t.Fatalf("actual writer with held shared-delete %s reader: %v", name, err)
			}
			ordinary, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			err = write()
			ordinary.Close()
			if !atomicfile.IsWindowsSharingError(err) {
				t.Fatalf("ordinary held reader must reject replacement: %v", err)
			}
			if err := write(); err != nil {
				t.Fatalf("released-reader control: %v", err)
			}
			if pid, err := ReadPID(path); err != nil || pid != os.Getpid() {
				t.Fatalf("PID: %d %v", pid, err)
			}
			if record, err := readProcessIdentity(path); err != nil || record.PID != os.Getpid() {
				t.Fatalf("identity: %+v %v", record, err)
			}
		})
	}
}

func TestPIDReadersShareDeleteAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, processIdentityPath(path)} {
		wide, err := windows.UTF16PtrFromString(target)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.CreateFile(wide, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, oldErr := os.ReadFile(target)
		if !errors.Is(oldErr, windows.ERROR_SHARING_VIOLATION) {
			windows.CloseHandle(h)
			t.Fatalf("ordinary read control: %v", oldErr)
		}
		if target == path {
			_, err = ReadPID(path)
		} else {
			_, err = readProcessIdentity(path)
		}
		windows.CloseHandle(h)
		if err != nil {
			t.Errorf("actual shared reader %s: %v", filepath.Base(target), err)
		}
	}
}
