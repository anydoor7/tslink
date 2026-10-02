param(
    [string]$Binary,
    [int]$RestartTimeoutSeconds = 100
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
function Wait-Daemon([int]$DifferentFrom = 0, [int]$Seconds = 30) {
    $deadline = [DateTime]::UtcNow.AddSeconds($Seconds)
    do {
        $s = Invoke-TSLink -Arguments @('status','--json')
        if ($s.daemon_running -and $s.daemon_pid -ne $DifferentFrom -and
            $s.supervision.manager -eq 'windows-task-scheduler' -and
            $s.supervision.restart_on_exit -and $s.supervision.autostart_scope -eq 'login') {
            $runtimePath = Join-Path $env:TSLINK_CONFIG_DIR 'runtime.json'
            if (Test-Path $runtimePath) {
                $runtime = Get-Content -Raw $runtimePath | ConvertFrom-Json
                $age = [DateTime]::UtcNow - [DateTime]::Parse($runtime.updated_at).ToUniversalTime()
                if ($runtime.daemon_pid -eq $s.daemon_pid -and $age.TotalSeconds -ge 0 -and $age.TotalSeconds -lt 30) { return $s.daemon_pid }
            }
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'Verified daemon and matching runtime artifact did not appear before deadline'
}
try {
    if ($env:OS -ne 'Windows_NT') { throw 'Windows is required' }
    if (-not $Binary -or -not $env:APPDATA) { throw 'Binary argument and APPDATA are required' }
    $startup = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Startup\tslink.vbs'
    $definition = Join-Path $env:APPDATA 'tslink-supervisor\task.xml'
    if ($RestartTimeoutSeconds -lt 75) { throw 'Restart deadline must allow the 60-second backoff' }
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
    Stop-Process -Id $first -Force
    $second = Wait-Daemon -DifferentFrom $first -Seconds $RestartTimeoutSeconds
    if (([DateTime]::UtcNow - $killedAt).TotalSeconds -lt 50) { throw 'Crash restarted before the configured backoff' }
    Write-Output "PASS crash restart: $first -> $second"
    $stop = Invoke-TSLink -Arguments @('stop','--json')
    if (-not $stop.stopped) { throw 'Graceful stop did not report success' }
    Start-Sleep -Seconds 65
    $s = Invoke-TSLink -Arguments @('status','--json')
    if ($s.daemon_running) { throw 'Graceful successful stop unexpectedly restarted' }
    $null = Invoke-TSLink -Arguments @('install','--json')
    $null = Wait-Daemon
    $uninstall = Invoke-TSLink -Arguments @('uninstall','--json')
    if (-not $uninstall.removed) { throw 'Uninstall did not remove task' }
    $installed = $false
    $s = Invoke-TSLink -Arguments @('status','--json')
    if ($s.daemon_running -or $s.supervision.installed -or (Test-Path $definition)) { throw 'Uninstall left daemon or supervision' }
    $exitCode = 0
} catch {
    Write-Output ('FAIL Windows supervision: ' + $_.Exception.Message)
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
