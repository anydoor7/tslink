//go:build windows

package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/daemon"
	"golang.org/x/sys/windows"
)

var (
	windowsSchedulerFn     = callWindowsScheduler
	windowsSIDFn           = currentWindowsSID
	windowsTaskOwnsPIDFn   = daemon.IsTaskOwnedProcess
	windowsDaemonRunningFn = daemon.IsRunning
)

func currentWindowsSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return u.User.Sid.String(), nil
}

func windowsPowerShellPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func windowsTaskName() (string, error) {
	sid, err := windowsSIDFn()
	return "TSLink-" + sid, err
}

func windowsTaskPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA is not set")
	}
	return filepath.Join(appData, "tslink-supervisor", "task.xml"), nil
}

// Both methods share one lock location, including during migration.
func supervisorPath() (string, error) {
	p, err := windowsTaskPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		return p, nil
	}
	startup, err := windowsStartupScriptPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(startup); err == nil {
		return startup, nil
	}
	return p, nil
}

func init() { supervisorLockPathFn = windowsTaskPath }
func supervisorName() string {
	p, err := windowsTaskPath()
	if err == nil {
		if _, err := os.Stat(p); err == nil {
			return "windows-task-scheduler"
		}
	}
	p, err = windowsStartupScriptPath()
	if err == nil {
		if _, err := os.Stat(p); err == nil {
			return "windows-startup"
		}
	}
	return "windows-task-scheduler"
}

func windowsTaskSpecFromDefinition(data []byte, dir string) (windowsTaskSpec, error) {
	t, err := parseWindowsTask(data)
	if err != nil {
		return windowsTaskSpec{}, err
	}
	sid, err := windowsSIDFn()
	if err != nil {
		return windowsTaskSpec{}, err
	}
	// The config binds the task; the executable may belong to a prior install.
	// Recover the executable from our encoded action, then regenerate and check
	// the entire expected action so arbitrary PowerShell is never trusted.
	if len(t.Actions.Exec) != 1 {
		return windowsTaskSpec{}, fmt.Errorf("unexpected task action")
	}
	for _, mode := range []string{"supervise", "serve --no-browser"} {
		for _, noAuto := range []bool{false, true} {
			// Decode only the launcher shape emitted by this installer.
			prefix := "-NoLogo -NoProfile -NonInteractive -WindowStyle Hidden -EncodedCommand "
			encoded := strings.TrimPrefix(t.Actions.Exec[0].Arguments, prefix)
			script, err := decodePowerShell(encoded)
			if err != nil {
				continue
			}
			start := "$ErrorActionPreference='Stop'; $env:TSLINK_CONFIG_DIR=" + powershellLiteral(dir) + "; $env:TSLINK_MANAGED_LOGS='1'; & '"
			end := "' " + mode
			if noAuto {
				end += " --no-auto-provision"
			}
			end += "; exit $LASTEXITCODE"
			if !strings.HasPrefix(script, start) || !strings.HasSuffix(script, end) {
				continue
			}
			exe := strings.TrimSuffix(strings.TrimPrefix(script, start), end)
			exe = strings.ReplaceAll(exe, "''", "'")
			spec := windowsTaskSpec{sid, exe, dir, windowsPowerShellPath(), noAuto}
			if windowsTaskOwned(data, spec) {
				return spec, nil
			}
		}
	}
	return windowsTaskSpec{}, fmt.Errorf("task definition does not match TSLink user/config/action ownership")
}

func windowsConfigEnvironment(dir string) string {
	return "shell.Environment(\"PROCESS\")(\"TSLINK_CONFIG_DIR\") = " + vbsStringLiteral(dir)
}

func supervisorConfigMatches(data []byte, dir string) bool {
	return windowsTaskConfigMatches(data, dir) || bytes.Contains(data, []byte(windowsConfigEnvironment(dir)+"\r\n"))
}

func checkUnregisteredSupervisor() error {
	name, err := windowsTaskName()
	if err != nil {
		return err
	}
	s, err := windowsSchedulerFn("query", name, nil)
	if err != nil {
		return fmt.Errorf("inspect Task Scheduler (fallback: tslink install --startup): %w", err)
	}
	if s.Exists {
		return fmt.Errorf("scheduler task exists without a local definition; inspect it before explicitly running 'tslink install'")
	}
	path, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	dir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	if !supervisorConfigMatches(data, dir) {
		return fmt.Errorf("existing Startup entry is not bound to config %s", dir)
	}
	return nil
}

