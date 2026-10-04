param(
    [string]$SmokeScript = (Join-Path $PSScriptRoot 'windows-supervision-smoke.ps1'),
    [string]$FixtureParent = [IO.Path]::GetTempPath()
)
# Safe standalone controls: import only observer functions, never the lifecycle
# body (which installs, taskkills and uninstalls). CLI status is mocked below.
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'Native Windows is required for sharing controls' }
$tokens = $null; $parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile($SmokeScript, [ref]$tokens, [ref]$parseErrors)
if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
foreach ($name in @('Read-SharedText','Read-PendingText','Get-NewDaemonPID','Wait-Daemon')) {
    $nodes = @($ast.FindAll({ param($n) $n -is [Management.Automation.Language.FunctionDefinitionAst] -and $n.Name -eq $name }, $false))
    if ($nodes.Count -ne 1) { throw "Expected one observer function $name" }
    . ([scriptblock]::Create($nodes[0].Extent.Text))
}
$script:passed = 0
function Check([string]$CheckName, [scriptblock]$Body) {
    & $Body
    $script:passed++
    Write-Output "PASS $CheckName"
}
function Equal($Actual, $Expected) {
    if ($Actual -cne $Expected) { throw "Expected [$Expected], got [$Actual]" }
}
function Reject([scriptblock]$Body, [string]$Message) {
    $caught = $null
    try { $null = & $Body } catch { $caught = $_ }
    if (-not $caught -or $caught.Exception.Message -notlike "*$Message*") { throw "Expected rejection [$Message], got [$caught]" }
}
function Invoke-TSLink([string[]]$Arguments) {
    Equal ($Arguments -join ' ') 'status --json'
    return $script:status
}
# Speed only the bounded polling in negative controls; no product deadline changes.
function Start-Sleep { param([int]$Milliseconds) }
function Set-GoodState {
    $script:status = [pscustomobject]@{
        daemon_running = $true; daemon_pid = 222
        supervision = [pscustomobject]@{ manager = 'windows-task-scheduler'; restart_on_exit = $true; autostart_scope = 'login' }
    }
    [IO.File]::WriteAllText($script:runtimePath, ('{"daemon_pid":222,"updated_at":"' + [DateTime]::UtcNow.ToString('o') + '"}'))
}
$previousConfig = $env:TSLINK_CONFIG_DIR
$root = Join-Path $FixtureParent ('tslink-smoke-reader-' + [Guid]::NewGuid())
$null = New-Item -ItemType Directory -Path $root
$env:TSLINK_CONFIG_DIR = $root
$pidPath = Join-Path $root 'tslink.pid'
$script:runtimePath = Join-Path $root 'runtime.json'
try {
    Write-Output ('SOURCE ' + (Get-FileHash -Algorithm SHA256 $SmokeScript).Hash)
    Write-Output ('RUNTIME ' + $PSVersionTable.PSVersion + ' ' + [Environment]::OSVersion)
    Check 'unlocked PID control' {
        [IO.File]::WriteAllText($pidPath, "222`n")
        Equal (Get-NewDaemonPID $pidPath 111) 222
    }
    Check 'old RED and shared writer GREEN' {
        $writer = [IO.File]::Open($pidPath, 'Open', 'ReadWrite', ([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
        try {
            $caught = $null
            try { $null = [IO.File]::ReadAllText($pidPath) } catch { $caught = $_.Exception.GetBaseException() }
            if (-not $caught -or ($caught.HResult -band 65535) -ne 32) { throw 'Old reader did not fail with ERROR_SHARING_VIOLATION' }
            Write-Output ('OLD_RED ' + $caught.Message)
            Equal (Get-NewDaemonPID $pidPath 111) 222
        } finally { $writer.Dispose() }
    }
    Check 'replacement returns fresh contents' {
        $next = Join-Path $root 'next.pid'
        [IO.File]::WriteAllText($next, '333')
        [IO.File]::Replace($next, $pidPath, (Join-Path $root 'old.pid'))
        Equal (Get-NewDaemonPID $pidPath 222) 333
    }
    Check 'delete-access handle is shared' {
        # DeleteOnClose requests DELETE access. A reader without FileShare.Delete
        # fails while this owned handle is open, even if it shares writes.
        $held = [IO.FileStream]::new($pidPath, [IO.FileMode]::Open, [IO.FileAccess]::Read,
            ([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete), 4096, [IO.FileOptions]::DeleteOnClose)
        try { Equal (Get-NewDaemonPID $pidPath 222) 333 }
        finally { $held.Dispose() }
    }
    Check 'missing PID never becomes fresh' { Equal (Get-NewDaemonPID $pidPath 111) 0 }
    foreach ($text in @('111', '0', '-2', 'not-a-pid', '222 extra', '')) {
        Check "reject PID [$text]" {
            [IO.File]::WriteAllText($pidPath, $text)
            Equal (Get-NewDaemonPID $pidPath 111) 0
        }
    }
    Check 'single-attempt shared read still reports exclusive lock' {
        $held = [IO.File]::Open($pidPath, 'Open', 'ReadWrite', 'None')
        try { Reject { Read-SharedText $pidPath } 'being used by another process' }
        finally { $held.Dispose() }
    }
    Check 'bounded PID observer treats sharing conflict as pending' {
        $held = [IO.File]::Open($pidPath, 'Open', 'ReadWrite', 'None')
        try { Equal (Get-NewDaemonPID $pidPath 111) 0 }
        finally { $held.Dispose() }
    }
    Check 'permanent invalid path remains an error' {
        $caught = $null
        try { $null = Read-PendingText ($root + [char]0) } catch { $caught = $_.Exception.GetBaseException() }
        if (-not ($caught -is [ArgumentException])) { throw "Expected invalid-path ArgumentException, got [$caught]" }
    }
    Check 'verified daemon and matching artifact control' {
        Set-GoodState
        Equal (Wait-Daemon -DifferentFrom 111 -Seconds 0) 222
    }
    Check 'runtime reader coexists with writer' {
        Set-GoodState
        $held = [IO.File]::Open($runtimePath, 'Open', 'ReadWrite', ([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
        try { Equal (Wait-Daemon -DifferentFrom 111 -Seconds 0) 222 }
        finally { $held.Dispose() }
    }
    Check 'persistent runtime lock fails at the original deadline' {
        Set-GoodState
        $held = [IO.File]::Open($runtimePath, 'Open', 'ReadWrite', 'None')
        try { Reject { Wait-Daemon -DifferentFrom 111 -Seconds 0 } 'Verified daemon and matching runtime artifact did not appear before deadline' }
        finally { $held.Dispose() }
    }
    Check 'supervisor accounting accepts one crash and rejects absorbed failure' {
        Set-GoodState
        $supervisorPath = Join-Path $root 'supervisor.json'
        [IO.File]::WriteAllText($supervisorPath, '{"state":"running","daemon_pid":222,"failures":1}')
        Equal (Wait-Daemon -DifferentFrom 111 -Seconds 0 -ExpectedFailures 1) 222
        [IO.File]::WriteAllText($supervisorPath, '{"state":"running","daemon_pid":222,"failures":2}')
        Reject { Wait-Daemon -DifferentFrom 111 -Seconds 0 -ExpectedFailures 1 } 'Unexpected supervisor failure count'
    }
    $badStates = [ordered]@{
        'not running' = { $script:status.daemon_running = $false }
        'old PID' = { $script:status.daemon_pid = 111 }
        'unowned daemon' = { $script:status.supervision.manager = 'manual' }
        'no restart policy' = { $script:status.supervision.restart_on_exit = $false }
        'wrong autostart scope' = { $script:status.supervision.autostart_scope = 'boot' }
        'wrong runtime PID' = { [IO.File]::WriteAllText($runtimePath, ('{"daemon_pid":333,"updated_at":"' + [DateTime]::UtcNow.ToString('o') + '"}')) }
        'stale artifact' = { [IO.File]::WriteAllText($runtimePath, '{"daemon_pid":222,"updated_at":"2000-01-01T00:00:00Z"}') }
        'future artifact' = { [IO.File]::WriteAllText($runtimePath, '{"daemon_pid":222,"updated_at":"2099-01-01T00:00:00Z"}') }
        'missing artifact' = { Remove-Item $runtimePath }
    }
    foreach ($name in $badStates.Keys) {
        Check "reject $name" {
            Set-GoodState
            & $badStates[$name]
            Reject { Wait-Daemon -DifferentFrom 111 -Seconds 0 } 'Verified daemon and matching runtime artifact did not appear before deadline'
        }
    }
    Check 'malformed artifact is an error' {
        Set-GoodState
        [IO.File]::WriteAllText($runtimePath, '{invalid')
        $caught = $null
        try { $null = Wait-Daemon -DifferentFrom 111 -Seconds 0 } catch { $caught = $_ }
        if (-not $caught -or $caught.FullyQualifiedErrorId -notlike '*ConvertFromJsonCommand*') {
            throw "Expected ConvertFrom-Json parser failure, got [$caught]"
        }
        Write-Output ('MALFORMED_REJECTED ' + $caught.FullyQualifiedErrorId)
    }
    Write-Output "RESULT passed=$script:passed failed=0"
} finally {
    $env:TSLINK_CONFIG_DIR = $previousConfig
    Remove-Item -Recurse -Force $root
}
