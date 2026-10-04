param(
    [string]$Binary,
    [int]$RestartTimeoutSeconds = 30
)
# Run in a clean, disposable Windows user session. No elevation, prompts,
# credentials, tailnet nodes or network services are used. Existing installations
# and TSLink credentials are refused before any mutation.
$ErrorActionPreference = 'Stop'
$exitCode = 1
$installed = $false
$scratch = $null
$previousConfig = $env:TSLINK_CONFIG_DIR
$oldSkipSSH = $env:TSLINK_DOCTOR_SKIP_TAILSCALE_SSH
$startup = $null
$definition = $null

function Invoke-TSLink([string[]]$Arguments) {
    $text = & $script:Binary @Arguments
    if ($LASTEXITCODE -ne 0) { throw "tslink $($Arguments[0]) exited $LASTEXITCODE" }
    $result = ($text -join "`n") | ConvertFrom-Json
    if (-not $result.ok -or $result.schema_version -ne 1) { throw 'Invalid TSLink result envelope' }
    return $result.data
}
function Read-SharedText([string]$Path) {
    # Match the product's shared state readers: coexist with writers and atomic
    # replacement. ReadAllText's FileShare.Read can itself break observation.
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read,
        ([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
    try {
        $reader = [IO.StreamReader]::new($stream)
        try { return $reader.ReadToEnd() }
        finally { $reader.Dispose() }
    } finally { $stream.Dispose() }
}
function Get-NewDaemonPID([string]$Path, [int]$DifferentFrom) {
    if (Test-Path $Path) {
        $text = (Read-SharedText $Path).Trim()
        if ($text -match '^\d+$' -and [int]$text -gt 0 -and [int]$text -ne $DifferentFrom) { return [int]$text }
    }
    return 0
}
function Write-FailureEvidence {
    # Capture before uninstall removes the process/state that explains failure.
    if (-not $script:scratch -or $env:TSLINK_CONFIG_DIR -ne $script:scratch) {
        Write-Output 'FAILURE_EVIDENCE unavailable before isolated config setup'
        return
    }
    try { Write-Output ('FAILURE_STATUS ' + ((& $script:Binary status --json) -join "`n")) }
    catch { Write-Output ('FAILURE_STATUS_ERROR ' + $_.Exception.Message) }
    foreach ($name in @('supervisor.json','runtime.json','logs\tslink.err.log','logs\tslink.out.log')) {
        try {
            $path = Join-Path $env:TSLINK_CONFIG_DIR $name
            if (Test-Path $path) { Write-Output ('FAILURE_' + $name + ' ' + (Read-SharedText $path)) }
            else { Write-Output ('FAILURE_' + $name + ' absent') }
        } catch { Write-Output ('FAILURE_' + $name + '_ERROR ' + $_.Exception.Message) }
    }
}
function Wait-Daemon([int]$DifferentFrom = 0, [int]$Seconds = 30) {
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    do {
        $s = Invoke-TSLink -Arguments @('status','--json')
        if ($s.daemon_running -and $s.daemon_pid -ne $DifferentFrom -and
            $s.supervision.manager -eq 'windows-task-scheduler' -and
            $s.supervision.restart_on_exit -and $s.supervision.autostart_scope -eq 'login') {
            $runtimePath = Join-Path $env:TSLINK_CONFIG_DIR 'runtime.json'
            if (Test-Path $runtimePath) {
                $runtime = Read-SharedText $runtimePath | ConvertFrom-Json
                $age = [DateTime]::UtcNow - [DateTime]::Parse($runtime.updated_at).ToUniversalTime()
                if ($runtime.daemon_pid -eq $s.daemon_pid -and $age.TotalSeconds -ge 0 -and $age.TotalSeconds -lt 30) { return $s.daemon_pid }
            }
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    Write-Output ('WAIT_TIMEOUT_STATUS ' + ($s | ConvertTo-Json -Compress -Depth 5))
    foreach ($name in @('runtime.json','supervisor.json')) {
        $path = Join-Path $env:TSLINK_CONFIG_DIR $name
        if (Test-Path $path) { Write-Output ('WAIT_TIMEOUT_' + $name + ' ' + (Read-SharedText $path).Trim()) }
    }
    throw 'Verified daemon and matching runtime artifact did not appear before deadline'
}
try {
    if ($env:OS -ne 'Windows_NT') { throw 'Windows is required' }
    if (-not $Binary -or -not $env:APPDATA) { throw 'Binary argument and APPDATA are required' }
    $startup = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs'
    $definition = Join-Path $env:APPDATA 'tslink-supervisor\task.xml'
    if ($RestartTimeoutSeconds -lt 5) { throw 'Restart deadline must allow the first 1-second backoff and initialization' }
    $Binary = (Resolve-Path $Binary).Path
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $taskName = 'TSLink-' + $sid
    $svc = New-Object -ComObject 'Schedule.Service'
    $svc.Connect()
    $folder = $svc.GetFolder('\')
    try {
        $null = $folder.GetTask($taskName)
        throw 'Existing TSLink scheduled task; use a clean disposable user'
    } catch {
        $e = $_.Exception
        while ($e.InnerException) { $e = $e.InnerException }
        if ($e.HResult -ne -2147024894) { throw }
    }
    if ((Test-Path $startup) -or (Test-Path $definition)) { throw 'Existing TSLink supervisor files; refusing to replace them' }
    $credentialMetadata = & "$env:SystemRoot\System32\cmdkey.exe" /list
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect Credential Manager metadata' }
    if (($credentialMetadata -join "`n") -match '(?i)tslink') { throw 'Existing TSLink credentials; use a clean disposable user' }
    foreach ($key in @('TSLINK_API_KEY','TSLINK_CLIENT_SECRET')) {
        foreach ($scope in @('Process','User','Machine')) {
            if ([Environment]::GetEnvironmentVariable($key,$scope)) { throw "Credential environment $key exists; refusing smoke run" }
        }
    }
    $scratch = Join-Path ([IO.Path]::GetTempPath()) ('tslink Windows smoke ' + [Guid]::NewGuid())
    $null = New-Item -ItemType Directory -Path $scratch
    $env:TSLINK_CONFIG_DIR = $scratch
    $env:TSLINK_DOCTOR_SKIP_TAILSCALE_SSH = '1'
    # Empty registry proves daemon business initialization without enrolling a
    # tsnet node or touching the live Tailscale API.
    [IO.File]::WriteAllText((Join-Path $scratch 'registry.json'), '{"schema_version":1,"services":[]}')
    $installed = $true # An error can still leave a registered task to clean up.
    $install = Invoke-TSLink -Arguments @('install','--json')
    if ($install.service_manager -ne 'windows-task-scheduler' -or -not $install.started) { throw 'Install did not verify Task Scheduler ownership' }
    $first = Wait-Daemon
    $killedAt = [DateTime]::UtcNow
    & "$env:SystemRoot\System32\taskkill.exe" /PID $first /F
    if ($LASTEXITCODE -ne 0) { throw 'taskkill did not kill the daemon' }
    $pidDeadline = $killedAt.AddSeconds($RestartTimeoutSeconds)
    $newPID = 0
    do {
        $newPID = Get-NewDaemonPID -Path (Join-Path $scratch 'tslink.pid') -DifferentFrom $first
        if ($newPID) { break }
        Start-Sleep -Milliseconds 25
    } while ([DateTime]::UtcNow -lt $pidDeadline)
    if (-not $newPID) { throw 'New daemon PID did not appear within the first crash backoff bound' }
    $pidRestartSeconds = ([DateTime]::UtcNow - $killedAt).TotalSeconds
    $second = Wait-Daemon -DifferentFrom $first -Seconds $RestartTimeoutSeconds
    $restartSeconds = ([DateTime]::UtcNow - $killedAt).TotalSeconds
    if ($second -ne $newPID -or $pidRestartSeconds -lt 1 -or $restartSeconds -gt $RestartTimeoutSeconds) { throw 'Crash restart outside the first backoff/initialization bound' }
    Write-Output "PASS crash restart: $first -> $second; new PID in $pidRestartSeconds seconds; verified runtime/ownership in $restartSeconds seconds (first backoff 1s)"
    $stop = Invoke-TSLink -Arguments @('stop','--json')
    if (-not $stop.stopped) { throw 'Graceful stop did not report success' }
    Start-Sleep -Seconds 65
    $s = Invoke-TSLink -Arguments @('status','--json')
    Write-Output ('STOP_STATUS ' + ($s | ConvertTo-Json -Compress -Depth 5))
    Write-Output ('STOP_SUPERVISOR ' + (Read-SharedText (Join-Path $scratch 'supervisor.json')).Trim())
    if ($s.daemon_running -or $s.supervision.supervisor_pid -or $s.supervision.runtime_state -ne 'stopped') { throw 'Graceful successful stop unexpectedly restarted or left a supervisor' }
    $null = Invoke-TSLink -Arguments @('install','--json')
    $null = Wait-Daemon
    $uninstall = Invoke-TSLink -Arguments @('uninstall','--json')
    if (-not $uninstall.removed) { throw 'Uninstall did not remove task' }
    $installed = $false
    $s = Invoke-TSLink -Arguments @('status','--json')
    if ($s.daemon_running -or $s.supervision.installed -or (Test-Path $definition) -or (Test-Path (Join-Path $scratch 'supervisor.json'))) { throw 'Uninstall left daemon or supervision' }
    $exitCode = 0
} catch {
    Write-Output ('FAIL Windows supervision: ' + $_.Exception.Message)
    Write-FailureEvidence
} finally {
    if ($installed) {
        try { $null = Invoke-TSLink -Arguments @('uninstall','--json'); $installed = $false }
        catch { Write-Output 'FAIL cleanup: task/config retained for inspection'; $exitCode = 1 }
    }
    if ($scratch -and -not $installed) {
        try { Remove-Item -Recurse -Force $scratch }
        catch { Write-Output 'FAIL cleanup: scratch config could not be removed'; $exitCode = 1 }
    }
    $env:TSLINK_CONFIG_DIR = $previousConfig
    $env:TSLINK_DOCTOR_SKIP_TAILSCALE_SSH = $oldSkipSSH
}
if ($exitCode -eq 0) { Write-Output 'PASS Windows supervision: install, runtime artifact, crash restart, graceful stop, reinstall, uninstall' }
exit $exitCode