func checkSupervisorProcessScope() error {
	name, err := windowsTaskName()
	if err != nil {
		return err
	}
	s, err := windowsSchedulerFn("query", name, nil)
	if err != nil {
		if _, ok := detectWindowsStartup(); ok {
			return nil
		}
		return err
	}
	if s.State == 4 || len(s.Engines) > 0 {
		return fmt.Errorf("scheduler task still running; stop it before replacing its config")
	}
	return nil
}

func startInstalledDaemon(ctx context.Context, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, _ := windowsTaskPath()
	if _, err := os.Stat(path); err == nil {
		name, err := windowsTaskName()
		if err != nil {
			return err
		}
		_, err = windowsSchedulerFn("run", name, nil)
		return err
	}
	path, err := windowsStartupScriptPath()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "wscript.exe", path)
	command.Stdout, command.Stderr = out, out
	return command.Run()
}

func detectSupervision(pidPath string, running bool, pid int) Supervision {
	name, err := windowsTaskName()
	if err != nil {
		return unmanagedSupervision(running, "Cannot inspect Windows user SID: "+err.Error())
	}
	s, err := windowsSchedulerFn("query", name, nil)
	if err != nil {
		path, _ := windowsTaskPath()
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			if fallback, ok := detectWindowsStartup(); ok {
				fallback.Detail += "; Task Scheduler unavailable: " + err.Error()
				return fallback
			}
		}
		return unmanagedSupervision(running, "Task Scheduler status unavailable: "+err.Error())
	}
	if s.Exists {
		dir, err := absoluteConfigDir()
		if err != nil {
			return unmanagedSupervision(running, err.Error())
		}
		spec, err := windowsTaskSpecFromDefinition([]byte(s.XML), dir)
		if err != nil || !s.Enabled || s.State == 0 || s.State == 1 || !windowsTaskMatches([]byte(s.XML), spec) {
			return unmanagedSupervision(running, "Task Scheduler user/config/action/restart policy or enabled state is unverified; run tslink install")
		}
		if running && (!windowsDaemonRunningFn(pidPath) || s.State != 4 || !windowsTaskOwnsPIDFn(pid, s.Engines, spec.Executable)) {
			return unmanagedSupervision(true, "Task Scheduler does not own the verified daemon PID; inspect tslink doctor before reinstalling")
		}
		path, _ := windowsTaskPath()
		result := Supervision{Manager: "windows-task-scheduler", Installed: true, Autostart: true, AutostartScope: autostartScopeLogin,
			Path: path, Detail: fmt.Sprintf("Task %s launches the built-in supervisor at sign-in. Daemon crash backoff: 1s doubling to 60s; reset after 5 minutes; breaker after 8 consecutive unstable runs. Task RestartOnFailure (60s/255) is a launcher backstop, not verified supervisor crash recovery. No boot before sign-in or survival after logout. State=%d, last_result=%d. Undo: tslink uninstall", name, s.State, s.LastResult)}
		record, recordErr := readBuiltinSupervisorFn(pidPath)
		if recordErr != nil {
			result.Detail += "; built-in supervisor state unavailable; run tslink install"
			if running {
				return unmanagedSupervision(true, result.Detail)
			}
			return result
		}
		result.RuntimeState, result.FailureReason = record.State, record.Reason
		alive, identityErr := builtinSupervisorAliveFn(record.Instance)
		if identityErr != nil && builtinSupervisorTerminal(record.State) {
			// Retain the historical terminal reason without claiming a live PID.
			// The task definition was independently verified above.
			alive, identityErr = false, nil
		}
		if identityErr != nil || (alive && (!strings.EqualFold(record.Instance.Executable, spec.Executable) || s.State != 4 || !windowsTaskOwnsPIDFn(record.Instance.PID, s.Engines, spec.Executable))) {
			return unmanagedSupervision(running, "Built-in supervisor identity/task ownership unverified; inspect tslink doctor")
		}
		if alive {
			result.SupervisorPID = record.Instance.PID
			result.RestartOnExit = record.State == "running" || record.State == "starting" || record.State == "restarting"
		}
		if running && (!alive || record.State != "running" || record.DaemonPID != pid || !windowsTaskOwnsPIDFn(pid, []int{record.Instance.PID}, spec.Executable)) {
			return unmanagedSupervision(true, "Built-in supervisor does not own the verified daemon PID; inspect tslink doctor")
		}
		result.Detail += fmt.Sprintf("; supervisor=%s, failures=%d, reason=%s", record.State, record.Failures, record.Reason)
		if record.State == "restarting" {
			result.Detail += fmt.Sprintf(", next_start=%s", record.NextStart.UTC().Format(time.RFC3339))
		}
		if record.State == "circuit_open" || record.State == "failed" {
			result.Detail += "; crash recovery stopped; inspect logs, then run tslink install"
		}
		return result
	}
	if fallback, ok := detectWindowsStartup(); ok {
		return fallback
	}
	return unmanagedSupervision(running, "No verified Windows TSLink supervisor; run tslink install")
}

func detectWindowsStartup() (Supervision, bool) {
	path, err := windowsStartupScriptPath()
	if err != nil {
		return Supervision{}, false
	}
	data, err := os.ReadFile(path)
	dir, dirErr := absoluteConfigDir()
	if err != nil || dirErr != nil || !supervisorConfigMatches(data, dir) {
		return Supervision{}, false
	}
	return windowsStartupSupervision(path), true
}

func callWindowsScheduler(operation, name string, definition []byte) (windowsSchedulerStatus, error) {
	// EncodedCommand serializes module-loading progress as CLIXML on stderr.
	// Keep the successful COM/JSON response clean without suppressing errors.
	script := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); " +
		"$svc=New-Object -ComObject 'Schedule.Service'; $svc.Connect(); $folder=$svc.GetFolder('\\'); $name=" + powershellLiteral(name) + "; "
	if operation == "register" {
		xmlString := windowsTaskCOMString(string(definition))
		script += "$xml=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String(" + powershellLiteral(base64.StdEncoding.EncodeToString([]byte(xmlString))) + ")); " +
			"$null=$folder.RegisterTask($name,$xml,6,$null,$null,3,$null); "
	}
	// Only a precise HRESULT proves absence. Localized schtasks text and all
	// access-denied/service errors must never be interpreted as 'not installed'.
	script += "try { $task=$folder.GetTask($name) } catch { $e=$_.Exception; while ($e.InnerException) { $e=$e.InnerException }; if ($e.HResult -eq -2147024894) { '{\"exists\":false}'; exit 0 }; throw }; "
	switch operation {
	case "query", "register":
	case "run":
		script += "$null=$task.Run($null); "
	case "disable":
		script += "$task.Enabled=$false; "
	case "delete":
		script += "$folder.DeleteTask($name,0); '{\"exists\":false}'; exit 0; "
	default:
		return windowsSchedulerStatus{}, fmt.Errorf("unknown scheduler operation %q", operation)
	}
	// The scheduler may expand a SID to DOMAIN\name. Resolve those identifiers
	// back to SIDs before ownership checks; failed translations remain errors.
	script += "$xml=[xml]$task.Xml; $ns=[Xml.XmlNamespaceManager]::new($xml.NameTable); $ns.AddNamespace('t'," + powershellLiteral(windowsTaskNamespace) + "); " +
		"foreach ($n in $xml.SelectNodes('/t:Task/t:Principals/t:Principal/t:UserId | /t:Task/t:Triggers/t:LogonTrigger/t:UserId',$ns)) { " +
		"if ($n.InnerText -notmatch '^S-\\d(-\\d+)+$') { $n.InnerText=([Security.Principal.NTAccount]::new($n.InnerText)).Translate([Security.Principal.SecurityIdentifier]).Value } }; " +
		"$engines=@($task.GetInstances(0) | ForEach-Object { [int]$_.EnginePID }); " +
		"@{exists=$true; xml=[string]$xml.OuterXml; enabled=[bool]$task.Enabled; state=[int]$task.State; engines=$engines; last_result=[long]$task.LastTaskResult} | ConvertTo-Json -Compress -Depth 4"
	var data []byte
	var err error
	args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", powershellEncoded(script)}
	if operation == "query" {
		data, err = managerOutputFn(windowsPowerShellPath(), args...)
	} else {
		data, err = runBoundedManagerCommand(windowsPowerShellPath(), 20*time.Second, args...)
	}
	if err != nil {
		return windowsSchedulerStatus{}, fmt.Errorf("task scheduler %s failed: %w", operation, err)
	}
	return parseWindowsSchedulerStatus(data)
}
